package knowledgebase

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitStep(t *testing.T, s *Service, p Principal, args map[string]any) (any, error) {
	t.Helper()
	return s.RunGit(t.Context(), p, args, func(ctx context.Context, dir string) (any, error) {
		var cmd []string
		switch stringArg(args, "op") {
		case "status", "pull", "push":
			cmd = []string{"status", "--porcelain"}
		case "stage":
			cmd = []string{"add", "-A"}
		case "commit":
			cmd = []string{"commit", "-m", "Update knowledge"}
		case "checkout":
			cmd = []string{"switch", stringArg(args, "branch")}
			if remote, _ := args["remote"].(bool); remote {
				cmd = []string{"switch", "--track", stringArg(args, "branch")}
			}
		case "stash":
			cmd = []string{"stash", "push", "--include-untracked"}
		case "stash_pop":
			cmd = []string{"stash", "pop"}
		default:
			t.Fatal("unknown test action")
		}
		return gitWorkspaceRun(ctx, dir, cmd...)
	})
}
func gitOK(t *testing.T, s *Service, p Principal, op, id string, extra map[string]any) any {
	t.Helper()
	v, err := gitStep(t, s, p, merge(extra, map[string]any{"op": op, "request_id": id}))
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	return v
}
func gitCommandTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false"}, args...)...)
	cmd.Env = append(gitEnvironment(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@localhost", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@localhost")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func gitSource(t *testing.T, s *Service) string {
	t.Helper()
	dir := t.TempDir()
	gitCommandTest(t, dir, "init", "--initial-branch=main")
	gitCommandTest(t, dir, "remote", "add", "origin", s.cfg.BackupRemote)
	os.MkdirAll(filepath.Join(dir, "Payments"), 0700)
	os.WriteFile(filepath.Join(dir, "Payments", "guide.md"), []byte("main\n"), 0600)
	gitCommandTest(t, dir, "add", ".")
	gitCommandTest(t, dir, "commit", "-m", "Main knowledge")
	gitCommandTest(t, dir, "push", "origin", "main")
	return dir
}
func TestGitPullBranchesStashAndLivePermissions(t *testing.T) {
	s, a, p := fixture(t, true)
	source := gitSource(t, s)
	gitOK(t, s, a, "pull", "initial", nil)
	grant(t, s, a, "priya", "Payments", "Reader", "reader")
	got := call(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	entry := got["entry"].(map[string]any)
	id := entry["entry_id"]
	if got["content"] != "main\n" || entry["type"] != "note" {
		t.Fatal(got)
	}
	gitCommandTest(t, source, "switch", "-c", "release")
	os.WriteFile(filepath.Join(source, "Payments", "guide.md"), []byte("release\n"), 0600)
	os.WriteFile(filepath.Join(source, "Payments", "new.md"), []byte("new\n"), 0600)
	gitCommandTest(t, source, "add", ".")
	gitCommandTest(t, source, "commit", "-m", "Release knowledge")
	gitCommandTest(t, source, "push", "origin", "release")
	gitOK(t, s, a, "pull", "fetch-branches", nil)
	gitOK(t, s, a, "checkout", "release", map[string]any{"branch": "origin/release", "remote": true})
	got = call(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	entry = got["entry"].(map[string]any)
	if got["content"] != "release\n" || entry["entry_id"] != id {
		t.Fatal("branch lost content, identity, or access", got)
	}
	call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": id, "expected_version": entry["version"], "content": "unpublished\n", "request_id": "edit"})
	_, err := gitStep(t, s, a, map[string]any{"op": "checkout", "branch": "main", "request_id": "dirty"})
	if domain, ok := err.(*Error); !ok || domain.Code != "GIT_DIRTY" {
		t.Fatalf("dirty switch: %v", err)
	}
	gitOK(t, s, a, "stash", "stash", nil)
	got = call(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	if got["content"] != "release\n" {
		t.Fatal(got)
	}
	gitOK(t, s, a, "stash_pop", "restore", nil)
	got = call(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	if got["content"] != "unpublished\n" {
		t.Fatal(got)
	}
	gitOK(t, s, a, "stage", "stage", nil)
	gitOK(t, s, a, "commit", "commit", nil)
	gitOK(t, s, a, "push", "push", nil)
	gitOK(t, s, a, "push", "push", nil)
	gitOK(t, s, a, "checkout", "main", map[string]any{"branch": "main"})
	got = call(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	if got["content"] != "main\n" {
		t.Fatal(got)
	}
	code(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/new.md"}, "NOT_FOUND")
}
func TestGitRejectsInvalidTreesAtomically(t *testing.T) {
	s, a, _ := fixture(t, true)
	source := gitSource(t, s)
	gitOK(t, s, a, "pull", "safe", nil)
	before, _ := s.readGitWorkspace()
	value := call(t, s, a, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	os.WriteFile(filepath.Join(source, "Payments", "guide.md"), []byte("would overwrite\n"), 0600)
	os.Symlink("/etc/passwd", filepath.Join(source, "unsafe.md"))
	gitCommandTest(t, source, "add", ".")
	gitCommandTest(t, source, "commit", "-m", "Invalid tree")
	gitCommandTest(t, source, "push", "origin", "main")
	_, err := gitStep(t, s, a, map[string]any{"op": "pull", "request_id": "invalid"})
	if err == nil {
		t.Fatal("accepted link")
	}
	after, _ := s.readGitWorkspace()
	got := call(t, s, a, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	if after != before || got["content"] != value["content"] || got["version"] != value["version"] {
		t.Fatal("failed pull changed live state")
	}
	for _, files := range []map[string][]byte{{"a.md": {}, "A.md": {}}, {"folder/x.md": {}, "Folder/y.md": {}}} {
		if validateGitPaths(files) == nil {
			t.Fatal("accepted case collision")
		}
	}
}
func TestGitRequiresWholeRepositoryAuthority(t *testing.T) {
	s, a, p := fixture(t, true)
	folder(t, s, a, "", "Payments")
	grant(t, s, a, "priya", "Payments", "Editor", "partial")
	for _, op := range []string{"status", "pull", "push", "checkout"} {
		called := false
		_, err := s.RunGit(t.Context(), p, map[string]any{"op": op, "request_id": op}, func(context.Context, string) (any, error) { called = true; return nil, nil })
		if err == nil || called {
			t.Fatalf("scoped editor reached repo: %s %v", op, err)
		}
	}
	caps := []Cap{{FolderPath: "", Role: "editor"}}
	a.Caps = &caps
	_, err := gitStep(t, s, a, map[string]any{"op": "pull", "request_id": "capped"})
	if err == nil {
		t.Fatal("capped token mutated repo")
	}
}

func TestGitPushRecoversDeliveryIntentAndPreservesStagedSnapshot(t *testing.T) {
	s, a, p := fixture(t, true)
	source := gitSource(t, s)
	gitOK(t, s, a, "pull", "pull", nil)
	grant(t, s, a, "priya", "Payments", "Reader", "read")
	read := call(t, s, a, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	e := read["entry"].(map[string]any)
	saved := call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "staged\n", "request_id": "stage-content"})
	gitOK(t, s, a, "stage", "stage", nil)
	call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": saved["version"], "content": "newer live\n", "request_id": "newer-content"})
	gitOK(t, s, a, "commit", "commit", nil)
	interrupted := a
	interrupted.Recheck = func(context.Context) error {
		if _, err := os.Stat(s.gitPushIntentPath()); err == nil {
			return kbErr("FORBIDDEN", "Connection revoked before transport.")
		}
		return nil
	}
	_, err := gitStep(t, s, interrupted, map[string]any{"op": "push", "request_id": "push"})
	if err == nil {
		t.Fatal("expected interrupted push")
	}
	intent, err := s.readGitPushIntent()
	if err != nil || intent == nil {
		t.Fatal("lost durable intent", err)
	}
	// Live writes remain independent while delivery is pending; reconciliation
	// must never journal an older registry/content snapshot back over them.
	current := call(t, s, a, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	call(t, s, a, "update_knowledgebase", map[string]any{"path": "Payments/guide.md", "expected_version": current["version"], "content": "written during pending push\n", "request_id": "while-pending"})
	// Simulate remote acceptance followed by process death before local recording.
	dir := filepath.Join(s.private, intent.Directory)
	transportCtx, err := s.withBackupCredentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gitWorkspaceRun(transportCtx, dir, "push", "origin", intent.Head+":refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	_, err = gitStep(t, s, a, map[string]any{"op": "checkout", "branch": "main", "request_id": "blocked"})
	if domain, ok := err.(*Error); !ok || domain.Code != "BACKUP_OUTCOME_UNKNOWN" {
		t.Fatal("unknown delivery did not block replacement", err)
	}
	gitOK(t, s, a, "push", "push", nil)
	gitOK(t, s, a, "push", "push", nil)
	if intent, err = s.readGitPushIntent(); err != nil || intent != nil {
		t.Fatal("intent not reconciled", err)
	}
	value := call(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/guide.md"})
	if value["content"] != "written during pending push\n" {
		t.Fatal("push overwrote unstaged knowledge", value)
	}
	gitCommandTest(t, source, "fetch", "origin", "main")
	backed := gitCommandTest(t, source, "show", "origin/main:Payments/guide.md")
	if backed != "staged" {
		t.Fatal("commit lost staged snapshot", backed)
	}
}
