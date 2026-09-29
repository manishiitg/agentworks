package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitPost(t *testing.T, user string, body map[string]any) (int, map[string]any) {
	t.Helper()
	api := &StreamingAPI{}
	req := sharedSecretsRequest(http.MethodPost, "/api/workspace-git", user, body)
	w := httptest.NewRecorder()
	api.handleWorkspaceGitAction(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repoFiles(body map[string]any) map[string]map[string]any {
	files := map[string]map[string]any{}
	repo, _ := body["repo"].(map[string]any)
	list, _ := repo["files"].([]any)
	for _, f := range list {
		file := f.(map[string]any)
		files[file["path"].(string)] = file
	}
	return files
}

func actionBody(op string, extra map[string]any) map[string]any {
	body := map[string]any{"workspace_path": gitTestProject, "repo": "app", "op": op}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func TestWorkspaceGitStageUnstageDiscardCommit(t *testing.T) {
	_, repo := gitTestEnv(t)

	// a.txt is modified (unstaged), new.txt is staged-added, loose.txt untracked.
	code, body := gitPost(t, "alice", actionBody("stage", map[string]any{"files": []string{"a.txt"}}))
	if code != http.StatusOK || repoFiles(body)["a.txt"]["index_status"] != "modified" {
		t.Fatalf("stage: %d %v", code, body)
	}
	code, body = gitPost(t, "alice", actionBody("unstage", map[string]any{"files": []string{"a.txt"}}))
	f := repoFiles(body)["a.txt"]
	if code != http.StatusOK || f["index_status"] != nil || f["worktree_status"] != "modified" {
		t.Fatalf("unstage: %d %v", code, body)
	}

	// Discard restores the file; an untracked file is deleted; staged-only is refused.
	if code, body = gitPost(t, "alice", actionBody("discard", map[string]any{"files": []string{"a.txt"}})); code != http.StatusOK {
		t.Fatalf("discard modified: %d %v", code, body)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "one\n" {
		t.Fatalf("a.txt not restored: %q", got)
	}
	if code, body = gitPost(t, "alice", actionBody("discard", map[string]any{"files": []string{"loose.txt"}})); code != http.StatusOK {
		t.Fatalf("discard untracked: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(repo, "loose.txt")); err == nil {
		t.Fatal("untracked file survived discard")
	}
	if code, _ = gitPost(t, "alice", actionBody("discard", map[string]any{"files": []string{"new.txt"}})); code != http.StatusConflict {
		t.Fatalf("discarding a staged-only file: %d", code)
	}

	// Commit needs a message and staged content, and is authored by the caller.
	if code, _ = gitPost(t, "alice", actionBody("commit", map[string]any{"message": "  "})); code != http.StatusBadRequest {
		t.Fatalf("empty message: %d", code)
	}
	code, body = gitPost(t, "alice", actionBody("commit", map[string]any{"message": "add new.txt"}))
	if code != http.StatusOK || len(repoFiles(body)) != 0 {
		t.Fatalf("commit: %d %v", code, body)
	}
	if subject := gitOut(t, repo, "log", "-1", "--format=%s|%an"); subject != "add new.txt|alice" && !strings.HasPrefix(subject, "add new.txt|") {
		t.Fatalf("commit: %q", subject)
	}
	if code, _ = gitPost(t, "alice", actionBody("commit", map[string]any{"message": "again"})); code != http.StatusBadRequest {
		t.Fatalf("nothing staged: %d", code)
	}

	// Commit all stages everything first.
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	if code, body = gitPost(t, "alice", actionBody("commit", map[string]any{"message": "add b", "all": true})); code != http.StatusOK {
		t.Fatalf("commit all: %d %v", code, body)
	}
	if gitOut(t, repo, "status", "--porcelain") != "" {
		t.Fatal("tree not clean after commit all")
	}
}

func TestWorkspaceGitWriteIsRefusedToStrangersAndBadInput(t *testing.T) {
	gitTestEnv(t)
	if code, _ := gitPost(t, "bob", actionBody("stage", map[string]any{"files": []string{"a.txt"}})); code != http.StatusForbidden {
		t.Fatalf("a stranger staged in alice's Code: %d", code)
	}
	for _, file := range []string{"../x", "/etc/passwd", "-A", ".git/config", ".git"} {
		if code, _ := gitPost(t, "alice", actionBody("stage", map[string]any{"files": []string{file}})); code != http.StatusBadRequest {
			t.Fatalf("file %q accepted: %d", file, code)
		}
	}
	if code, _ := gitPost(t, "alice", actionBody("nuke", nil)); code != http.StatusBadRequest {
		t.Fatalf("unknown op: %d", code)
	}
}

func TestWorkspaceGitWriteRefusesRepoFiltersAndNeverRunsHooks(t *testing.T) {
	_, repo := gitTestEnv(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	writeFile(t, hook, "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, body := gitPost(t, "alice", actionBody("commit", map[string]any{"message": "with hook"})); code != http.StatusOK {
		t.Fatalf("commit: %d %v", code, body)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repo hook ran during a commit")
	}

	// A clean filter would run a command on add: refused.
	cfg := filepath.Join(repo, ".git", "config")
	existing, _ := os.ReadFile(cfg)
	writeFile(t, cfg, string(existing)+"\n[filter \"x\"]\n\tclean = touch "+marker+"\n")
	writeFile(t, filepath.Join(repo, "z.txt"), "z\n")
	if code, _ := gitPost(t, "alice", actionBody("stage", map[string]any{"files": []string{"z.txt"}})); code != http.StatusConflict {
		t.Fatalf("filter repo not refused: %d", code)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repo filter ran")
	}
}
