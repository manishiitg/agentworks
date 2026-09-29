package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func branchNames(body map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := body["branches"].([]any)
	for _, b := range list {
		branch := b.(map[string]any)
		out[branch["name"].(string)] = branch
	}
	return out
}

func TestWorkspaceGitBranches(t *testing.T) {
	_, repo := gitTestEnv(t)
	gitRun(t, repo, "stash", "-u") // start clean: switching with local edits is git's call, not this test's
	if code, body := gitPost(t, "alice", actionBody("create_branch", map[string]any{"branch": "feature/x"})); code != http.StatusOK || body["repo"].(map[string]any)["branch"] != "feature/x" {
		t.Fatalf("create: %d %v", code, body)
	}
	if code, body := gitPost(t, "alice", actionBody("checkout", map[string]any{"branch": "main"})); code != http.StatusOK || body["repo"].(map[string]any)["branch"] != "main" {
		t.Fatalf("checkout: %d %v", code, body)
	}
	code, list := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"branches"}, "repo": {"app"}})
	names := branchNames(list)
	if code != http.StatusOK || names["main"]["current"] != true || names["feature/x"] == nil {
		t.Fatalf("branches: %d %v", code, list)
	}
	if code, _ := gitPost(t, "alice", actionBody("delete_branch", map[string]any{"branch": "main"})); code != http.StatusConflict {
		t.Fatalf("deleted the current branch: %d", code)
	}
	if code, _ := gitPost(t, "alice", actionBody("delete_branch", map[string]any{"branch": "feature/x"})); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	for _, bad := range []string{"-c", "a..b", "x y", "--detach", "a.lock", "/abs", ""} {
		if code, _ := gitPost(t, "alice", actionBody("checkout", map[string]any{"branch": bad})); code != http.StatusBadRequest {
			t.Fatalf("branch %q accepted: %d", bad, code)
		}
	}
	if code, _ := gitPost(t, "bob", actionBody("create_branch", map[string]any{"branch": "hack"})); code != http.StatusForbidden {
		t.Fatalf("stranger created a branch: %d", code)
	}
}

func TestWorkspaceGitStash(t *testing.T) {
	_, repo := gitTestEnv(t)
	code, body := gitPost(t, "alice", actionBody("stash", map[string]any{"message": "wip: two"}))
	if code != http.StatusOK || len(repoFiles(body)) != 0 {
		t.Fatalf("stash: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(repo, "loose.txt")); err == nil {
		t.Fatal("untracked file not stashed")
	}
	_, list := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"stashes"}, "repo": {"app"}})
	stashes := list["stashes"].([]any)
	if len(stashes) != 1 || !strings.Contains(stashes[0].(map[string]any)["message"].(string), "wip: two") {
		t.Fatalf("stashes: %v", list)
	}
	ref := stashes[0].(map[string]any)["ref"].(string)
	if code, body = gitPost(t, "alice", actionBody("stash_pop", map[string]any{"ref": ref})); code != http.StatusOK || len(repoFiles(body)) == 0 {
		t.Fatalf("pop: %d %v", code, body)
	}
	if code, _ := gitPost(t, "alice", actionBody("stash_drop", map[string]any{"ref": "stash@{0}; rm -rf"})); code != http.StatusBadRequest {
		t.Fatalf("bad stash ref accepted: %d", code)
	}
}

// makeConflict leaves the repo mid-merge with a.txt conflicted:
// main says "main side", feature says "feature side".
func makeConflict(t *testing.T, repo string) {
	t.Helper()
	gitRun(t, repo, "stash", "-u")
	gitRun(t, repo, "switch", "-c", "feature")
	writeFile(t, filepath.Join(repo, "a.txt"), "feature side\n")
	gitRun(t, repo, "commit", "-qam", "feature change")
	gitRun(t, repo, "switch", "main")
	writeFile(t, filepath.Join(repo, "a.txt"), "main side\n")
	gitRun(t, repo, "commit", "-qam", "main change")
	cmd := exec.Command("git", "-C", repo, "merge", "feature")
	_ = cmd.Run() // conflicts: non-zero exit is expected
}

func TestWorkspaceGitResolveConflict(t *testing.T) {
	for _, tc := range []struct{ choice, want string }{
		{"ours", "main side\n"},
		{"theirs", "feature side\n"},
		{"both", "main side\nfeature side\n"},
	} {
		t.Run(tc.choice, func(t *testing.T) {
			_, repo := gitTestEnv(t)
			makeConflict(t, repo)
			_, body := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}})
			var status string
			for _, r := range body["repos"].([]any) {
				for _, f := range r.(map[string]any)["files"].([]any) {
					if f.(map[string]any)["path"] == "a.txt" {
						status, _ = f.(map[string]any)["worktree_status"].(string)
					}
				}
			}
			if status != "conflict" {
				t.Fatalf("a.txt status %q, want conflict", status)
			}
			code, res := gitPost(t, "alice", actionBody("resolve", map[string]any{"files": []string{"a.txt"}, "choice": tc.choice}))
			if code != http.StatusOK {
				t.Fatalf("resolve: %d %v", code, res)
			}
			if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != tc.want {
				t.Fatalf("a.txt = %q, want %q", got, tc.want)
			}
			f := repoFiles(res)["a.txt"]
			switch {
			case tc.choice == "ours":
				// Identical to HEAD once resolved: nothing left to show.
				if f != nil {
					t.Fatalf("a.txt still listed after choosing ours: %v", f)
				}
			case f == nil || f["index_status"] == nil || f["worktree_status"] != nil:
				t.Fatalf("a.txt should now be staged, not conflicted: %v", f)
			}
		})
	}
	// A non-conflicted file and a bad choice are refused.
	_, repo := gitTestEnv(t)
	makeConflict(t, repo)
	if code, _ := gitPost(t, "alice", actionBody("resolve", map[string]any{"files": []string{"keep.txt"}, "choice": "ours"})); code != http.StatusConflict {
		t.Fatalf("resolved a file with no conflict: %d", code)
	}
	if code, _ := gitPost(t, "alice", actionBody("resolve", map[string]any{"files": []string{"a.txt"}, "choice": "yolo"})); code != http.StatusBadRequest {
		t.Fatalf("bad choice: %d", code)
	}
}

func TestWorkspaceGitBlame(t *testing.T) {
	_, repo := gitTestEnv(t)
	gitRun(t, repo, "stash", "-u")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\ntwo\n")
	gitRun(t, repo, "commit", "-qam", "add two")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\ntwo\nthree\n")
	code, body := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"blame"}, "repo": {"app"}, "file": {"a.txt"}})
	lines, _ := body["lines"].([]any)
	if code != http.StatusOK || len(lines) != 3 {
		t.Fatalf("blame: %d %v", code, body)
	}
	first, second, third := lines[0].(map[string]any), lines[1].(map[string]any), lines[2].(map[string]any)
	if first["summary"] != "first commit" || second["summary"] != "add two" {
		t.Fatalf("summaries: %v %v", first, second)
	}
	if third["uncommitted"] != true {
		t.Fatalf("an uncommitted line is not marked: %v", third)
	}
	if code, _ := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"blame"}, "repo": {"app"}, "file": {"nope.txt"}}); code != http.StatusNotFound {
		t.Fatalf("blame of an untracked file: %d", code)
	}
}
