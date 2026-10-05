package knowledgebase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Git workspaces are immutable generations. A successful action journals the
// generation pointer together with live content, registries and backup state.
// A failed Git action or invalid incoming tree discards the entire candidate.
type gitWorkspace struct {
	Directory    string `json:"directory"`
	Branch       string `json:"branch"`
	PublishedTip string `json:"published_tip"`
}

func (s *Service) gitWorkspacePath() string { return filepath.Join(s.private, "git-workspace.json") }
func (s *Service) readGitWorkspace() (gitWorkspace, error) {
	var state gitWorkspace
	b, err := os.ReadFile(s.gitWorkspacePath())
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if json.Unmarshal(b, &state) != nil || !strings.HasPrefix(state.Directory, "git-generation-") || filepath.Base(state.Directory) != state.Directory {
		return state, kbErr("STORAGE_UNAVAILABLE", "Invalid Git workspace state.")
	}
	return state, nil
}
func GitReadOperation(op string) bool {
	switch op {
	case "status", "diff", "log", "branches", "stashes", "blame", "show":
		return true
	}
	return false
}
func validGitOperation(op string) bool {
	if GitReadOperation(op) {
		return true
	}
	switch op {
	case "stage", "unstage", "commit", "discard", "checkout", "create_branch", "delete_branch", "stash", "stash_apply", "stash_pop", "stash_drop", "resolve", "pull", "push":
		return true
	}
	return false
}

// RunGit is the repository-wide boundary used by Files and the existing backup
// MCP tool. The callback is server code, never a client-supplied shell command.
func (s *Service) RunGit(ctx context.Context, p Principal, a map[string]any, run func(context.Context, string) (any, error)) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	op := stringArg(a, "op")
	if !validGitOperation(op) {
		return nil, badArg("Unknown Git operation.")
	}
	write := !GitReadOperation(op)
	if p.IdentityID == "" || p.AccessOnly || p.BindingPolicy != nil || write && p.Caps != nil {
		return nil, kbErr("FORBIDDEN", "Repository actions require an unrestricted Knowledge Base connection.")
	}
	if p.Recheck != nil {
		if err := p.Recheck(ctx); err != nil {
			return nil, err
		}
	}
	if p.IsAdmin && !s.identityKnown(p.IdentityID) {
		if err := s.registerAdministrator(ctx, p.IdentityID); err != nil {
			return nil, err
		}
	}
	unlockPub, err := s.publicationLock()
	if err != nil {
		return nil, err
	}
	defer unlockPub()
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = s.recover(); err != nil {
		return nil, err
	}
	authorize := func() error {
		if !s.IdentityActive(ctx, p.IdentityID) {
			return kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
		}
		if p.Recheck != nil {
			if err := p.Recheck(ctx); err != nil {
				return err
			}
		}
		role := roleReader
		if write {
			role = roleEditor
		}
		return s.require(p, "", role)
	}
	if err = authorize(); err != nil {
		return nil, err
	}
	if err = s.cleanupGitGenerations(); err != nil {
		return nil, err
	}
	var reqPath, hash string
	if write {
		reqPath, hash, err = s.requestPath(p, "git_knowledgebase", a)
		if err != nil {
			return nil, err
		}
		if b, e := os.ReadFile(reqPath); e == nil {
			var rec requestRecord
			if json.Unmarshal(b, &rec) != nil {
				return nil, kbErr("STORAGE_UNAVAILABLE", "Invalid Git request state.")
			}
			if rec.Hash != hash {
				return nil, kbErr("REQUEST_ID_REUSE", "This request ID was used with different arguments.")
			}
			return rec.Result, nil
		}
	}
	if intent, err := s.readGitPushIntent(); err != nil {
		return nil, err
	} else if intent != nil {
		if op != "push" || intent.Owner != p.IdentityID || intent.RequestPath != reqPath || intent.Hash != hash {
			return nil, kbErr("BACKUP_OUTCOME_UNKNOWN", "Retry the original Git push to reconcile its delivery before other repository actions.")
		}
		return s.finishGitPush(ctx, *intent, authorize)
	}
	dest, err := s.backupDestination()
	if err != nil {
		return nil, err
	}
	if dest.Remote == "" {
		return nil, kbErr("BACKUP_NOT_CONFIGURED", "Configure a Git repository first.")
	}
	if err = s.ensureRepo(ctx); err != nil {
		return nil, err
	}
	receipts, err := s.receipts()
	if err != nil {
		return nil, err
	}
	for _, r := range receipts {
		if r.State == "PUSH_UNKNOWN" {
			return nil, kbErr("PUSH_UNKNOWN", "Reconcile the uncertain backup push before using repository actions.")
		}
	}
	previous, err := s.readGitWorkspace()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(s.private, "git-generation-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()
	if previous.Directory != "" {
		if err = copyGitDirectory(filepath.Join(s.private, previous.Directory, ".git"), filepath.Join(dir, ".git")); err != nil {
			return nil, err
		}
	} else {
		if _, err = gitWorkspaceRun(ctx, dir, "init", "--template=", "--initial-branch="+dest.Branch); err != nil {
			return nil, err
		}
		if _, err = gitWorkspaceRun(ctx, dir, "remote", "add", "origin", dest.Remote); err != nil {
			return nil, err
		}
	}
	st := s.backupState()
	// Receipt pushes advance the published baseline. Preserve any staged work;
	// refuse to silently replace locally committed work on a different history.
	if previous.Directory == "" || previous.PublishedTip != st.Tip {
		head, _ := gitWorkspaceRun(ctx, dir, "rev-parse", "--verify", "HEAD")
		if previous.Directory != "" && head != "" && head != previous.PublishedTip {
			return nil, kbErr("GIT_LOCAL_COMMITS", "Git and receipt publication histories changed independently. Push or reconcile local commits first.")
		}
		if st.Tip != "" {
			if _, err = gitWorkspaceRun(ctx, dir, "fetch", "--no-tags", s.repo(), st.Tip); err != nil {
				return nil, err
			}
			if _, err = gitWorkspaceRun(ctx, dir, "reset", "--mixed", st.Tip); err != nil {
				return nil, err
			}
			if _, err = gitWorkspaceRun(ctx, dir, "update-ref", "refs/remotes/origin/"+dest.Branch, st.Tip); err != nil {
				return nil, err
			}
			gitWorkspaceRun(ctx, dir, "branch", "--set-upstream-to=origin/"+dest.Branch, dest.Branch)
		}
	}
	regs, err := s.registries()
	if err != nil {
		return nil, err
	}
	liveCount, liveBytes := 0, 0
	for _, r := range regs {
		for _, e := range r.Entries {
			b, e2 := s.entryContent(e)
			if e2 != nil {
				return nil, e2
			}
			liveCount++
			liveBytes += len(b)
			if liveCount > 5000 || liveBytes > 50<<20 {
				return nil, kbErr("LIMIT_EXCEEDED", "Live knowledge exceeds the MVP Git workspace limit.")
			}
			name := filepath.Join(dir, filepath.FromSlash(e.Path))
			if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				return nil, err
			}
			if err = os.WriteFile(name, b, 0600); err != nil {
				return nil, err
			}
		}
	}
	if op == "checkout" || op == "pull" {
		dirty, e := gitWorkspaceRun(ctx, dir, "status", "--porcelain")
		if e != nil {
			return nil, e
		}
		if dirty != "" {
			return nil, kbErr("GIT_DIRTY", "Commit or stash unpublished changes before pulling or switching branches.")
		}
	}
	if err = authorize(); err != nil {
		return nil, err
	}
	if op == "pull" {
		if _, err = gitWorkspaceRun(ctx, dir, "fetch", "--prune", "--no-tags", "origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return nil, err
		}
		target := "refs/remotes/origin/" + dest.Branch
		if _, err = gitWorkspaceTree(ctx, dir, target); err != nil {
			return nil, err
		}
		head, _ := gitWorkspaceRun(ctx, dir, "rev-parse", "--verify", "HEAD")
		if head == "" {
			_, err = gitWorkspaceRun(ctx, dir, "reset", "--hard", target)
		} else {
			_, err = gitWorkspaceRun(ctx, dir, "merge", "--ff-only", target)
		}
		if err != nil {
			return nil, err
		}
		gitWorkspaceRun(ctx, dir, "branch", "--set-upstream-to=origin/"+dest.Branch, dest.Branch)
	}
	// Validate a branch before Git materializes it (no symlinks/submodules/control
	// files). New branches reuse the already validated current working tree.
	if op == "checkout" {
		branch := stringArg(a, "branch")
		if branch == "" || strings.HasPrefix(branch, "-") {
			return nil, badArg("Invalid branch.")
		}
		ref := "refs/heads/" + branch
		if remote, _ := a["remote"].(bool); remote {
			ref = "refs/remotes/" + branch
		}
		if _, err = gitWorkspaceTree(ctx, dir, ref); err != nil {
			return nil, err
		}
	}
	result, err := run(ctx, dir)
	if err != nil {
		return nil, err
	}
	if err = authorize(); err != nil {
		return nil, err
	}
	branch, err := gitWorkspaceRun(ctx, dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return nil, err
	}
	files, err := gitWorkspaceFiles(dir)
	if err != nil {
		return nil, err
	}
	changes := []fileChange{}
	if write {
		switch op {
		case "pull", "checkout", "discard", "stash", "stash_apply", "stash_pop", "resolve":
			changes, err = s.gitImportChanges(p, regs, files)
			if err != nil {
				return nil, err
			}
		}
		// Never persist an unresolved merge or binary/index-only path.
		if conflicts, _ := gitWorkspaceRun(ctx, dir, "ls-files", "-u"); conflicts != "" {
			return nil, kbErr("GIT_CONFLICT", "Resolve conflicts before applying changes to live knowledge.")
		}
		if index, err := gitWorkspaceRun(ctx, dir, "write-tree"); err == nil {
			if _, err = gitWorkspaceTree(ctx, dir, index); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	head, _ := gitWorkspaceRun(ctx, dir, "rev-parse", "--verify", "HEAD")
	published := st.Tip
	if branch != dest.Branch || op == "pull" || op == "push" {
		published, _ = gitWorkspaceRun(ctx, dir, "rev-parse", "--verify", "refs/remotes/origin/"+branch)
	}
	pushBase := published
	if op == "push" {
		if head == "" {
			return nil, kbErr("NO_CHANGES", "Commit knowledge before pushing.")
		}
		if published != "" {
			if _, err = gitWorkspaceRun(ctx, dir, "merge-base", "--is-ancestor", published, head); err != nil {
				return nil, kbErr("BACKUP_BRANCH_ADVANCED", "Pull or reconcile diverged history before pushing.")
			}
		}
		if _, err = gitWorkspaceTree(ctx, dir, head); err != nil {
			return nil, err
		}

		published = head
		if _, err = gitWorkspaceRun(ctx, dir, "update-ref", "refs/remotes/origin/"+branch, head); err != nil {
			return nil, err
		}
		gitWorkspaceRun(ctx, dir, "branch", "--set-upstream-to=origin/"+branch, branch)
	}
	if published != "" && (published != st.Tip || branch != dest.Branch) {
		if _, err = s.git(ctx, nil, nil, "fetch", "--no-tags", dir, published); err != nil {
			return nil, err
		}
		tree, e := gitWorkspaceTree(ctx, dir, published)
		if e != nil {
			return nil, e
		}
		st.Paths = map[string]publishedPath{}
		for name, b := range tree {
			st.Paths[name] = publishedPath{Fingerprint: digest(b)}
		}
		// Bind published paths to the entries that survive this import.
		for _, r := range regs {
			for _, e := range r.Entries {
				if v, ok := st.Paths[e.Path]; ok {
					v.EntryID = e.ID
					v.Sequence = e.ContentSequence
					st.Paths[e.Path] = v
				}
			}
		}
		st.Tip = published
		st.Initialized = true
		st.LastError = ""
		st.ExternalChange = false
		st.ObservedTip = ""
		if op == "push" || op == "pull" {
			st.LastSuccessfulAt = stamp()
		}
		changes = append(changes, jsonChange(filepath.Join(s.private, "backup-state.json"), st))
	} else if branch != dest.Branch {
		st = backupState{Paths: map[string]publishedPath{}}
		changes = append(changes, jsonChange(filepath.Join(s.private, "backup-state.json"), st))
	}
	if branch != dest.Branch || op == "pull" || op == "push" || op == "commit" {
		for _, receipt := range receipts {
			if receipt.State == "PREPARED" {
				receipt.State = "STALE"
				receipt.LastError = "Repository history changed; prepare a new receipt."
				changes = append(changes, receiptChange(s, receipt))
			}
		}
	}
	next := gitWorkspace{Directory: filepath.Base(dir), Branch: branch, PublishedTip: published}
	changes = append(changes, jsonChange(s.gitWorkspacePath(), next))
	if write {
		changes = append(changes, jsonChange(reqPath, requestRecord{Hash: hash, At: stamp(), Result: result}))
	}
	if err = authorize(); err != nil {
		return nil, err
	}
	if err = syncGitDirectory(dir); err != nil {
		return nil, err
	}
	// Retain a fully synced candidate once a journal may reference it, even if applying that journal fails.
	keep = true
	if op == "push" {
		intent := gitPushIntent{Owner: p.IdentityID, Branch: branch, Head: head, Base: pushBase, Directory: filepath.Base(dir), Previous: previous.Directory, RequestPath: reqPath, Hash: hash, Changes: changes, Result: result}
		if err = s.transact([]fileChange{jsonChange(s.gitPushIntentPath(), intent)}); err != nil {
			return nil, err
		}
		return s.finishGitPush(ctx, intent, authorize)
	}
	if err = s.transact(changes); err != nil {
		return nil, err
	}
	if previous.Directory != "" {
		os.RemoveAll(filepath.Join(s.private, previous.Directory))
	}
	return result, nil
}

func gitWorkspaceRun(ctx context.Context, dir string, args ...string) (string, error) {
	raw, err := gitWorkspaceBytes(ctx, dir, args...)
	return strings.TrimSuffix(string(raw), "\n"), err
}
func gitWorkspaceBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	flags := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.attributesFile=/dev/null", "-c", "core.excludesFile=/dev/null", "-c", "commit.gpgSign=false", "-c", "push.gpgSign=false", "-c", "gc.auto=0", "-c", "protocol.allow=never", "-c", "protocol.ssh.allow=always", "-c", "protocol.file.allow=always", "-c", "remote.origin.mirror=false", "--git-dir=" + filepath.Join(dir, ".git"), "--work-tree=" + dir}
	cmd := exec.CommandContext(ctx, "git", append(flags, args...)...)
	cmd.Dir = dir
	cmd.Env = append(gitEnvironment(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Knowledge Base", "GIT_AUTHOR_EMAIL=knowledgebase@localhost", "GIT_COMMITTER_NAME=Knowledge Base", "GIT_COMMITTER_EMAIL=knowledgebase@localhost")
	out := gitOutputBuffer{limit: 16 << 20}
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, kbErr("GIT_FAILED", "Git could not complete this action. Check for diverged history, repository credentials, or an unavailable branch.")
	}
	return out.Bytes(), nil
}
func copyGitDirectory(src, dst string) error {
	var total int64
	return filepath.WalkDir(src, func(name string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if de.Type()&os.ModeSymlink != 0 {
			return kbErr("STORAGE_UNAVAILABLE", "Unexpected link in Git state.")
		}
		if de.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return kbErr("STORAGE_UNAVAILABLE", "Unexpected Git file.")
		}
		total += info.Size()
		if total > 512<<20 {
			return kbErr("LIMIT_EXCEEDED", "Git workspace exceeds the MVP size limit.")
		}
		srcFile, err := os.Open(name)
		if err != nil {
			return err
		}
		defer srcFile.Close()
		dstFile, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(dstFile, srcFile)
		closeErr := dstFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
func syncGitDirectory(dir string) error {
	return filepath.WalkDir(dir, func(name string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			return syncDir(name)
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Sync()
	})
}
func gitWorkspaceTree(ctx context.Context, dir, ref string) (map[string][]byte, error) {
	out, err := gitWorkspaceRun(ctx, dir, "ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	total := 0
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 2)
		fields := strings.Fields(parts[0])
		if len(parts) != 2 || len(fields) != 3 || fields[0] != "100644" || fields[1] != "blob" {
			return nil, badArg("Git trees may contain only regular Markdown files.")
		}
		if err = validatePath(parts[1], true); err != nil {
			return nil, err
		}
		size, err := gitWorkspaceRun(ctx, dir, "cat-file", "-s", fields[2])
		if err != nil {
			return nil, err
		}
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil || n < 0 || n > 10<<20 || int64(total)+n > 50<<20 || len(files) >= 5000 {
			return nil, kbErr("LIMIT_EXCEEDED", "Git tree exceeds the MVP import limit.")
		}
		raw, e := gitWorkspaceBytes(ctx, dir, "cat-file", "blob", fields[2])
		if e != nil {
			return nil, e
		}
		text, e := normalizeText(string(raw), 10<<20, "Git content")
		if e != nil {
			return nil, e
		}
		files[parts[1]] = []byte(text)
		total += len(text)
		if len(files) > 5000 || total > 50<<20 {
			return nil, kbErr("LIMIT_EXCEEDED", "Git tree exceeds the MVP import limit.")
		}
	}
	if err := validateGitPaths(files); err != nil {
		return nil, err
	}
	return files, nil
}
func gitWorkspaceFiles(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	total := 0
	err := filepath.WalkDir(dir, func(name string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == filepath.Join(dir, ".git") {
			return filepath.SkipDir
		}
		if name == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, name)
		rel = filepath.ToSlash(rel)
		if de.Type()&os.ModeSymlink != 0 {
			return badArg("Git knowledge must not contain links.")
		}
		if de.IsDir() {
			return validatePath(rel, false)
		}
		if !de.Type().IsRegular() {
			return badArg("Git knowledge must contain regular Markdown files.")
		}
		if err = validatePath(rel, true); err != nil {
			return err
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		if info.Size() > 10<<20 {
			return kbErr("LIMIT_EXCEEDED", "Git content exceeds its byte limit.")
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		text, err := normalizeText(string(b), 10<<20, "Git content")
		if err != nil {
			return err
		}
		files[rel] = []byte(text)
		total += len(text)
		if len(files) > 5000 || total > 50<<20 {
			return kbErr("LIMIT_EXCEEDED", "Git tree exceeds the MVP import limit.")
		}
		return nil
	})
	if err == nil {
		err = validateGitPaths(files)
	}
	return files, err
}
func (s *Service) gitImportChanges(p Principal, regs []folderRegistry, files map[string][]byte) ([]fileChange, error) {
	folders := map[string]*folderRegistry{}
	names := map[string]string{}
	for i := range regs {
		r := &regs[i]
		folders[r.Path] = r
		names[strings.ToLower(r.Path)] = r.Path
	}
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		folder := path.Dir(name)
		if folder == "." {
			folder = ""
		}
		parts := strings.Split(folder, "/")
		parent := ""
		for _, part := range parts {
			if part == "" {
				continue
			}
			parent = childPath(parent, part)
			key := strings.ToLower(parent)
			if existing, ok := names[key]; ok && existing != parent {
				return nil, badArg("Git paths collide with existing folder names.")
			}
			if _, ok := folders[parent]; !ok {
				r := folderRegistry{ID: "folder_" + uuid.NewString(), Path: parent, Entries: []Entry{}, Deletions: []Deletion{}}
				folders[parent] = &r
				names[key] = parent
			}
		}
		key := strings.ToLower(name)
		if existing, ok := names[key]; ok {
			return nil, badArg("Git file/folder paths collide: %s.", existing)
		}
		names[key] = name
	}
	changes := []fileChange{}
	now := stamp()
	for folder, r := range folders {
		existing := map[string]Entry{}
		entries := []Entry{}
		for _, e := range r.Entries {
			existing[e.Path] = e
			if _, ok := files[e.Path]; !ok {
				r.Deletions = append(r.Deletions, Deletion{ID: "deletion_" + uuid.NewString(), EntryID: e.ID, FolderID: r.ID, FolderPath: r.Path, Path: e.Path, Sequence: e.Sequence + 1, Actor: p.IdentityID, CreatedAt: now})
				changes = append(changes, fileChange{Path: filepath.Join(s.live, filepath.FromSlash(e.Path)), Delete: true})
			}
		}
		for _, name := range paths {
			parent := path.Dir(name)
			if parent == "." {
				parent = ""
			}
			if parent != folder {
				continue
			}
			b := files[name]
			e, ok := existing[name]
			if !ok {
				e = Entry{ID: "entry_" + uuid.NewString(), FolderID: r.ID, FolderPath: folder, Path: name, Filename: path.Base(name), Type: "note", Title: strings.TrimSuffix(path.Base(name), ".md"), Tags: []string{}, Sequence: 1, ContentSequence: 1, CreatedAt: now, CreatedBy: p.IdentityID}
			}
			changed := !ok || e.Fingerprint != digest(b)
			if changed {
				if ok {
					e.Sequence++
					e.ContentSequence++
				}
				e.Fingerprint = digest(b)
				e.UpdatedAt = now
				e.UpdatedBy = p.IdentityID
				e.Version = entryVersion(e)
				changes = append(changes, fileChange{Path: filepath.Join(s.live, filepath.FromSlash(name)), Data: b})
			}
			entries = append(entries, e)
		}
		r.Entries = entries
		changes = append(changes, jsonChange(s.registryPath(folder), r))
	}
	return changes, nil
}

func validateGitPaths(files map[string][]byte) error {
	names := map[string]string{}
	for name := range files {
		parts := strings.Split(name, "/")
		for i := range parts {
			current := strings.Join(parts[:i+1], "/")
			key := strings.ToLower(current)
			if other, ok := names[key]; ok && other != current {
				return badArg("Git paths collide by case.")
			}
			names[key] = current
			if i < len(parts)-1 {
				if _, exists := files[current]; exists {
					return badArg("Git file/folder paths collide.")
				}
			}
		}
	}
	return nil
}

type gitPushIntent struct {
	Owner       string       `json:"owner"`
	Branch      string       `json:"branch"`
	Head        string       `json:"head"`
	Base        string       `json:"base"`
	Directory   string       `json:"directory"`
	Previous    string       `json:"previous"`
	RequestPath string       `json:"request_path"`
	Hash        string       `json:"hash"`
	Changes     []fileChange `json:"changes"`
	Result      any          `json:"result"`
}

func (s *Service) gitPushIntentPath() string { return filepath.Join(s.private, "git-push-intent.json") }
func (s *Service) readGitPushIntent() (*gitPushIntent, error) {
	b, err := os.ReadFile(s.gitPushIntentPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var intent gitPushIntent
	if json.Unmarshal(b, &intent) != nil || !strings.HasPrefix(intent.Directory, "git-generation-") || filepath.Base(intent.Directory) != intent.Directory {
		return nil, kbErr("STORAGE_UNAVAILABLE", "Invalid pending Git push.")
	}
	return &intent, nil
}
func (s *Service) finishGitPush(ctx context.Context, intent gitPushIntent, authorize func() error) (any, error) {
	if err := authorize(); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.private, intent.Directory)
	observe := func() (string, error) {
		out, err := gitWorkspaceRun(ctx, dir, "ls-remote", "--heads", "origin", "refs/heads/"+intent.Branch)
		if err != nil {
			return "", err
		}
		fields := strings.Fields(out)
		if len(fields) == 0 {
			return "", nil
		}
		if len(fields) != 2 {
			return "", kbErr("GIT_FAILED", "Cannot inspect the remote branch.")
		}
		return fields[0], nil
	}
	tip, err := observe()
	if err != nil {
		return nil, retryErr("BACKUP_OUTCOME_UNKNOWN", "Retry this Git push to reconcile its delivery.", 3)
	}
	if tip == intent.Base && tip != intent.Head {
		if err := authorize(); err != nil {
			return nil, err
		}
		_, pushErr := gitWorkspaceRun(ctx, dir, "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--force-with-lease=refs/heads/"+intent.Branch+":"+intent.Base, "origin", intent.Head+":refs/heads/"+intent.Branch)
		tip, err = observe()
		if err != nil {
			return nil, retryErr("BACKUP_OUTCOME_UNKNOWN", "Retry this Git push to reconcile its delivery.", 3)
		}
		if tip == intent.Base && tip != intent.Head {
			s.transact([]fileChange{{Path: s.gitPushIntentPath(), Delete: true}})
			os.RemoveAll(dir)
			if pushErr != nil {
				return nil, pushErr
			}
			return nil, kbErr("GIT_FAILED", "The remote did not confirm this push.")
		}
	}
	if tip != intent.Head {
		s.transact([]fileChange{{Path: s.gitPushIntentPath(), Delete: true}})
		os.RemoveAll(dir)
		return nil, kbErr("BACKUP_BRANCH_ADVANCED", "The remote branch changed; pull before pushing.")
	}
	// Publication succeeded. Record delivery even if the connection was revoked
	// during transport; no further remote or live content changes are performed.
	changes := append(intent.Changes, fileChange{Path: s.gitPushIntentPath(), Delete: true})
	if err = s.transact(changes); err != nil {
		return nil, err
	}
	if intent.Previous != "" {
		os.RemoveAll(filepath.Join(s.private, intent.Previous))
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	return intent.Result, nil
}

func (s *Service) cleanupGitGenerations() error {
	state, err := s.readGitWorkspace()
	if err != nil {
		return err
	}
	intent, err := s.readGitPushIntent()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(s.private)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "git-generation-") || entry.Name() == state.Directory {
			continue
		}
		if intent != nil && (entry.Name() == intent.Directory || entry.Name() == intent.Previous) {
			continue
		}
		if err = os.RemoveAll(filepath.Join(s.private, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

type gitOutputBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *gitOutputBuffer) Bytes() []byte { return b.buffer.Bytes() }
func (b *gitOutputBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.limit {
		return 0, errors.New("Git output exceeds its limit")
	}
	return b.buffer.Write(p)
}
