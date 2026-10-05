package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Local git actions for the Files pane: stage, unstage, discard and commit.
// Push and pull need the user's remote credentials, which live with the agent,
// so those stay agent requests.
//
// Writing is narrower than reading: the caller must own the tree, or hold the
// editor role or better on the Code (never an admin by role alone). Every
// action runs with the read-only view's hardening plus these: a repo whose
// config defines filters or includes is refused (a clean/smudge filter runs a
// command on add/restore), commits are unsigned and hook-free, and the author
// is the signed-in person.

const (
	workspaceGitWriteTimeout = 30 * time.Second
	workspaceGitMaxActFiles  = 500
	workspaceGitMaxMessage   = 5000
	workspaceGitMaxConfig    = 64 << 10
)

// One action at a time per repo, so the pane's own clicks never race on the
// index lock.
var workspaceGitRepoLocks sync.Map

func workspaceGitLock(dir string) func() {
	value, _ := workspaceGitRepoLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// workspaceGitWriteAllowed reports whether the caller may change the repo
// tree at canonical ("_users/<owner>/...").
var workspaceGitWriteAllowed = func(ctx context.Context, r *http.Request, claims *UserClaims, canonical string) bool {
	ref := workspaceref.MustParse(canonical)
	if !ref.HasOwner() {
		return false
	}
	caller := sanitizeUserIDForPath(publicWorkspaceUserID(r))
	if caller != "" && ref.Owner() == caller {
		return true
	}
	if root, project, ok := ref.Project(); claims != nil && ok && root == workspaceref.CodeProjectsRoot {
		return codeRoleFor(ctx, claims.UserID, ref.Owner(), project).atLeast(codeRoleEditor)
	}
	return false
}

// workspaceGitWriteSafe refuses repos whose own config could run a command
// during add, restore or commit.
func workspaceGitWriteSafe(repoDir string) error {
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Lstat(filepath.Join(gitDir, "config.worktree")); err == nil {
		return errors.New("this repo uses per-worktree config; ask the agent to make this change")
	}
	file, err := os.Open(filepath.Join(gitDir, "config"))
	if err != nil {
		return nil
	}
	defer file.Close()
	raw := make([]byte, workspaceGitMaxConfig)
	n, _ := file.Read(raw)
	config := strings.ToLower(string(raw[:n]))
	for _, marker := range []string{"[filter", "[include", "[includeif"} {
		if strings.Contains(config, marker) {
			return errors.New("this repo's config defines filters or includes; ask the agent to make this change")
		}
	}
	return nil
}

func workspaceGitRunWrite(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	cmd := workspaceGitCommand(ctx, dir, args...)
	cmd.Env = append(cmd.Env, extraEnv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := out.String()
	if len(text) > 4096 {
		text = text[:4096]
	}
	return strings.TrimSpace(text), err
}

// workspaceGitActionPaths validates the file arguments. A folder entry (as
// status lists an untracked folder) may end in "/".
func workspaceGitActionPaths(raw []string) ([]string, bool) {
	if len(raw) == 0 || len(raw) > workspaceGitMaxActFiles {
		return nil, false
	}
	files := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSuffix(strings.TrimSpace(item), "/")
		clean, ok := workspaceGitRelFile(item)
		if !ok || clean == ".git" || strings.HasPrefix(clean, ".git/") {
			return nil, false
		}
		files = append(files, clean)
	}
	return files, true
}

type workspaceGitActionRequest struct {
	WorkspacePath string   `json:"workspace_path"`
	Repo          string   `json:"repo"`
	Op            string   `json:"op"`
	Files         []string `json:"files"`
	All           bool     `json:"all"`
	Message       string   `json:"message"`
	// Branch operations. Remote names a remote-tracking branch to track.
	Branch string `json:"branch"`
	Remote bool   `json:"remote"`
	// Ref names a stash entry ("stash@{0}"); Choice resolves a conflict.
	Ref    string `json:"ref"`
	Choice string `json:"choice"`
}

var (
	workspaceGitBranchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
	workspaceGitStashRe  = regexp.MustCompile(`^stash@\{[0-9]{1,3}\}$`)
)

// workspaceGitBranchName validates a branch name as git itself would take it,
// and refuses anything that could read as an option.
func workspaceGitBranchName(ctx context.Context, dir, name string) bool {
	if !workspaceGitBranchRe.MatchString(name) || strings.Contains(name, "..") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".lock") {
		return false
	}
	_, err := workspaceGitRunWrite(ctx, dir, nil, "check-ref-format", "--branch", name)
	return err == nil
}

// workspaceGitResolveBoth keeps both sides of every conflict block, dropping
// the markers (and any diff3 base section).
func workspaceGitResolveBoth(content string) string {
	var out []string
	inBase := false
	for _, line := range strings.SplitAfter(content, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<< "), strings.HasPrefix(line, ">>>>>>> "), strings.HasPrefix(line, "=======") && strings.TrimRight(line, "\r\n") == "=======":
			inBase = false
		case strings.HasPrefix(line, "||||||| "):
			inBase = true
		default:
			if !inBase {
				out = append(out, line)
			}
		}
	}
	return strings.Join(out, "")
}

// gitIdentity is the commit author: the signed-in person.
func gitIdentity(claims *UserClaims) (name, email string) {
	name, email = "AgentWorks user", "user@agentworks.local"
	if claims == nil {
		return
	}
	if v := strings.TrimSpace(claims.Username); v != "" {
		name = v
	}
	if v := strings.TrimSpace(claims.Email); v != "" {
		email = v
	} else if id := sanitizeUserIDForPath(claims.UserID); id != "" {
		email = id + "@users.agentworks.local"
	}
	return
}

// handleWorkspaceGitAction serves POST /api/workspace-git.
func (api *StreamingAPI) handleWorkspaceGitAction(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req workspaceGitActionRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil {
		writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	canonical, ok := workspaceGitCanonicalPath(r, req.WorkspacePath)
	if !ok {
		writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace_path"})
		return
	}
	claims := GetUserFromContext(r.Context())
	if !workspaceGitWriteAllowed(r.Context(), r, claims, canonical) {
		writeWorkspaceGitJSON(w, http.StatusForbidden, map[string]string{"error": "you do not have permission to change files here"})
		return
	}
	root := workspaceGitDocsRoot()
	if root == "" {
		writeWorkspaceGitJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace is not available"})
		return
	}
	base := filepath.Join(root, filepath.FromSlash(canonical))
	realBase, ok := workspaceGitConfined(base, base)
	if !ok {
		writeWorkspaceGitJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	repoDir, status, message := workspaceGitResolveRepo(realBase, req.Repo)
	if status != 0 {
		writeWorkspaceGitJSON(w, status, map[string]string{"error": message})
		return
	}
	if err := workspaceGitWriteSafe(repoDir); err != nil {
		writeWorkspaceGitJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	serveWorkspaceGitAction(w, r, repoDir, req, claims)
}

// Shared action implementation; callers must authorize and select the repository.
func serveWorkspaceGitAction(w http.ResponseWriter, r *http.Request, repoDir string, req workspaceGitActionRequest, claims *UserClaims) {
	unlock := workspaceGitLock(repoDir)
	defer unlock()
	ctx, cancel := context.WithTimeout(r.Context(), workspaceGitWriteTimeout)
	defer cancel()

	fail := func(code int, text string) {
		writeWorkspaceGitJSON(w, code, map[string]string{"error": text})
	}
	gitFail := func(out string, err error) {
		if out == "" {
			out = "git failed"
		}
		if strings.Contains(out, "index.lock") {
			out = "another git operation is running in this repo; try again in a moment"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			out = "git took too long"
		}
		fail(http.StatusConflict, out)
	}

	switch req.Op {
	case "stage":
		if req.All {
			if out, err := workspaceGitRunWrite(ctx, repoDir, nil, "add", "-A"); err != nil {
				gitFail(out, err)
				return
			}
			break
		}
		files, ok := workspaceGitActionPaths(req.Files)
		if !ok {
			fail(http.StatusBadRequest, "invalid files")
			return
		}
		if out, err := workspaceGitRunWrite(ctx, repoDir, nil, append([]string{"add", "-A", "--"}, files...)...); err != nil {
			gitFail(out, err)
			return
		}
	case "unstage":
		_, headErr := workspaceGitRunWrite(ctx, repoDir, nil, "rev-parse", "--verify", "-q", "HEAD")
		var args []string
		if req.All {
			args = []string{"reset", "-q"}
			if headErr != nil {
				args = []string{"rm", "--cached", "-r", "-q", "--ignore-unmatch", "--", "."}
			}
		} else {
			files, ok := workspaceGitActionPaths(req.Files)
			if !ok {
				fail(http.StatusBadRequest, "invalid files")
				return
			}
			if headErr == nil {
				args = append([]string{"restore", "--staged", "--"}, files...)
			} else {
				args = append([]string{"rm", "--cached", "-r", "-q", "--ignore-unmatch", "--"}, files...)
			}
		}
		if out, err := workspaceGitRunWrite(ctx, repoDir, nil, args...); err != nil {
			gitFail(out, err)
			return
		}
	case "discard":
		files, ok := workspaceGitActionPaths(req.Files)
		if !ok {
			fail(http.StatusBadRequest, "invalid files")
			return
		}
		current, err := workspaceGitStatus(ctx, repoDir)
		if err != nil {
			fail(http.StatusInternalServerError, "git failed")
			return
		}
		byPath := map[string]workspaceGitFile{}
		for _, file := range current.Files {
			byPath[strings.TrimSuffix(file.Path, "/")] = file
		}
		for _, file := range files {
			entry, found := byPath[file]
			switch {
			case !found:
				continue // already clean
			case entry.WorktreeStatus == "conflict":
				fail(http.StatusConflict, "resolve the merge conflict in "+file+" first")
				return
			case entry.WorktreeStatus == "untracked":
				if out, err := workspaceGitRunWrite(ctx, repoDir, nil, "clean", "-f", "-d", "-q", "--", file); err != nil {
					gitFail(out, err)
					return
				}
			case entry.WorktreeStatus == "":
				fail(http.StatusConflict, file+" has only staged changes; unstage it first")
				return
			default:
				if out, err := workspaceGitRunWrite(ctx, repoDir, nil, "restore", "--", file); err != nil {
					gitFail(out, err)
					return
				}
			}
		}
	case "commit":
		text := strings.TrimSpace(req.Message)
		if text == "" || len(text) > workspaceGitMaxMessage {
			fail(http.StatusBadRequest, "write a commit message (up to 5000 characters)")
			return
		}
		if req.All {
			if out, err := workspaceGitRunWrite(ctx, repoDir, nil, "add", "-A"); err != nil {
				gitFail(out, err)
				return
			}
		}
		// Exit 1 means something is staged.
		if _, err := workspaceGitRunWrite(ctx, repoDir, nil, "diff", "--cached", "--quiet"); err == nil {
			fail(http.StatusBadRequest, "nothing is staged to commit")
			return
		}
		name, email := gitIdentity(claims)
		env := []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email}
		if out, err := workspaceGitRunWrite(ctx, repoDir, env, "commit", "-q", "--no-gpg-sign", "-m", text); err != nil {
			gitFail(out, err)
			return
		}
	case "checkout", "create_branch", "delete_branch":
		if !workspaceGitBranchName(ctx, repoDir, req.Branch) {
			fail(http.StatusBadRequest, "invalid branch name")
			return
		}
		var args []string
		switch req.Op {
		case "checkout":
			args = []string{"switch", req.Branch}
			if req.Remote {
				args = []string{"switch", "--track", req.Branch}
			}
		case "create_branch":
			args = []string{"switch", "-c", req.Branch}
		default:
			current, _ := workspaceGitStatus(ctx, repoDir)
			if current.Branch == req.Branch {
				fail(http.StatusConflict, "cannot delete the branch you are on")
				return
			}
			args = []string{"branch", "-d", req.Branch}
		}
		if out, err := workspaceGitRunWrite(ctx, repoDir, nil, args...); err != nil {
			gitFail(out, err)
			return
		}
	case "stash", "stash_apply", "stash_pop", "stash_drop":
		name, email := gitIdentity(claims)
		env := []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email}
		var args []string
		if req.Op == "stash" {
			args = []string{"stash", "push", "--include-untracked"}
			if text := strings.TrimSpace(req.Message); text != "" {
				if len(text) > 200 {
					fail(http.StatusBadRequest, "keep the stash message under 200 characters")
					return
				}
				args = append(args, "-m", text)
			}
		} else {
			if !workspaceGitStashRe.MatchString(req.Ref) {
				fail(http.StatusBadRequest, "invalid stash")
				return
			}
			args = []string{"stash", strings.TrimPrefix(req.Op, "stash_"), req.Ref}
		}
		if out, err := workspaceGitRunWrite(ctx, repoDir, env, args...); err != nil {
			gitFail(out, err)
			return
		}
	case "resolve":
		files, ok := workspaceGitActionPaths(req.Files)
		if !ok || len(files) != 1 {
			fail(http.StatusBadRequest, "name one file to resolve")
			return
		}
		file := files[0]
		current, err := workspaceGitStatus(ctx, repoDir)
		if err != nil {
			fail(http.StatusInternalServerError, "git failed")
			return
		}
		conflicted := false
		for _, entry := range current.Files {
			if entry.Path == file && entry.WorktreeStatus == "conflict" {
				conflicted = true
			}
		}
		if !conflicted {
			fail(http.StatusConflict, file+" has no merge conflict")
			return
		}
		switch req.Choice {
		case "ours", "theirs":
			if out, err := workspaceGitRunWrite(ctx, repoDir, nil, "checkout", "--"+req.Choice, "--", file); err != nil {
				gitFail(out, err)
				return
			}
		case "both":
			target := filepath.Join(repoDir, filepath.FromSlash(file))
			// A symlinked folder on the way could point outside the repo.
			if _, inside := workspaceGitConfined(repoDir, filepath.Dir(target)); !inside {
				fail(http.StatusConflict, "this file cannot be resolved here; ask the agent")
				return
			}
			info, err := os.Lstat(target)
			if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
				fail(http.StatusConflict, "this file cannot be resolved here; ask the agent")
				return
			}
			raw, err := os.ReadFile(target)
			if err != nil {
				fail(http.StatusConflict, "could not read "+file)
				return
			}
			if err := os.WriteFile(target, []byte(workspaceGitResolveBoth(string(raw))), info.Mode().Perm()); err != nil {
				fail(http.StatusConflict, "could not write "+file)
				return
			}
		default:
			fail(http.StatusBadRequest, "choice must be ours, theirs or both")
			return
		}
		if out, err := workspaceGitRunWrite(ctx, repoDir, nil, "add", "--", file); err != nil {
			gitFail(out, err)
			return
		}
	default:
		fail(http.StatusBadRequest, "unknown op")
		return
	}

	repo, err := workspaceGitStatus(ctx, repoDir)
	if err != nil {
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	repo.Root = req.Repo
	writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"ok": true, "repo": repo})
}
