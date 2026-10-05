package knowledgebase

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type Snapshot struct {
	EntryID     string `json:"entry_id,omitempty"`
	DeletionID  string `json:"deletion_id,omitempty"`
	Path        string `json:"path"`
	FolderPath  string `json:"folder_path"`
	Version     string `json:"version,omitempty"`
	Sequence    uint64 `json:"content_sequence"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Content     []byte `json:"-"`
}
type Receipt struct {
	Branch         string     `json:"branch,omitempty"`
	ID             string     `json:"receipt_id"`
	IdentityID     string     `json:"-"`
	OrganizationID string     `json:"-"`
	Base           string     `json:"base_commit_id"`
	Commit         string     `json:"prepared_commit_id"`
	Selections     []Snapshot `json:"selections"`
	CreatedAt      string     `json:"created_at"`
	ExpiresAt      string     `json:"expires_at"`
	State          string     `json:"state"`
	PushedAt       string     `json:"pushed_at,omitempty"`
	LastError      string     `json:"last_backup_error,omitempty"`
}

// Receipt's private owner fields are serialized only through receiptDisk.
type receiptDisk struct {
	Receipt
	Owner        string `json:"owner"`
	Organization string `json:"organization"`
}
type publishedPath struct {
	EntryID     string `json:"entry_id"`
	Sequence    uint64 `json:"sequence"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
}
type backupState struct {
	Tip              string                   `json:"tip"`
	Initialized      bool                     `json:"initialized"`
	Paths            map[string]publishedPath `json:"paths"`
	LastSuccessfulAt string                   `json:"last_successful_backup_at,omitempty"`
	LastError        string                   `json:"last_backup_error,omitempty"`
	ExternalChange   bool                     `json:"external_change,omitempty"`
	ObservedTip      string                   `json:"observed_tip,omitempty"`
}

func (s *Service) repo() string { return filepath.Join(s.private, "backup.git") }
func (s *Service) receiptPath(id string) string {
	return filepath.Join(s.private, "receipts", id+".json")
}
func receiptChange(s *Service, r Receipt) fileChange {
	return jsonChange(s.receiptPath(r.ID), receiptDisk{Receipt: r, Owner: r.IdentityID, Organization: r.OrganizationID})
}
func (s *Service) readReceipt(id string) (Receipt, error) {
	var d receiptDisk
	if !strings.HasPrefix(id, "receipt_") || strings.ContainsAny(id, "/\\.") {
		return Receipt{}, kbErr("NOT_FOUND", "Resource not found.")
	}
	b, err := os.ReadFile(s.receiptPath(id))
	if os.IsNotExist(err) {
		return Receipt{}, kbErr("NOT_FOUND", "Resource not found.")
	}
	if err != nil {
		return Receipt{}, err
	}
	if err = json.Unmarshal(b, &d); err != nil {
		return Receipt{}, err
	}
	d.Receipt.IdentityID = d.Owner
	d.Receipt.OrganizationID = d.Organization
	return d.Receipt, nil
}
func (s *Service) receipts() ([]Receipt, error) {
	des, err := os.ReadDir(filepath.Join(s.private, "receipts"))
	if err != nil {
		return nil, err
	}
	out := []Receipt{}
	for _, de := range des {
		if !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		r, err := s.readReceipt(strings.TrimSuffix(de.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Service) backupState() backupState {
	var st backupState
	b, err := os.ReadFile(filepath.Join(s.private, "backup-state.json"))
	if err == nil {
		json.Unmarshal(b, &st)
	}
	if st.Paths == nil {
		st.Paths = map[string]publishedPath{}
	}
	return st
}
func (s *Service) publicationLock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.private, "publication.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
			return nil, retryErr("BACKUP_BUSY", "Another publication is in progress.", 3)
		}
		return nil, err
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func (s *Service) git(ctx context.Context, stdin []byte, extraEnv []string, args ...string) (string, error) {
	args = append(s.gitArgs(), args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(gitEnvironment(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Knowledge Base", "GIT_AUTHOR_EMAIL=knowledgebase@localhost", "GIT_COMMITTER_NAME=Knowledge Base", "GIT_COMMITTER_EMAIL=knowledgebase@localhost")
	cmd.Env = append(cmd.Env, extraEnv...)
	if stdin != nil {
		cmd.Stdin = strings.NewReader(string(stdin))
	}
	out, err := cmd.Output()
	if err != nil {
		return "", retryErr("BACKUP_UNAVAILABLE", "Git backup is unavailable; check the backend repository configuration.", 3)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}
func (s *Service) ensureRepo(ctx context.Context) error {
	destination, err := s.configuredBackupDestination()
	if err != nil {
		return err
	}
	if destination.Remote == "" {
		return kbErr("BACKUP_NOT_CONFIGURED", "An administrator must configure a Git backup repository.")
	}
	if _, err := s.git(ctx, nil, nil, "check-ref-format", "refs/heads/"+destination.Branch); err != nil {
		return kbErr("BACKUP_NOT_CONFIGURED", "The configured backup branch is invalid.")
	}
	if _, err := os.Stat(filepath.Join(s.repo(), "HEAD")); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "init", "--bare", "--template=", "--", s.repo())
		cmd.Env = gitEnvironment()
		if err = cmd.Run(); err != nil {
			return retryErr("BACKUP_UNAVAILABLE", "Git backup initialization failed.", 3)
		}
		os.Chmod(s.repo(), 0700)
		if _, err = s.git(ctx, nil, nil, "remote", "add", "origin", destination.Remote); err != nil {
			return err
		}
	}
	fetchURL, err := s.git(ctx, nil, nil, "remote", "get-url", "--all", "origin")
	if err != nil {
		return err
	}
	pushURL, err := s.git(ctx, nil, nil, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	if fetchURL != destination.Remote || pushURL != destination.Remote {
		return kbErr("BACKUP_REMOTE_CHANGED", "The staging repository destination differs from its configured backup.")
	}
	cfgPath := filepath.Join(s.private, "backup-configuration.json")
	expected := map[string]string{"remote": destination.Remote, "branch": destination.Branch}
	if b, e := os.ReadFile(cfgPath); e == nil {
		var actual map[string]string
		if json.Unmarshal(b, &actual) != nil || actual["remote"] != expected["remote"] || actual["branch"] != expected["branch"] {
			return kbErr("BACKUP_REMOTE_CHANGED", "The backup configuration changed; administrator reconciliation is required.")
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if e = atomicJSON(cfgPath, expected); e != nil {
		return e
	}
	return nil
}
func (s *Service) remoteTip(ctx context.Context) (string, error) {
	destination, err := s.backupDestination()
	if err != nil {
		return "", err
	}
	out, err := s.git(ctx, nil, nil, "ls-remote", "--heads", "origin", "refs/heads/"+destination.Branch)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", nil
	}
	parts := strings.Fields(out)
	if len(parts) != 2 || len(parts[0]) < 40 {
		return "", retryErr("BACKUP_UNAVAILABLE", "The remote branch could not be inspected.", 3)
	}
	if _, err = s.git(ctx, nil, nil, "fetch", "--no-tags", "origin", "+refs/heads/"+destination.Branch+":refs/remotes/origin/"+destination.Branch); err != nil {
		return "", err
	}
	tip, err := s.git(ctx, nil, nil, "rev-parse", "refs/remotes/origin/"+destination.Branch)
	return tip, err
}
func (s *Service) treeFingerprint(ctx context.Context, tip, p string) (string, bool, error) {
	if tip == "" {
		return "", false, nil
	}
	out, err := s.git(ctx, nil, nil, "ls-tree", "-z", tip, "--", p)
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return "", false, nil
	}
	parts := strings.Fields(strings.SplitN(out, "\t", 2)[0])
	if len(parts) != 3 || parts[0] != "100644" || parts[1] != "blob" {
		return "", false, kbErr("BACKUP_REMOTE_CHANGED", "The backup path is not a regular Markdown file.")
	}
	b, err := s.gitBytes(ctx, "cat-file", "blob", parts[2])
	if err != nil {
		return "", false, err
	}
	return digest(b), true, nil
}
func (s *Service) gitBytes(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append(s.gitArgs(), args...)...)
	cmd.Env = gitEnvironment()
	b, e := cmd.Output()
	if e != nil {
		return nil, kbErr("BACKUP_UNAVAILABLE", "Git backup state is unavailable.")
	}
	return b, nil
}
func (s *Service) authorizeSelections(p Principal, r Receipt) error {
	if r.IdentityID != p.IdentityID || r.OrganizationID != s.cfg.OrganizationID {
		return kbErr("NOT_FOUND", "Resource not found.")
	}
	destination, err := s.backupDestination()
	if err != nil {
		return err
	}
	configured, err := s.configuredBackupDestination()
	if err != nil {
		return err
	}
	branch := r.Branch
	if branch == "" {
		branch = configured.Branch
	}
	if branch != destination.Branch || r.State == "STALE" {
		return kbErr("BACKUP_BRANCH_ADVANCED", "Repository history changed; prepare a new receipt.")
	}
	for _, sel := range r.Selections {
		if err := s.require(p, sel.FolderPath, roleEditor); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) validateGenerations(ctx context.Context, p Principal, r Receipt) error {
	if p.Recheck != nil {
		if e := p.Recheck(ctx); e != nil {
			return e
		}
	}
	if !s.IdentityActive(ctx, p.IdentityID) {
		return kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
	}
	if err := s.authorizeSelections(p, r); err != nil {
		return err
	}
	rs, err := s.registries()
	if err != nil {
		return err
	}
	st := s.backupState()
	for _, sel := range r.Selections {
		found := false
		for _, f := range rs {
			if f.Path != sel.FolderPath {
				continue
			}
			if sel.EntryID != "" {
				for _, e := range f.Entries {
					if e.ID == sel.EntryID && e.Path == sel.Path {
						found = true
					}
				}
			} else {
				for _, d := range f.Deletions {
					if d.ID == sel.DeletionID && d.Path == sel.Path {
						found = true
					}
				}
				for _, e := range f.Entries {
					if strings.EqualFold(e.Path, sel.Path) {
						found = false
						break
					}
				}
			}
		}
		if !found {
			return kbErr("BACKUP_VERSION_CONFLICT", "A selected entry was deleted or replaced.")
		}
		if published, ok := st.Paths[sel.Path]; ok && published.EntryID == sel.EntryID && sel.EntryID != "" && published.Sequence > sel.Sequence {
			return kbErr("BACKUP_VERSION_CONFLICT", "The snapshot would regress a newer published version.")
		}
	}
	return nil
}
func (s *Service) collect(p Principal, a map[string]any) ([]Snapshot, error) {
	sels := []Snapshot{}
	seen := map[string]bool{}
	if entries, ok := a["entries"]; ok {
		xs, e := objects(entries)
		if e != nil {
			return nil, e
		}
		for _, loc := range xs {
			e, _, err := s.resolveEntry(p, loc, roleEditor)
			if err != nil {
				return nil, err
			}
			if stringArg(loc, "expected_version") != e.Version {
				return nil, versionConflict(e)
			}
			if seen[e.Path] {
				return nil, badArg("Selections must not duplicate a path.")
			}
			seen[e.Path] = true
			b, err := s.entryContent(e)
			if err != nil {
				return nil, err
			}
			sels = append(sels, Snapshot{EntryID: e.ID, Path: e.Path, FolderPath: e.FolderPath, Version: e.Version, Sequence: e.ContentSequence, Fingerprint: e.Fingerprint, Content: b})
		}
	}
	if deletions, ok := a["deletions"]; ok {
		xs, e := objects(deletions)
		if e != nil {
			return nil, e
		}
		rs, err := s.registries()
		if err != nil {
			return nil, err
		}
		for _, loc := range xs {
			id := stringArg(loc, "deletion_id")
			found := false
			for _, r := range rs {
				for _, d := range r.Deletions {
					if d.ID == id {
						if err = s.require(p, d.FolderPath, roleEditor); err != nil {
							return nil, err
						}
						for _, live := range r.Entries {
							if strings.EqualFold(live.Path, d.Path) {
								return nil, kbErr("BACKUP_VERSION_CONFLICT", "The deletion path has been reused.")
							}
						}
						if seen[d.Path] {
							return nil, badArg("Selections must not duplicate a path.")
						}
						seen[d.Path] = true
						sels = append(sels, Snapshot{DeletionID: d.ID, Path: d.Path, FolderPath: d.FolderPath, Sequence: d.Sequence})
						found = true
						break
					}
				}
			}
			if !found {
				return nil, kbErr("NOT_FOUND", "Resource not found.")
			}
		}
	}
	if len(sels) == 0 {
		return nil, badArg("Select at least one entry or deletion.")
	}
	if len(sels) > 100 {
		return nil, kbErr("LIMIT_EXCEEDED", "A backup may select at most 100 paths.")
	}
	return sels, nil
}
func objects(v any) ([]map[string]any, error) {
	switch xs := v.(type) {
	case []map[string]any:
		return xs, nil
	case []any:
		out := []map[string]any{}
		for _, x := range xs {
			m, ok := x.(map[string]any)
			if !ok {
				return nil, badArg("Selections must be arrays of objects.")
			}
			out = append(out, m)
		}
		return out, nil
	default:
		return nil, badArg("Selections must be arrays of objects.")
	}
}
func (s *Service) backupCall(ctx context.Context, p Principal, tool string, a map[string]any) (result any, resultErr error) {
	reqPath, hash, err := s.requestPath(p, tool, a)
	if err != nil {
		return nil, err
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
	if err = s.recover(); err != nil {
		unlock()
		return nil, err
	}
	if intent, e := s.readGitPushIntent(); e != nil || intent != nil {
		unlock()
		if e != nil {
			return nil, e
		}
		return nil, kbErr("BACKUP_OUTCOME_UNKNOWN", "Reconcile the pending Files Git push first.")
	}
	if tool == "commit_knowledgebase" {
		workspace, e := s.readGitWorkspace()
		if e != nil {
			unlock()
			return nil, e
		}
		if workspace.Directory != "" {
			head, _ := gitWorkspaceRun(ctx, filepath.Join(s.private, workspace.Directory), "rev-parse", "--verify", "HEAD")
			if head != "" && head != workspace.PublishedTip {
				unlock()
				return nil, kbErr("GIT_LOCAL_COMMITS", "Push the pending Files Git commits before preparing a receipt.")
			}
		}
	}
	if b, e := os.ReadFile(reqPath); e == nil {
		var rec requestRecord
		if json.Unmarshal(b, &rec) == nil {
			at, _ := time.Parse(time.RFC3339Nano, rec.At)
			if time.Since(at) < 7*24*time.Hour {
				if rec.Hash != hash {
					unlock()
					return nil, kbErr("REQUEST_ID_REUSE", "This request ID was used with different arguments.")
				}
				m := asMap(rec.Result)
				id := stringArg(m, "receipt_id")
				if id != "" {
					r, e := s.readReceipt(id)
					if e == nil {
						e = s.authorizeSelections(p, r)
					}
					if e != nil {
						unlock()
						return nil, e
					}
				} else if _, e = s.collect(p, a); e != nil && tool == "commit_knowledgebase" {
					unlock()
					return nil, e
				}
				unlock()
				return rec.Result, nil
			}
		}
	}
	var sels []Snapshot
	var receipt Receipt
	if tool == "commit_knowledgebase" {
		message := stringArg(a, "message")
		if strings.TrimSpace(message) == "" || len([]rune(message)) > 2000 {
			unlock()
			return nil, badArg("message must contain 1–2000 characters.")
		}
		sels, err = s.collect(p, a)
	} else {
		receipt, err = s.readReceipt(stringArg(a, "receipt_id"))
		if err == nil {
			err = s.authorizeSelections(p, receipt)
		}
		if err == nil && receipt.State == "PUSHED" {
			result := asMap(receipt)
			err = s.transact([]fileChange{jsonChange(reqPath, requestRecord{Hash: hash, At: stamp(), Result: result})})
			unlock()
			return result, err
		}
		if err == nil {
			expires, _ := time.Parse(time.RFC3339Nano, receipt.ExpiresAt)
			if receipt.State != "PUSH_UNKNOWN" && time.Now().After(expires) {
				receipt.State = "EXPIRED"
				s.transact([]fileChange{receiptChange(s, receipt)})
				err = kbErr("SNAPSHOT_EXPIRED", "The prepared snapshot expired; prepare a new commit.")
			}
			if receipt.State == "STALE" {
				err = kbErr("BACKUP_BRANCH_ADVANCED", "The backup branch advanced; prepare a new commit.")
			}
		}
	}
	unlock()
	if err != nil {
		return nil, err
	}
	// Record backend failures after inner content locks have been released, while
	// still holding the publication lock. Argument and authorization failures do
	// not become organization-wide backup errors.
	defer func() {
		if err := s.recordBackupError(resultErr); err != nil {
			resultErr = kbErr("STORAGE_UNAVAILABLE", "Backup error status could not be saved.")
		}
	}()
	netctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = s.ensureRepo(netctx); err != nil {
		return nil, err
	}
	tip, err := s.remoteTip(netctx)
	if err != nil {
		return nil, err
	}
	unlock, err = s.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	if err = s.reconcile(netctx, tip); err != nil {
		unlock()
		return nil, err
	}
	st := s.backupState()
	if st.ExternalChange {
		unlock()
		return nil, kbErr("BACKUP_REMOTE_CHANGED", "The repository changed outside Knowledge Base; administrator reconciliation is required.")
	}
	unlock()
	if tool == "commit_knowledgebase" {
		return s.prepare(netctx, p, a, sels, tip, reqPath, hash)
	}
	return s.push(netctx, p, a, receipt, tip, reqPath, hash)
}

func (s *Service) recordBackupError(err error) error {
	failure, ok := err.(*Error)
	if !ok {
		return nil
	}
	switch failure.Code {
	case "BACKUP_UNAVAILABLE", "BACKUP_OUTCOME_UNKNOWN", "BACKUP_REMOTE_CHANGED", "BACKUP_BRANCH_ADVANCED":
	default:
		return nil
	}
	unlock, lockErr := s.lock(context.Background(), false)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	if recoverErr := s.recover(); recoverErr != nil {
		return recoverErr
	}
	st := s.backupState()
	st.LastError = failure.Message
	return s.transact([]fileChange{jsonChange(filepath.Join(s.private, "backup-state.json"), st)})
}
func (s *Service) reconcile(ctx context.Context, tip string) error {
	st := s.backupState()
	rs, err := s.receipts()
	if err != nil {
		return err
	}
	if !st.Initialized && tip != "" {
		return kbErr("BACKUP_REMOTE_CHANGED", "The backup branch already contains content; an administrator must explicitly reconcile it before use.")
	}
	knownTip := tip == st.Tip
	changes := []fileChange{}
	for _, r := range rs {
		if r.Commit == tip && r.State == "PUSH_UNKNOWN" {
			knownTip = true
		}
		if r.State != "PUSH_UNKNOWN" {
			continue
		}
		reached := r.Commit == tip
		if !reached && tip != "" {
			var e error
			reached, e = s.isAncestor(ctx, r.Commit, tip)
			if e != nil {
				return e
			}
		}
		if reached {
			r.State = "PUSHED"
			r.PushedAt = stamp()
			r.LastError = ""
			for _, sel := range r.Selections {
				st.Paths[sel.Path] = publishedPath{EntryID: sel.EntryID, Sequence: sel.Sequence, Fingerprint: sel.Fingerprint, Deleted: sel.DeletionID != ""}
			}
			st.LastSuccessfulAt = r.PushedAt
			st.LastError = ""
			changes = append(changes, receiptChange(s, r))
		} else if tip == r.Base {
			r.State = "PREPARED"
			changes = append(changes, receiptChange(s, r))
		} else {
			r.State = "STALE"
			changes = append(changes, receiptChange(s, r))
		}
	}
	if st.Initialized && tip != st.Tip && !knownTip {
		st.ExternalChange = true
	}
	st.Initialized = true
	if st.ExternalChange {
		st.ObservedTip = tip
	} else {
		st.Tip = tip
		st.ObservedTip = ""
	}
	changes = append(changes, jsonChange(filepath.Join(s.private, "backup-state.json"), st))
	return s.transact(changes)
}
func (s *Service) prepare(ctx context.Context, p Principal, a map[string]any, sels []Snapshot, base, reqPath, hash string) (any, error) {
	destination, err := s.backupDestination()
	if err != nil {
		return nil, err
	}
	idx := filepath.Join(s.private, "index-"+uuid.NewString())
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}
	if base == "" {
		if _, err := s.git(ctx, nil, env, "read-tree", "--empty"); err != nil {
			return nil, err
		}
	} else if _, err := s.git(ctx, nil, env, "read-tree", base); err != nil {
		return nil, err
	}
	changed := false
	for _, sel := range sels {
		fp, exists, inspectErr := s.treeFingerprint(ctx, base, sel.Path)
		if inspectErr != nil {
			return nil, inspectErr
		}
		if sel.DeletionID != "" {
			if exists {
				changed = true
			}
			if _, err := s.git(ctx, []byte("0 0000000000000000000000000000000000000000\t"+sel.Path+"\n"), env, "update-index", "--index-info"); err != nil {
				return nil, err
			}
		} else {
			if !exists || fp != sel.Fingerprint {
				changed = true
			}
			blob, err := s.git(ctx, sel.Content, env, "hash-object", "-w", "--stdin")
			if err != nil {
				return nil, err
			}
			if _, err = s.git(ctx, nil, env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+sel.Path); err != nil {
				return nil, err
			}
		}
	}
	if !changed {
		result := map[string]any{"status": "NO_CHANGES", "changed": false, "base_commit_id": base}
		unlock, err := s.lock(ctx, false)
		if err != nil {
			return nil, err
		}
		defer unlock()
		st := s.backupState()
		st.LastError = ""
		check := Receipt{Branch: destination.Branch, IdentityID: p.IdentityID, OrganizationID: s.cfg.OrganizationID, Selections: sels}
		if err = s.validateGenerations(ctx, p, check); err != nil {
			return nil, err
		}
		if !s.IdentityActive(ctx, p.IdentityID) {
			return nil, kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
		}
		for _, sel := range sels {
			st.Paths[sel.Path] = publishedPath{EntryID: sel.EntryID, Sequence: sel.Sequence, Fingerprint: sel.Fingerprint, Deleted: sel.DeletionID != ""}
		}
		return result, s.transact([]fileChange{jsonChange(filepath.Join(s.private, "backup-state.json"), st), jsonChange(reqPath, requestRecord{Hash: hash, At: stamp(), Result: result})})
	}
	tree, err := s.git(ctx, nil, env, "write-tree")
	if err != nil {
		return nil, err
	}
	argv := []string{"commit-tree", tree}
	if base != "" {
		argv = append(argv, "-p", base)
	}
	commit, err := s.git(ctx, []byte(stringArg(a, "message")+"\n"), env, argv...)
	if err != nil {
		return nil, err
	}
	r := Receipt{Branch: destination.Branch, ID: "receipt_" + uuid.NewString(), IdentityID: p.IdentityID, OrganizationID: s.cfg.OrganizationID, Base: base, Commit: commit, Selections: sels, CreatedAt: stamp(), ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano), State: "PREPARED"}
	for i := range r.Selections {
		r.Selections[i].Content = nil
	}
	if _, err = s.git(ctx, nil, nil, "update-ref", "refs/knowledgebase/receipts/"+r.ID, commit); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = s.validateGenerations(ctx, p, r); err != nil {
		return nil, err
	}
	result := asMap(r)
	return result, s.transact([]fileChange{receiptChange(s, r), jsonChange(reqPath, requestRecord{Hash: hash, At: stamp(), Result: result})})
}
func (s *Service) push(ctx context.Context, p Principal, a map[string]any, r Receipt, tip, reqPath, hash string) (any, error) {
	destination, err := s.backupDestination()
	if err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	current, err := s.readReceipt(r.ID)
	if err != nil {
		unlock()
		return nil, err
	}
	r = current
	if r.State != "PUSHED" && r.State != "PUSH_UNKNOWN" {
		expires, _ := time.Parse(time.RFC3339Nano, r.ExpiresAt)
		if time.Now().After(expires) {
			r.State = "EXPIRED"
			s.transact([]fileChange{receiptChange(s, r)})
			unlock()
			return nil, kbErr("SNAPSHOT_EXPIRED", "The prepared snapshot expired; prepare a new commit.")
		}
	}
	if r.State == "PUSHED" {
		result := asMap(r)
		err = s.transact([]fileChange{jsonChange(reqPath, requestRecord{Hash: hash, At: stamp(), Result: result})})
		unlock()
		return result, err
	}
	if tip != r.Base {
		r.State = "STALE"
		s.transact([]fileChange{receiptChange(s, r)})
		unlock()
		return nil, kbErr("BACKUP_BRANCH_ADVANCED", "The backup branch advanced; prepare a new commit.")
	}
	if err = s.validateGenerations(ctx, p, r); err != nil {
		unlock()
		return nil, err
	}
	if !s.IdentityActive(ctx, p.IdentityID) {
		unlock()
		return nil, kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
	}
	parents, e := s.git(ctx, nil, nil, "rev-list", "--parents", "-n", "1", r.Commit)
	want := r.Commit
	if r.Base != "" {
		want += " " + r.Base
	}
	if e != nil || parents != want {
		unlock()
		return nil, kbErr("BACKUP_VERSION_CONFLICT", "The receipt is not a direct successor of its published base.")
	}
	r.State = "PUSH_UNKNOWN"
	if err = s.transact([]fileChange{receiptChange(s, r)}); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	if p.Recheck != nil {
		if e := p.Recheck(ctx); e != nil {
			u, le := s.lock(context.Background(), false)
			if le == nil {
				r.State = "PREPARED"
				s.transact([]fileChange{receiptChange(s, r)})
				u()
			}
			return nil, e
		}
	}
	_, pushErr := s.git(ctx, nil, nil, "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--force-with-lease=refs/heads/"+destination.Branch+":"+r.Base, "origin", r.Commit+":refs/heads/"+destination.Branch)
	// Observe delivery independently after transport failure. Timeout or process death leaves the durable unknown state.
	observed, observeErr := s.remoteTip(ctx)
	unlock, err = s.lock(context.Background(), false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if observeErr != nil {
		r.LastError = "Backup delivery must be reconciled on the next explicit push."
		s.transact([]fileChange{receiptChange(s, r)})
		return nil, retryErr("BACKUP_OUTCOME_UNKNOWN", "Push delivery is unknown; retry this receipt to reconcile.", 3)
	}
	reached := observed == r.Commit
	if !reached && observed != "" {
		var e error
		reached, e = s.isAncestor(ctx, r.Commit, observed)
		if e != nil {
			r.LastError = "Backup delivery must be reconciled on the next explicit push."
			s.transact([]fileChange{receiptChange(s, r)})
			return nil, e
		}
	}
	if reached {
		r.State = "PUSHED"
		r.PushedAt = stamp()
		r.LastError = ""
		st := s.backupState()
		st.Tip = observed
		st.ExternalChange = observed != r.Commit
		if st.ExternalChange {
			st.Tip = r.Commit
			st.ObservedTip = observed
		} else {
			st.ObservedTip = ""
		}
		st.Initialized = true
		st.LastSuccessfulAt = r.PushedAt
		st.LastError = ""
		for _, sel := range r.Selections {
			st.Paths[sel.Path] = publishedPath{EntryID: sel.EntryID, Sequence: sel.Sequence, Fingerprint: sel.Fingerprint, Deleted: sel.DeletionID != ""}
		}
		result := asMap(r)
		return result, s.transact([]fileChange{receiptChange(s, r), jsonChange(filepath.Join(s.private, "backup-state.json"), st), jsonChange(reqPath, requestRecord{Hash: hash, At: stamp(), Result: result})})
	}
	if observed == r.Base {
		r.State = "PREPARED"
		r.LastError = "Push failed before remote publication."
		s.transact([]fileChange{receiptChange(s, r)})
		if pushErr != nil {
			return nil, pushErr
		}
		return nil, retryErr("BACKUP_UNAVAILABLE", "The remote has not confirmed this push.", 3)
	}
	r.State = "STALE"
	st := s.backupState()
	st.ExternalChange = true
	st.ObservedTip = observed
	s.transact([]fileChange{receiptChange(s, r), jsonChange(filepath.Join(s.private, "backup-state.json"), st)})
	return nil, kbErr("BACKUP_BRANCH_ADVANCED", "The backup branch advanced; prepare a new commit.")
}

func (s *Service) deletionReusable(d Deletion) bool {
	destination, err := s.backupDestination()
	if err != nil {
		return false
	}
	st := s.backupState()
	// A live-only installation has no remote deletion to confirm. Never use
	// this exception after backup initialization or while receipts exist:
	// removing the remote must not release paths with unpublished snapshots.
	if destination.Remote == "" && !st.Initialized && !st.ExternalChange && st.Tip == "" && len(st.Paths) == 0 {
		// An unreadable/corrupt state is not evidence of a live-only store.
		b, err := os.ReadFile(filepath.Join(s.private, "backup-state.json"))
		if err != nil && !os.IsNotExist(err) || err == nil && json.Unmarshal(b, &st) != nil {
			return false
		}
		rs, err := s.receipts()
		return err == nil && len(rs) == 0
	}
	if !st.Initialized || st.ExternalChange {
		return false
	}
	_, exists, inspectErr := s.treeFingerprint(context.Background(), st.Tip, d.Path)
	if inspectErr != nil || exists {
		return false
	}
	rs, err := s.receipts()
	if err != nil {
		return false
	}
	for _, r := range rs {
		if r.State == "PUSH_UNKNOWN" {
			return false
		}
	}
	return true
}
func (s *Service) backupStatus(p Principal, a map[string]any) (any, error) {
	destination, err := s.backupDestination()
	if err != nil {
		return nil, err
	}
	r, rs, err := s.listingScope(p, a)
	if err != nil {
		return nil, err
	}
	st := s.backupState()
	if st.ExternalChange {
		return map[string]any{"configured": destination.Remote != "", "external_change": true, "state": "reconciliation_required", "entries": []any{}, "deletions": []any{}, "receipts": []any{}, "last_backup_error": "The remote branch requires administrator reconciliation."}, nil
	}
	receipts, err := s.receipts()
	if err != nil {
		return nil, err
	}
	entries := []any{}
	deletions := []any{}
	visibleReceipts := []any{}
	now := time.Now()
	for _, rec := range receipts {
		visible := rec.IdentityID == p.IdentityID
		for _, sel := range rec.Selections {
			if !within(sel.FolderPath, r.Path) || s.effective(p, sel.FolderPath) < roleReader {
				visible = false
			}
		}
		if visible {
			expires, _ := time.Parse(time.RFC3339Nano, rec.ExpiresAt)
			if rec.State == "PREPARED" && rec.Base != st.Tip {
				rec.State = "STALE"
			} else if rec.State == "PREPARED" && now.After(expires) {
				rec.State = "EXPIRED"
			}
			visibleReceipts = append(visibleReceipts, asMap(rec))
		}
	}
	for _, f := range rs {
		if !within(f.Path, r.Path) || s.effective(p, f.Path) < roleReader {
			continue
		}
		for _, e := range f.Entries {
			fp, exists, inspectErr := s.treeFingerprint(context.Background(), st.Tip, e.Path)
			if inspectErr != nil {
				return nil, inspectErr
			}
			state := "pending"
			if st.Initialized && exists && fp == e.Fingerprint {
				state = "backed_up"
			} else {
				for _, rec := range receipts {
					expires, _ := time.Parse(time.RFC3339Nano, rec.ExpiresAt)
					if rec.State != "PREPARED" || rec.Base != st.Tip || now.After(expires) {
						continue
					}
					for _, sel := range rec.Selections {
						if sel.EntryID == e.ID && sel.Fingerprint == e.Fingerprint {
							state = "committed_not_pushed"
						}
					}
				}
			}
			m := publicEntry(e)
			m["backup_status"] = state
			entries = append(entries, m)
		}
		for _, d := range f.Deletions {
			_, exists, inspectErr := s.treeFingerprint(context.Background(), st.Tip, d.Path)
			if inspectErr != nil {
				return nil, inspectErr
			}
			state := "pending"
			if destination.Remote == "" && !st.Initialized && s.deletionReusable(d) {
				state = "not_required"
			} else if st.Initialized && !exists && s.deletionReusable(d) {
				state = "backed_up"
			} else {
				for _, rec := range receipts {
					expires, _ := time.Parse(time.RFC3339Nano, rec.ExpiresAt)
					if rec.State != "PREPARED" || rec.Base != st.Tip || now.After(expires) {
						continue
					}
					for _, sel := range rec.Selections {
						if sel.DeletionID == d.ID {
							state = "committed_not_pushed"
						}
					}
				}
			}
			m := asMap(d)
			m["backup_status"] = state
			deletions = append(deletions, m)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return stringArg(asMap(entries[i]), "path") < stringArg(asMap(entries[j]), "path")
	})
	return map[string]any{"configured": destination.Remote != "", "entries": entries, "deletions": deletions, "receipts": visibleReceipts, "last_backup_error": st.LastError, "last_successful_backup_at": st.LastSuccessfulAt}, nil
}

// Fixed options isolate host hooks and automatic repository writes. Authentication
// helpers remain backend-owned; no caller controls the environment or configuration.
func (s *Service) gitArgs() []string {
	return []string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false", "-c", "push.gpgSign=false", "-c", "core.fsmonitor=false", "-c", "core.attributesFile=/dev/null", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "push.followTags=false", "-c", "remote.origin.mirror=false", "--git-dir=" + s.repo()}
}
func gitEnvironment() []string {
	out := []string{}
	for _, v := range os.Environ() {
		key := strings.SplitN(v, "=", 2)[0]
		if strings.HasPrefix(key, "GIT_") && key != "GIT_SSH" && key != "GIT_SSH_COMMAND" && key != "GIT_ASKPASS" {
			continue
		}
		out = append(out, v)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
}

func (s *Service) isAncestor(ctx context.Context, ancestor, tip string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", append(s.gitArgs(), "merge-base", "--is-ancestor", ancestor, tip)...)
	cmd.Env = gitEnvironment()
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, retryErr("BACKUP_OUTCOME_UNKNOWN", "Backup delivery could not be determined; retry to reconcile.", 3)
}

// ReconcileBackup is a maintenance boundary for an administrator who explicitly
// accepts a pre-existing or externally changed plain-Markdown branch. It never
// imports, commits, or publishes content.
func (s *Service) ReconcileBackup(ctx context.Context, p Principal) (any, error) {
	if p.Recheck != nil {
		if e := p.Recheck(ctx); e != nil {
			return nil, e
		}
	}
	if !p.IsAdmin || p.AccessOnly || p.Caps != nil || !s.IdentityActive(ctx, p.IdentityID) {
		return nil, kbErr("FORBIDDEN", "An administrator is required to reconcile a backup.")
	}
	release, e := s.publicationLock()
	if e != nil {
		return nil, e
	}
	defer release()
	netctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if e = s.ensureRepo(netctx); e != nil {
		return nil, e
	}
	tip, e := s.remoteTip(netctx)
	if e != nil {
		return nil, e
	}
	if tip != "" {
		b, e := s.gitBytes(netctx, "ls-tree", "-r", "-z", tip)
		if e != nil {
			return nil, e
		}
		for _, record := range strings.Split(string(b), "\x00") {
			if record == "" {
				continue
			}
			parts := strings.SplitN(record, "\t", 2)
			if len(parts) != 2 {
				return nil, kbErr("BACKUP_REMOTE_CHANGED", "The branch is not a plain-Markdown backup.")
			}
			header := strings.Fields(parts[0])
			if len(header) != 3 || header[0] != "100644" || header[1] != "blob" || validatePath(parts[1], true) != nil {
				return nil, kbErr("BACKUP_REMOTE_CHANGED", "The branch is not a plain-Markdown backup.")
			}
		}
	}
	unlock, e := s.lock(ctx, false)
	if e != nil {
		return nil, e
	}
	defer unlock()
	if p.Recheck != nil {
		if e := p.Recheck(ctx); e != nil {
			return nil, e
		}
	}
	if !s.IdentityActive(ctx, p.IdentityID) {
		return nil, kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
	}
	receipts, e := s.receipts()
	if e != nil {
		return nil, e
	}
	for _, r := range receipts {
		if r.State == "PUSH_UNKNOWN" {
			return nil, retryErr("BACKUP_OUTCOME_UNKNOWN", "Reconcile the unresolved receipt with an explicit push retry first.", 3)
		}
	}
	st := s.backupState()
	st.Tip = tip
	st.Initialized = true
	st.ExternalChange = false
	st.ObservedTip = ""
	st.LastError = ""
	if st.Paths == nil {
		st.Paths = map[string]publishedPath{}
	}
	staleChanges := []fileChange{}
	for _, r := range receipts {
		if r.State == "PREPARED" {
			r.State = "STALE"
			staleChanges = append(staleChanges, receiptChange(s, r))
		}
	}
	rs, e := s.registries()
	if e != nil {
		return nil, e
	}
	for _, f := range rs {
		for _, entry := range f.Entries {
			fp, exists, e := s.treeFingerprint(netctx, tip, entry.Path)
			if e != nil {
				return nil, e
			}
			if exists && fp == entry.Fingerprint {
				st.Paths[entry.Path] = publishedPath{EntryID: entry.ID, Sequence: entry.ContentSequence, Fingerprint: entry.Fingerprint}
			}
		}
	}
	result := map[string]any{"reconciled": true, "base_commit_id": tip}
	return result, s.transact(append(staleChanges, jsonChange(filepath.Join(s.private, "backup-state.json"), st)))
}
