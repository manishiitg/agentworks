package server

import (
	"context"
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

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@x.com", "-c", "user.name=T", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const gitTestProject = "_users/alice/Chats/Code/projects/p1"

// gitTestEnv builds a Code root holding one repo (app/) with a commit, a
// modified file, a staged new file and an untracked file.
func gitTestEnv(t *testing.T) (docs, repo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("MULTI_USER_MODE", "true")
	docs = t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	withMemoryUserDirectory(t, `{"users":[
		{"id":"alice","username":"alice","email":"alice@x.com","can_create":true,"products":[]},
		{"id":"bob","username":"bob","email":"bob@x.com","can_create":true,"products":[]}]}`)
	repo = filepath.Join(docs, filepath.FromSlash(gitTestProject), "app")
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, filepath.Dir(repo), "init", "-q", "-b", "main", "app")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
	writeFile(t, filepath.Join(repo, "keep.txt"), "keep\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "first commit")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\ntwo\n")
	writeFile(t, filepath.Join(repo, "new.txt"), "new\n")
	gitRun(t, repo, "add", "new.txt")
	writeFile(t, filepath.Join(repo, "loose.txt"), "loose\n")
	return docs, repo
}

func gitGet(t *testing.T, user string, query url.Values) (int, map[string]any) {
	t.Helper()
	api := &StreamingAPI{}
	req := sharedSecretsRequest(http.MethodGet, "/api/workspace-git?"+query.Encode(), user, nil)
	w := httptest.NewRecorder()
	requireWorkflowReadAccessCallerRelative(api.handleWorkspaceGit)(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestWorkspaceGitStatusFindsNestedRepoAndChanges(t *testing.T) {
	gitTestEnv(t)
	code, body := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}})
	if code != http.StatusOK {
		t.Fatalf("status: %d %v", code, body)
	}
	repos := body["repos"].([]any)
	if len(repos) != 1 {
		t.Fatalf("repos: %v", repos)
	}
	repo := repos[0].(map[string]any)
	if repo["root"] != "app" || repo["branch"] != "main" {
		t.Fatalf("repo: %v", repo)
	}
	got := map[string]string{}
	for _, f := range repo["files"].([]any) {
		file := f.(map[string]any)
		got[file["path"].(string)] = file["status"].(string)
	}
	want := map[string]string{"a.txt": "modified", "new.txt": "added", "loose.txt": "untracked"}
	for path, status := range want {
		if got[path] != status {
			t.Fatalf("%s = %q, want %q (all: %v)", path, got[path], status, got)
		}
	}
	if _, clean := got["keep.txt"]; clean {
		t.Fatalf("an unchanged file is listed: %v", got)
	}
}

func TestWorkspaceGitDiffAndLog(t *testing.T) {
	gitTestEnv(t)
	code, body := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"diff"}, "repo": {"app"}, "file": {"a.txt"}})
	if code != http.StatusOK || !strings.Contains(body["diff"].(string), "+two") {
		t.Fatalf("diff: %d %v", code, body)
	}
	code, body = gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"log"}, "repo": {"app"}, "file": {"a.txt"}})
	commits, _ := body["commits"].([]any)
	if code != http.StatusOK || len(commits) != 1 || commits[0].(map[string]any)["subject"] != "first commit" {
		t.Fatalf("log: %d %v", code, body)
	}
	if refs := commits[0].(map[string]any)["refs"].([]any); len(refs) == 0 || !strings.Contains(refs[0].(string), "main") {
		t.Fatalf("refs: %v", refs)
	}
	hash := commits[0].(map[string]any)["hash"].(string)
	code, body = gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"show"}, "repo": {"app"}, "file": {"a.txt"}, "commit": {hash}})
	if code != http.StatusOK || !strings.Contains(body["diff"].(string), "+one") {
		t.Fatalf("show: %d %v", code, body)
	}
	if code, _ := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"show"}, "repo": {"app"}, "file": {"a.txt"}, "commit": {"--output=/tmp/x"}}); code != http.StatusBadRequest {
		t.Fatalf("option-looking commit accepted: %d", code)
	}
}

func TestWorkspaceGitRefusesOtherPeopleAndBadPaths(t *testing.T) {
	gitTestEnv(t)
	if code, _ := gitGet(t, "bob", url.Values{"workspace_path": {gitTestProject}}); code != http.StatusForbidden {
		t.Fatalf("a stranger read alice's Code git: %d", code)
	}
	for _, file := range []string{"../../etc/passwd", "/etc/passwd", "-p", "a/../b"} {
		if code, _ := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"diff"}, "repo": {"app"}, "file": {file}}); code != http.StatusBadRequest {
			t.Fatalf("file %q accepted: %d", file, code)
		}
	}
	if code, _ := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"log"}, "repo": {"../../.."}}); code != http.StatusBadRequest {
		t.Fatalf("repo escape accepted: %d", code)
	}
}

// A repo is untrusted input: its config must not run code, and a .git file or
// alternates must not reach outside the folder.
func TestWorkspaceGitHostileRepos(t *testing.T) {
	docs, repo := gitTestEnv(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "fsmon.sh")
	writeFile(t, script, "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	// core.fsmonitor and an external diff would run a command.
	cfg := filepath.Join(repo, ".git", "config")
	existing, _ := os.ReadFile(cfg)
	writeFile(t, cfg, string(existing)+"\n[core]\n\tfsmonitor = "+script+"\n[diff]\n\texternal = "+script+"\n")
	// Control: plain git does run the configured command, so the check below
	// proves the handler's hardening rather than an inert config.
	_ = exec.Command("git", "-C", repo, "status").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run core.fsmonitor here; the hostile-config check would prove nothing")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}})
	gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}, "op": {"diff"}, "repo": {"app"}, "file": {"a.txt"}})
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repo's config ran a command")
	}

	// A .git FILE naming a git dir elsewhere is not a repo here.
	other := filepath.Join(docs, filepath.FromSlash(gitTestProject), "linked")
	writeFile(t, filepath.Join(other, ".git"), "gitdir: "+filepath.Join(repo, ".git")+"\n")
	// Alternates borrow objects from another path.
	borrower := filepath.Join(docs, filepath.FromSlash(gitTestProject), "borrower")
	gitRun(t, filepath.Dir(borrower), "init", "-q", "borrower")
	writeFile(t, filepath.Join(borrower, ".git", "objects", "info", "alternates"), filepath.Join(repo, ".git", "objects")+"\n")
	_, body := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}})
	roots := map[string]bool{}
	for _, r := range body["repos"].([]any) {
		roots[r.(map[string]any)["root"].(string)] = true
	}
	if roots["linked"] || roots["borrower"] || !roots["app"] {
		t.Fatalf("repos listed: %v", roots)
	}
}

func TestWorkspaceGitNoRepoIsEmpty(t *testing.T) {
	docs, _ := gitTestEnv(t)
	writeFile(t, filepath.Join(docs, "_users", "alice", "Chats", "Code", "projects", "empty", "readme.md"), "x")
	code, body := gitGet(t, "alice", url.Values{"workspace_path": {"_users/alice/Chats/Code/projects/empty"}})
	if code != http.StatusOK || len(body["repos"].([]any)) != 0 {
		t.Fatalf("no repo: %d %v", code, body)
	}
}

// The Files pane sends the Code's path as the browser knows it: the logical
// "Chats/Code/projects/<id>" form resolves under the caller's own tree.
func TestWorkspaceGitLogicalPathResolvesUnderCaller(t *testing.T) {
	gitTestEnv(t)
	code, body := gitGet(t, "alice", url.Values{"workspace_path": {"Chats/Code/projects/p1"}})
	if code != http.StatusOK || len(body["repos"].([]any)) != 1 {
		t.Fatalf("logical path: %d %v", code, body)
	}
	// A leading slash (some callers send one) still works.
	code, body = gitGet(t, "alice", url.Values{"workspace_path": {"/Chats/Code/projects/p1"}})
	if code != http.StatusOK || len(body["repos"].([]any)) != 1 {
		t.Fatalf("leading slash: %d %v", code, body)
	}
	// Bob's logical path is Bob's own tree, which holds nothing.
	code, body = gitGet(t, "bob", url.Values{"workspace_path": {"Chats/Code/projects/p1"}})
	if code != http.StatusOK || len(body["repos"].([]any)) != 0 {
		t.Fatalf("bob's own tree: %d %v", code, body)
	}
}

func TestWorkspaceGitFindsReposTwoFoldersDeepButNotInDependencies(t *testing.T) {
	docs, _ := gitTestEnv(t)
	project := filepath.Join(docs, filepath.FromSlash(gitTestProject))
	deep := filepath.Join(project, "code", "service")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, deep, "init", "-q")
	hidden := filepath.Join(project, "node_modules", "pkg")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, hidden, "init", "-q")
	_, body := gitGet(t, "alice", url.Values{"workspace_path": {gitTestProject}})
	roots := map[string]bool{}
	for _, r := range body["repos"].([]any) {
		roots[r.(map[string]any)["root"].(string)] = true
	}
	if !roots["app"] || !roots["code/service"] || roots["node_modules/pkg"] {
		t.Fatalf("repos: %v", roots)
	}
}

// The platform's private .sandbox-cache folder never shows up as a change, is never walked into (an unreadable folder inside it must not warn), and
// "stage everything" does not add it.
func TestWorkspaceGitIgnoresPlatformPrivateFolder(t *testing.T) {
	_, repo := gitTestEnv(t)
	private := filepath.Join(repo, ".sandbox-cache", "cli-home", "muse-cli", "tmp", "muse-workspace-probe-abc")
	if err := os.MkdirAll(private, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(private, "note.txt"), "x\n")
	writeFile(t, filepath.Join(repo, ".sandbox-cache", "home.txt"), "x\n")
	if err := os.Chmod(private, 0); err == nil {
		t.Cleanup(func() { _ = os.Chmod(private, 0o755) })
	}
	status, err := workspaceGitStatus(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range status.Files {
		if strings.Contains(f.Path, ".sandbox-cache") {
			t.Fatalf("platform folder listed as a change: %+v", f)
		}
	}
	if out, err := workspaceGitRunWrite(context.Background(), repo, nil, "add", "-A"); err != nil || strings.Contains(out, "warning") {
		t.Fatalf("stage everything: %v %q", err, out)
	}
	staged, _ := workspaceGitRunWrite(context.Background(), repo, nil, "diff", "--cached", "--name-only")
	if strings.Contains(staged, ".sandbox-cache") {
		t.Fatalf("stage everything added the platform folder: %q", staged)
	}
	if !strings.Contains(staged, "loose.txt") {
		t.Fatalf("stage everything must still add real changes: %q", staged)
	}
}
