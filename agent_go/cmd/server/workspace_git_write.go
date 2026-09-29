package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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
	segments := strings.Split(canonical, "/")
	if len(segments) < 2 || segments[0] != "_users" {
		return false
	}
	caller := sanitizeUserIDForPath(publicWorkspaceUserID(r))
	if caller != "" && segments[1] == caller {
		return true
	}
	if claims != nil && len(segments) >= 6 && segments[2] == "Chats" && segments[3] == "Code" && segments[4] == "projects" {
		return codeRoleFor(ctx, claims.UserID, segments[1], segments[5]).atLeast(codeRoleEditor)
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
	default:
		fail(http.StatusBadRequest, "unknown op")
		return
	}

	repo, err := workspaceGitStatus(ctx, repoDir)
	if err != nil {
		writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	rel, _ := filepath.Rel(realBase, repoDir)
	if rel == "." {
		rel = ""
	}
	repo.Root = filepath.ToSlash(rel)
	writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"ok": true, "repo": repo})
}
