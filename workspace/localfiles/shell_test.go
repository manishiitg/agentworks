package localfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func shellFixture(t *testing.T) (*Executor, Grant) {
	t.Helper()
	capability := security.CurrentSandboxCapability()
	if !capability.Available || runtime.GOOS == "linux" && capability.Backend != "landlock" {
		t.Skip("OS sandbox unavailable")
	}
	base := t.TempDir()
	root := filepath.Join(base, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	g := Grant{Resource: Resource{ID: "project", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: []string{"blocked"}, ReadOnlyPaths: []string{"locked"}}}, Root: root, State: filepath.Join(base, "state")}
	for _, dir := range []string{"blocked", "locked"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Open("laptop", []Grant{g})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if !e.Hello.Resources[0].Shell {
		t.Fatal("writable folder did not enable shell automatically")
	}
	return e, g
}
func shellRequest(command, id string) Request {
	return Request{Operation: "shell", ResourceID: "project", Path: ".", Command: command, RequestID: id, Identity: wf.EditIdentity{UserID: "owner"}}
}

func TestLocalMacShellCannotUseDesktopCredentialOrClipboardServices(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS broker services")
	}
	e, g := shellFixture(t)
	// Use an outside fixture rather than reading any real laptop credentials.
	private := filepath.Join(filepath.Dir(g.Root), "private-home", ".ssh")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(private, "id_test")
	if err := os.WriteFile(key, []byte("fixture-not-a-real-key"), 0600); err != nil {
		t.Fatal(err)
	}
	commands := []struct{ name, command string }{
		{"desktop", "open -a Calculator"},
		{"apple-events", `osascript -e 'do shell script "id"'`},
		{"clipboard", "pbpaste >/dev/null"},
		{"outside-key", "cat '" + strings.ReplaceAll(key, "'", "'\\''") + "' >/dev/null"},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			got := e.Execute(t.Context(), shellRequest(command.command, command.name))
			if got.Status != 200 || got.Shell == nil || got.Shell.ExitCode == 0 || got.Shell.TimedOut {
				t.Fatalf("broker or outside-file access was not denied: status=%d result=%+v", got.Status, got.Shell)
			}
		})
	}
}

func TestLocalMacShellNetworkGitAndNpm(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("AGENTWORKS_TEST_LOCAL_NETWORK") != "1" {
		t.Skip("opt in to real macOS network integration with AGENTWORKS_TEST_LOCAL_NETWORK=1")
	}
	e, g := shellFixture(t)
	if err := os.WriteFile(filepath.Join(g.Root, "package.json"), []byte(`{"private":true,"scripts":{"test":"node -e \"if (!require('is-number')(42)) process.exit(1)\""}}`), 0600); err != nil {
		t.Fatal(err)
	}
	commands := []struct{ name, command string }{
		{"curl", "curl --fail --silent --show-error --max-time 15 https://example.com >/dev/null"},
		{"git", "git -c credential.helper= clone --depth=1 https://github.com/octocat/Hello-World.git remote"},
		{"npm-install", "npm install --ignore-scripts --no-audit --no-fund --save-exact is-number@7.0.0 --fetch-timeout=15000 --fetch-retries=0"},
		{"npm-test", "npm test"},
	}
	for _, command := range commands {
		got := e.Execute(t.Context(), shellRequest(command.command, command.name))
		if got.Status != 200 || got.Shell == nil || got.Shell.ExitCode != 0 || got.Shell.TimedOut {
			t.Fatalf("%s: status=%d result=%+v", command.name, got.Status, got.Shell)
		}
	}
}
func TestLocalShellCommandsResultsAndDurableRetry(t *testing.T) {
	e, g := shellFixture(t)
	r := shellRequest("printf 'local-output'; printf 'error-output' >&2; echo once >> count; exit 7", "command-1")
	got := e.Execute(t.Context(), r)
	if got.Status != 200 || got.Shell == nil || got.Shell.Stdout != "local-output" || !strings.Contains(got.Shell.Stderr, "error-output") || got.Shell.ExitCode != 7 {
		t.Fatalf("shell %+v / %+v", got, got.Shell)
	}
	restarted, err := Open("laptop", []Grant{g})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	retry := restarted.Execute(t.Context(), r)
	if retry.Status != 200 || retry.Shell == nil || *retry.Shell != *got.Shell {
		t.Fatalf("retry %+v / %+v", retry, retry.Shell)
	}
	count, err := os.ReadFile(filepath.Join(g.Root, "count"))
	if err != nil || string(count) != "once\n" {
		t.Fatalf("command reran: %s %v", count, err)
	}
	r.Command = "echo different"
	if got := e.Execute(t.Context(), r); got.Status != 409 {
		t.Fatalf("request ID reuse %+v", got)
	}
}

func TestLocalDownloadsRequireExplicitCompanionGrant(t *testing.T) {
	_, project := shellFixture(t)
	downloadsRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(downloadsRoot, "import.csv"), []byte("downloaded"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(downloadsRoot, "blocked"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloadsRoot, "blocked", "private"), []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	companion := Grant{Resource: Resource{ID: "downloads", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: []string{"blocked"}}}, Root: downloadsRoot, State: filepath.Join(t.TempDir(), "state")}
	for _, enabled := range []bool{false, true} {
		// A read-only project may still opt in to writable Downloads.
		project.Writable = false
		project.Guard.WritePaths = nil
		project.Downloads = enabled
		e, err := Open("laptop", []Grant{project, companion})
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		id := fmt.Sprintf("downloads-%v", enabled)
		got := e.Execute(t.Context(), shellRequest("cat '"+downloadsRoot+"/import.csv'", id))
		if got.Shell == nil || (got.Shell.ExitCode == 0) != enabled {
			t.Fatalf("Downloads grant %v: %+v", enabled, got.Shell)
		}
		patch := shellRequest("", id+"-patch")
		patch.Path = ""
		patch.Operation, patch.Content = "patch", "*** Begin Patch\n*** Add File: "+filepath.Join(downloadsRoot, "export.txt")+"\n+exported\n*** End Patch"
		result := e.Execute(t.Context(), patch)
		if (result.Status == 200) != enabled {
			t.Fatalf("Downloads patch %v: %+v", enabled, result)
		}
		if enabled {
			canonical, err := filepath.EvalSymlinks(downloadsRoot)
			if err != nil {
				t.Fatal(err)
			}
			patch.Content = "*** Begin Patch\n*** Add File: " + filepath.Join(canonical, "canonical.txt") + "\n+new\n*** End Patch"
			patch.RequestID = id + "-canonical-patch"
			if result := e.Execute(t.Context(), patch); result.Status != 200 {
				t.Fatalf("canonical Downloads patch: %+v", result)
			}
			patch.Content = "*** Begin Patch\n*** Add File: " + filepath.Join(downloadsRoot, "blocked", "new.txt") + "\n+hidden\n*** End Patch"
			patch.RequestID = id + "-blocked-patch"
			if result := e.Execute(t.Context(), patch); result.Status != 403 {
				t.Fatalf("Downloads patch bypassed exclusion: %+v", result)
			}
			patch.Content = "*** Begin Patch\n*** Add File: " + filepath.Join(downloadsRoot, "mixed.txt") + "\n+new\n*** Add File: untouched.txt\n+new\n*** End Patch"
			patch.RequestID = id + "-mixed-patch"
			if result := e.Execute(t.Context(), patch); result.Status != 400 {
				t.Fatalf("mixed patch accepted: %+v", result)
			}
			if _, err := os.Stat(filepath.Join(downloadsRoot, "mixed.txt")); !os.IsNotExist(err) {
				t.Fatal("mixed patch wrote before validation")
			}
			got = e.Execute(t.Context(), shellRequest(`printf updated > "$AGENTWORKS_DOWNLOADS/import.csv"`, id+"-write"))
			if got.Shell == nil || got.Shell.ExitCode != 0 {
				t.Fatalf("Downloads write: %+v", got.Shell)
			}
			if data, _ := os.ReadFile(filepath.Join(downloadsRoot, "import.csv")); string(data) != "updated" {
				t.Fatalf("Downloads not updated: %s", data)
			}
			got = e.Execute(t.Context(), shellRequest(`cat "$AGENTWORKS_DOWNLOADS/blocked/private"`, id+"-blocked"))
			if got.Shell == nil || got.Shell.ExitCode == 0 {
				t.Fatal("Downloads exclusion ignored")
			}
		}
	}
}
func TestLocalShellEnforcesFolderGuardsAndEnvironment(t *testing.T) {
	e, g := shellFixture(t)
	t.Setenv("LOCAL_EXECUTOR_TEST_SECRET", "must-not-reach-command")
	for _, dir := range []string{"blocked", "locked", "sub"} {
		if err := os.MkdirAll(filepath.Join(g.Root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"blocked/secret", "locked/source"} {
		if err := os.WriteFile(filepath.Join(g.Root, file), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(filepath.Dir(g.Root), "outside"), []byte("outside"), 0600)
	for i, command := range []string{"cat blocked/secret", "echo changed > locked/source", "cat ../outside"} {
		got := e.Execute(t.Context(), shellRequest(command, string(rune('a'+i))))
		if got.Status != 200 || got.Shell == nil || got.Shell.ExitCode == 0 {
			t.Fatalf("guard bypass: %s %+v %+v", command, got, got.Shell)
		}
	}
	got := e.Execute(t.Context(), shellRequest("printf '%s' \"${LOCAL_EXECUTOR_TEST_SECRET-unset}\"", "environment"))
	if got.Status != 200 || got.Shell == nil || got.Shell.Stdout != "unset" {
		t.Fatalf("env leak %+v %+v", got, got.Shell)
	}
	r := shellRequest("echo nested > result", "nested")
	r.Path = "sub"
	got = e.Execute(t.Context(), r)
	data, err := os.ReadFile(filepath.Join(g.Root, "sub/result"))
	if got.Status != 200 || err != nil || string(data) != "nested\n" {
		t.Fatalf("cwd %+v %s %v", got, data, err)
	}
	for _, path := range []string{"../outside", "blocked", "locked"} {
		r.Path = path
		if got := e.Execute(t.Context(), r); got.Status == 200 {
			t.Fatalf("working directory bypass %s %+v", path, got)
		}
	}
	os.Symlink(filepath.Dir(g.Root), filepath.Join(g.Root, "link"))
	r.Path = "link"
	if got := e.Execute(t.Context(), r); got.Status == 200 {
		t.Fatalf("symlink cwd %+v", got)
	}
}
func TestLocalShellTimeoutCancellationAndBoundedOutput(t *testing.T) {
	e, _ := shellFixture(t)
	r := shellRequest("sleep 10", "timeout")
	r.TimeoutSeconds = 1
	started := time.Now()
	got := e.Execute(t.Context(), r)
	if got.Status != 200 || got.Shell == nil || !got.Shell.TimedOut || time.Since(started) > 4*time.Second {
		t.Fatalf("timeout %+v %+v", got, got.Shell)
	}
	ctx, cancel := context.WithCancel(t.Context())
	timer := time.AfterFunc(300*time.Millisecond, cancel)
	defer timer.Stop()
	got = e.Execute(ctx, shellRequest("sleep 10", "cancel"))
	if got.Status != 200 || got.Shell == nil || got.Shell.ExitCode == 0 {
		t.Fatalf("cancel %+v %+v", got, got.Shell)
	}
	got = e.Execute(t.Context(), shellRequest("awk 'BEGIN {for(i=0;i<1100000;i++)printf \"x\"}'", "bounded"))
	if got.Status != 200 || got.Shell == nil || !got.Shell.Truncated || len(got.Shell.Stdout) != MaxShellOutputBytes {
		t.Fatalf("output %+v", got)
	}
}
func TestLocalShellReadOnlyFoldersCanInspectButCannotWrite(t *testing.T) {
	capability := security.CurrentSandboxCapability()
	if !capability.Available || runtime.GOOS == "linux" && capability.Backend != "landlock" {
		t.Skip("OS sandbox unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Open("laptop", []Grant{{Resource: Resource{ID: "project", Guard: wf.FolderGuard{ReadPaths: []string{"."}}}, Root: root, State: filepath.Join(t.TempDir(), "state")}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !e.Hello.Resources[0].Shell || e.Hello.Resources[0].Writable {
		t.Fatal("incorrect read-only capabilities")
	}
	got := e.Execute(t.Context(), shellRequest("cat readme", "inspect"))
	if got.Status != 200 || got.Shell == nil || got.Shell.Stdout != "original" {
		t.Fatalf("read-only inspection %+v %+v", got, got.Shell)
	}
	got = e.Execute(t.Context(), shellRequest("echo changed > readme", "write-denied"))
	if got.Status != 200 || got.Shell == nil || got.Shell.ExitCode == 0 {
		t.Fatalf("read-only command wrote %+v %+v", got, got.Shell)
	}
	data, err := os.ReadFile(filepath.Join(root, "readme"))
	if err != nil || string(data) != "original" {
		t.Fatalf("file changed %s %v", data, err)
	}
	if err := e.Hello.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestLocalShellRejectsInvalidRequests(t *testing.T) {
	for _, r := range []Request{{Command: "echo ok"}, {Command: "", RequestID: "a"}, {Command: "echo ok", RequestID: "a", TimeoutSeconds: 301}, {Command: "echo ok", RequestID: "a", TimeoutSeconds: -1}} {
		if r.ValidateShell() == nil {
			t.Fatalf("invalid request %+v", r)
		}
	}
}

func TestLocalShellUnknownOutcomeDoesNotExecuteAgain(t *testing.T) {
	e, g := shellFixture(t)
	r := shellRequest("echo once >> counter", "interrupted")
	got := e.Execute(t.Context(), r)
	if got.Status != 200 {
		t.Fatalf("initial command %+v", got)
	}
	id := sha256.Sum256([]byte(r.RequestID))
	filename := filepath.Join(g.State, "shell-runs", hex.EncodeToString(id[:])+".json")
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var record shellRecord
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.Result = nil // Simulate a crash after dispatch, before durable result storage.
	data, _ = json.Marshal(record)
	if err = os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got = e.Execute(t.Context(), r); got.Status != 409 || got.Code != "shell_outcome_unknown" {
		t.Fatalf("unknown command reran %+v", got)
	}
	data, err = os.ReadFile(filepath.Join(g.Root, "counter"))
	if err != nil || string(data) != "once\n" {
		t.Fatalf("unknown side effect repeated %s %v", data, err)
	}
}

func TestLocalDownloadsDoNotOverrideNestedReadonlyProject(t *testing.T) {
	_, project := shellFixture(t)
	downloads := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	project.Root = filepath.Join(downloads, "project")
	if err := os.Mkdir(project.Root, 0700); err != nil {
		t.Fatal(err)
	}
	project.Writable, project.Downloads = false, true
	project.Guard.WritePaths = nil
	companion := Grant{Resource: Resource{ID: "downloads", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}}, Root: downloads, State: filepath.Join(t.TempDir(), "state")}
	e, err := Open("laptop", []Grant{project, companion})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	got := e.Execute(t.Context(), shellRequest(`touch "$AGENTWORKS_DOWNLOADS/project/forbidden"`, "nested-deny"))
	if got.Shell == nil || got.Shell.ExitCode == 0 {
		t.Fatalf("companion widened project: %+v", got)
	}
	got = e.Execute(t.Context(), shellRequest(`touch "$AGENTWORKS_DOWNLOADS/allowed"`, "nested-allow"))
	if got.Shell == nil || got.Shell.ExitCode != 0 {
		t.Fatalf("Downloads write blocked: %+v", got)
	}
	patch := shellRequest("", "nested-patch")
	patch.Path, patch.Operation = "", "patch"
	patch.Content = "*** Begin Patch\n*** Add File: " + filepath.Join(project.Root, "forbidden") + "\n+new\n*** End Patch"
	if result := e.Execute(t.Context(), patch); result.Status != 403 {
		t.Fatalf("patch widened project: %+v", result)
	}
}
