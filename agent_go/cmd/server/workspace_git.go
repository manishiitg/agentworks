package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Read-only git view for the Files pane (VS Code style): which repos a
// workspace folder holds, their branch and changed files, a file's diff and
// its history. Nothing here writes; commits, pulls and pushes go through the
// agent.
//
// Access is the file-read check on workspace_path (owner, or anyone a Code is
// shared with). git runs on the server against the Code's own folder, so a
// repo is untrusted input: hooks, fsmonitor, external diff and textconv are
// off, global/system config is ignored, the index lock is not taken, and every
// path is confined to the workspace before and after symlink resolution.

const (
	workspaceGitTimeout      = 10 * time.Second
	workspaceGitMaxFiles     = 5000
	workspaceGitMaxDiffBytes = 1 << 20
	workspaceGitMaxCommits   = 30
	workspaceGitMaxRepos     = 20
	workspaceGitSearchDepth  = 3
	workspaceGitMaxScan      = 400
)

var workspaceGitCommitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Folders never searched for nested repos (dependency and build output).
var workspaceGitSkipDirs = map[string]bool{
	"node_modules": true, ".venv": true, "venv": true, "vendor": true, "dist": true,
	"build": true, ".next": true, "target": true, "__pycache__": true, ".cache": true,
}

type workspaceGitFile struct {
	Path string `json:"path"`
	// Status is one of modified, added, deleted, renamed, untracked, conflict.
	Status string `json:"status"`
	Staged bool   `json:"staged,omitempty"`
	// IndexStatus is the change staged for the next commit ("" when none) and
	// WorktreeStatus the change not yet staged ("" when none, "untracked" for
	// a new file). A file can have both.
	IndexStatus    string `json:"index_status,omitempty"`
	WorktreeStatus string `json:"worktree_status,omitempty"`
}

type workspaceGitRepo struct {
	// Root is the repo folder relative to the requested workspace_path ("" for
	// the workspace itself).
	Root      string             `json:"root"`
	Branch    string             `json:"branch"`
	Detached  bool               `json:"detached,omitempty"`
	Upstream  string             `json:"upstream,omitempty"`
	Ahead     int                `json:"ahead"`
	Behind    int                `json:"behind"`
	Files     []workspaceGitFile `json:"files"`
	Truncated bool               `json:"truncated,omitempty"`
}

type workspaceGitCommit struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

// workspaceGitDocsRoot is the workspace docs directory on this host.
var workspaceGitDocsRoot = func() string { return strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH")) }

// workspaceGitCanonicalPath is the workspace path with per-user logical roots
// (Chats/...) resolved under the caller: "_users/<id>/Chats/...".
func workspaceGitCanonicalPath(r *http.Request, raw string) (string, bool) {
	clean, ok := cleanWorkspaceReadPath(raw, true)
	if !ok {
		return "", false
	}
	if workspaceLogicalPerUserRoots[strings.Split(clean, "/")[0]] {
		owner := sanitizeUserIDForPath(publicWorkspaceUserID(r))
		if owner == "" {
			return "", false
		}
		clean = path.Join("_users", owner, clean)
	}
	return clean, true
}

// workspaceGitPhysicalPath maps a workspace path the caller may read to its
// folder on disk.
func workspaceGitPhysicalPath(r *http.Request, raw string) (string, bool) {
	clean, ok := workspaceGitCanonicalPath(r, raw)
	if !ok {
		return "", false
	}
	root := workspaceGitDocsRoot()
	if root == "" {
		return "", false
	}
	return filepath.Join(root, filepath.FromSlash(clean)), true
}

// workspaceGitConfined resolves symlinks and reports whether dir is inside
// base, so a symlink in a repo cannot point outside the workspace.
func workspaceGitConfined(base, dir string) (string, bool) {
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", false
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realBase, realDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return realDir, true
}

func workspaceGitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := append([]string{
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.pager=cat",
		"-c", "core.quotepath=off",
		"-c", "safe.directory=" + dir,
		"-c", "protocol.allow=never",
		"-c", "diff.external=",
		"-c", "gc.auto=0",
		// Explicit git dir and work tree: a repo's own config (core.worktree)
		// or a .git file (gitdir: elsewhere) cannot redirect git outside it.
		"--git-dir", filepath.Join(dir, ".git"),
		"--work-tree", dir,
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
	return cmd
}

// workspaceGitIsRepo reports whether dir holds a plain .git directory. A .git
// file or symlink (which can name a git dir elsewhere) and a repo borrowing
// objects from another path (alternates) are not read.
func workspaceGitIsRepo(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil || !info.IsDir() {
		return false
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git", "objects", "info", "alternates")); err == nil {
		return false
	}
	return true
}

// workspaceGitFindRepos lists repo folders under base: base itself, else the
// repos found up to workspaceGitSearchDepth folders down (dependency and dot
// folders skipped, a found repo's own subfolders not searched, bounded scan).
func workspaceGitFindRepos(base string) []string {
	if workspaceGitIsRepo(base) {
		return []string{base}
	}
	type level struct {
		dir   string
		depth int
	}
	var repos []string
	queue := []level{{base, 0}}
	visited := 0
	for len(queue) > 0 && len(repos) < workspaceGitMaxRepos && visited < workspaceGitMaxScan {
		current := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(current.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || strings.HasPrefix(name, ".") || workspaceGitSkipDirs[name] {
				continue
			}
			visited++
			dir := filepath.Join(current.dir, name)
			if workspaceGitIsRepo(dir) {
				repos = append(repos, dir)
				if len(repos) >= workspaceGitMaxRepos {
					break
				}
				continue
			}
			if current.depth+1 < workspaceGitSearchDepth {
				queue = append(queue, level{dir, current.depth + 1})
			}
		}
	}
	sort.Strings(repos)
	return repos
}

// parseWorkspaceGitStatus reads `git status --porcelain=v2 --branch -z`.
func parseWorkspaceGitStatus(out []byte) workspaceGitRepo {
	repo := workspaceGitRepo{Files: []workspaceGitFile{}}
	fields := bytes.Split(out, []byte{0})
	for i := 0; i < len(fields); i++ {
		line := string(fields[i])
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			repo.Branch = strings.TrimPrefix(line, "# branch.head ")
			if repo.Branch == "(detached)" {
				repo.Detached = true
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			repo.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			for _, part := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				if n, err := strconv.Atoi(part[1:]); err == nil {
					if part[0] == '+' {
						repo.Ahead = n
					} else if part[0] == '-' {
						repo.Behind = n
					}
				}
			}
		case strings.HasPrefix(line, "1 "):
			// 1 XY sub mH mI mW hH hI path
			parts := strings.SplitN(line, " ", 9)
			if len(parts) == 9 {
				repo.Files = append(repo.Files, workspaceGitFileFor(parts[1], parts[8]))
			}
		case strings.HasPrefix(line, "2 "):
			// 2 XY sub mH mI mW hH hI Xscore path <NUL> origPath
			parts := strings.SplitN(line, " ", 10)
			if len(parts) == 10 {
				repo.Files = append(repo.Files, workspaceGitFileFor(parts[1], parts[9]))
			}
			i++ // the original path follows as its own field
		case strings.HasPrefix(line, "u "):
			parts := strings.SplitN(line, " ", 11)
			if len(parts) == 11 {
				repo.Files = append(repo.Files, workspaceGitFile{Path: parts[10], Status: "conflict", WorktreeStatus: "conflict"})
			}
		case strings.HasPrefix(line, "? "):
			repo.Files = append(repo.Files, workspaceGitFile{Path: strings.TrimPrefix(line, "? "), Status: "untracked", WorktreeStatus: "untracked"})
		}
		if len(repo.Files) >= workspaceGitMaxFiles {
			repo.Truncated = true
			break
		}
	}
	return repo
}

func workspaceGitCodeStatus(code byte) string {
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R', 'C':
		return "renamed"
	case 'U':
		return "conflict"
	default:
		return "modified"
	}
}

func workspaceGitFileFor(xy, p string) workspaceGitFile {
	file := workspaceGitFile{Path: p}
	x, y := byte('.'), byte('.')
	if len(xy) == 2 {
		x, y = xy[0], xy[1]
	}
	if x != '.' && x != '?' {
		file.IndexStatus = workspaceGitCodeStatus(x)
		file.Staged = true
	}
	if y != '.' {
		file.WorktreeStatus = workspaceGitCodeStatus(y)
	}
	file.Status = file.WorktreeStatus
	if file.Status == "" {
		file.Status = file.IndexStatus
	}
	return file
}

func workspaceGitStatus(ctx context.Context, repoDir string) (workspaceGitRepo, error) {
	out, err := workspaceGitCommand(ctx, repoDir, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal").Output()
	if err != nil {
		return workspaceGitRepo{}, err
	}
	return parseWorkspaceGitStatus(out), nil
}

// workspaceGitRelFile validates a repo-relative file argument.
func workspaceGitRelFile(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, "\\\x00") {
		return "", false
	}
	clean := path.Clean(raw)
	if clean != raw || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func writeWorkspaceGitJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// workspaceGitResolveRepo resolves a repo folder (relative to base) to a
// confined real path; a non-zero status says why it cannot be used.
func workspaceGitResolveRepo(realBase, repoRel string) (dir string, status int, message string) {
	repoRel = strings.Trim(repoRel, "/")
	dir = realBase
	if repoRel != "" {
		clean, ok := workspaceGitRelFile(repoRel)
		if !ok {
			return "", http.StatusBadRequest, "invalid repo"
		}
		dir = filepath.Join(realBase, filepath.FromSlash(clean))
	}
	dir, ok := workspaceGitConfined(realBase, dir)
	if !ok {
		return "", http.StatusNotFound, "repo not found"
	}
	if !workspaceGitIsRepo(dir) {
		return "", http.StatusNotFound, "not a git repository"
	}
	return dir, 0, ""
}

// handleWorkspaceGit serves GET /api/workspace-git?workspace_path=&op=
//
//	op=status              repos, branches and changed files (default)
//	op=diff&repo=&file=    unified diff of one file against HEAD
//	op=log&repo=[&file=]   recent commits (of one file when given)
//	op=show&repo=&file=&commit=  one commit's diff for one file
//
// It sits behind the file-read check on workspace_path.
func (api *StreamingAPI) handleWorkspaceGit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	base, ok := workspaceGitPhysicalPath(r, requestWorkflowWorkspacePath(r))
	if !ok {
		writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace_path"})
		return
	}
	realBase, ok := workspaceGitConfined(base, base)
	if !ok {
		// Nothing on disk yet: not an error, just no repos.
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"repos": []workspaceGitRepo{}})
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"repos": []workspaceGitRepo{}, "git_unavailable": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), workspaceGitTimeout)
	defer cancel()

	query := r.URL.Query()
	op := query.Get("op")
	if op == "" || op == "status" {
		repos := []workspaceGitRepo{}
		for _, dir := range workspaceGitFindRepos(realBase) {
			real, ok := workspaceGitConfined(realBase, dir)
			if !ok {
				continue
			}
			repo, err := workspaceGitStatus(ctx, real)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(realBase, real)
			if rel == "." {
				rel = ""
			}
			repo.Root = filepath.ToSlash(rel)
			repos = append(repos, repo)
		}
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"repos": repos})
		return
	}

	// diff and log name one repo (relative to workspace_path).
	repoDir, status, message := workspaceGitResolveRepo(realBase, query.Get("repo"))
	if status != 0 {
		writeWorkspaceGitJSON(w, status, map[string]string{"error": message})
		return
	}
	file := ""
	if raw := query.Get("file"); raw != "" {
		clean, ok := workspaceGitRelFile(raw)
		if !ok {
			writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file"})
			return
		}
		file = clean
	}

	switch op {
	case "diff":
		if file == "" {
			writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "file is required"})
			return
		}
		cmd := workspaceGitCommand(ctx, repoDir, "diff", "HEAD", "--no-ext-diff", "--no-textconv", "--no-color", "--", file)
		out, err := cmd.Output()
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				writeWorkspaceGitJSON(w, http.StatusInternalServerError, map[string]string{"error": "git failed"})
				return
			}
			// No HEAD yet (a fresh repo): fall back to the staged diff.
			out, _ = workspaceGitCommand(ctx, repoDir, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--no-color", "--", file).Output()
		}
		truncated := false
		if len(out) > workspaceGitMaxDiffBytes {
			out, truncated = out[:workspaceGitMaxDiffBytes], true
		}
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"diff": string(out), "truncated": truncated})
	case "show":
		// One commit's diff for one file (file history).
		commit := query.Get("commit")
		if file == "" || !workspaceGitCommitRe.MatchString(commit) {
			writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "commit and file are required"})
			return
		}
		out, err := workspaceGitCommand(ctx, repoDir, "show", "--no-ext-diff", "--no-textconv", "--no-color", "--format=", commit, "--", file).Output()
		if err != nil {
			writeWorkspaceGitJSON(w, http.StatusNotFound, map[string]string{"error": "commit not found"})
			return
		}
		truncated := false
		if len(out) > workspaceGitMaxDiffBytes {
			out, truncated = out[:workspaceGitMaxDiffBytes], true
		}
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"diff": string(out), "truncated": truncated})
	case "log":
		args := []string{"log", "--no-color", "-n", strconv.Itoa(workspaceGitMaxCommits), "--date=iso-strict", "--format=%H%x1f%an%x1f%ad%x1f%s%x1e"}
		if file != "" {
			args = append(args, "--", file)
		}
		out, err := workspaceGitCommand(ctx, repoDir, args...).Output()
		commits := []workspaceGitCommit{}
		if err == nil {
			for _, record := range strings.Split(string(out), "\x1e") {
				parts := strings.Split(strings.TrimSpace(record), "\x1f")
				if len(parts) == 4 {
					commits = append(commits, workspaceGitCommit{Hash: parts[0], Author: parts[1], Date: parts[2], Subject: parts[3]})
				}
			}
		}
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"commits": commits})
	default:
		writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown op"})
	}
}
