//go:build windows

package localfiles

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// On Windows commands run through Git Bash with the user's permissions (no sandbox): the command sees its working folder,
// reports its exit code, and gets only the filtered environment (system variables, no secrets).
func TestWindowsShellRunsThroughGitBash(t *testing.T) {
	if findGitBash() == "" {
		t.Fatal("Git for Windows is expected on this machine")
	}
	t.Setenv("AGENTWORKS_TOKEN", "must-not-reach-commands")
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, cleanup, err := windowsShellCommand(ctx, dir, `pwd -W; echo "token=[$AGENTWORKS_TOKEN]"; echo "root=[$SYSTEMROOT]"; exit 3`)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	cmd.Env = shellEnvironment(cmd.Env)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	kill := configureShellProcess(cmd)
	defer kill()
	err = runShellCommand(cmd)
	var exit *exec.ExitError
	if err == nil || !asExit(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("expected exit code 3, got %v\n%s", err, out.String())
	}
	got := strings.ReplaceAll(strings.ToLower(out.String()), "\\", "/")
	if !strings.Contains(got, strings.ToLower(strings.ReplaceAll(dir, "\\", "/"))) {
		t.Fatalf("the command must run in its working folder %s:\n%s", dir, out.String())
	}
	if strings.Contains(out.String(), "must-not-reach-commands") || !strings.Contains(out.String(), "token=[]") {
		t.Fatalf("secrets must not reach commands:\n%s", out.String())
	}
	if strings.Contains(out.String(), "root=[]") {
		t.Fatalf("system variables must reach commands:\n%s", out.String())
	}
}

func asExit(err error, target **exec.ExitError) bool {
	exit, ok := err.(*exec.ExitError)
	if ok {
		*target = exit
	}
	return ok
}

func windowsExecutor(t *testing.T) (*Executor, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "project")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{root, filepath.Join(root, "blocked"), outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{filepath.Join(root, "blocked", "secret.txt"): "blocked-secret", filepath.Join(outside, "secret.txt"): "outside-secret", filepath.Join(root, "ok.txt"): "fine"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	grant := Grant{Resource: Resource{ID: "project", Writable: true, Shell: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: []string{"blocked"}}}, Root: root, State: filepath.Join(base, "state")}
	e, err := Open("laptop", []Grant{grant})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, root, outside
}

func run(e *Executor, r Request) Response {
	r.ResourceID, r.Identity = "project", wf.EditIdentity{UserID: "owner"}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return e.Execute(ctx, r)
}

// The whole local path on Windows: write a file, read it back, run a command that writes another, list the folder.
func TestWindowsExecutorEditsFilesAndRunsCommands(t *testing.T) {
	e, root, _ := windowsExecutor(t)
	if resp := run(e, Request{ID: "1", Operation: "write", Path: "sub/new.txt", Content: "hello", RequestID: "w1", ExpectedRevision: wf.MissingRevision}); resp.Status != 200 {
		t.Fatalf("write: %d %s", resp.Status, resp.Error)
	}
	if resp := run(e, Request{ID: "2", Operation: "read", Path: "sub/new.txt"}); resp.Status != 200 || resp.File == nil || resp.File.Content != "hello" {
		t.Fatalf("read back: %d %s %+v", resp.Status, resp.Error, resp.File)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	shell, err := e.shell(ctx, e.grants["project"], Request{ID: "3", ResourceID: "project", Operation: "shell", Path: ".", Command: `cat ok.txt; echo ran > from-command.txt`, RequestID: "s1", Identity: wf.EditIdentity{UserID: "owner"}})
	if err != nil || shell == nil || shell.ExitCode != 0 || !strings.Contains(shell.Stdout, "fine") {
		t.Fatalf("shell: %v %+v", err, shell)
	}
	if data, err := os.ReadFile(filepath.Join(root, "from-command.txt")); err != nil || !strings.Contains(string(data), "ran") {
		t.Fatalf("the command must have written its file: %v %q", err, data)
	}
	if resp := run(e, Request{ID: "4", Operation: "list", Path: "."}); resp.Status != 200 || len(resp.Entries) == 0 {
		t.Fatalf("list: %d %s", resp.Status, resp.Error)
	}
}

// Windows spells paths in more ways than Unix: every one of these must be refused by the file tools, and none may leak content.
func TestWindowsFileToolsRefuseEscapesAndBlockedPaths(t *testing.T) {
	e, root, outside := windowsExecutor(t)
	cases := []string{
		`..\outside\secret.txt`, `../outside/secret.txt`, `sub\..\..\outside\secret.txt`,
		filepath.Join(outside, "secret.txt"), `C:\Windows\win.ini`, `\\?\C:\Windows\win.ini`, `C:win.ini`, `\Windows\win.ini`,
		`blocked\secret.txt`, `BLOCKED/secret.txt`, `Blocked\..\blocked\secret.txt`, `blocked./secret.txt`, `blocked /secret.txt`, `BLOCKE~1\secret.txt`,
		`ok.txt::$DATA`, `ok.txt:stream`, `con`, `nul`, `aux.txt`, `COM1`,
	}
	for _, path := range cases {
		resp := run(e, Request{ID: "x", Operation: "read", Path: path})
		leaked := resp.File != nil && (strings.Contains(resp.File.Content, "secret"))
		if leaked || (resp.Status == 200 && resp.File != nil && resp.File.Exists && path != "ok.txt") {
			t.Errorf("read %q must be refused: status %d, file %+v", path, resp.Status, resp.File)
		}
		if resp := run(e, Request{ID: "y", Operation: "write", Path: path, Content: "x", RequestID: "w-" + path, ExpectedRevision: wf.MissingRevision}); resp.Status == 200 && !strings.HasPrefix(path, "ok.txt") {
			t.Errorf("write %q must be refused, got %d", path, resp.Status)
		}
	}
	_ = root
}

// A junction (the Windows directory link that needs no privileges) pointing out of the folder must not be followed by the file tools.
func TestWindowsFileToolsDoNotFollowAJunctionOutOfTheFolder(t *testing.T) {
	e, root, outside := windowsExecutor(t)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "link"), outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, out)
	}
	for _, path := range []string{`link/secret.txt`, `link\secret.txt`} {
		resp := run(e, Request{ID: "j", Operation: "read", Path: path})
		if resp.File != nil && strings.Contains(resp.File.Content, "outside-secret") {
			t.Errorf("read through the junction leaked content (%q): %d", path, resp.Status)
		}
		if w := run(e, Request{ID: "k", Operation: "write", Path: strings.ReplaceAll(path, "secret.txt", "planted.txt"), Content: "x", RequestID: "wj-" + path, ExpectedRevision: wf.MissingRevision}); w.Status == 200 {
			if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
				t.Errorf("a write through the junction planted a file outside the folder (%q)", path)
			}
		}
	}
}
