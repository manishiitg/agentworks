package localfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	g := Grant{Resource: Resource{ID: "project", Writable: true, Shell: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: []string{"blocked"}, ReadOnlyPaths: []string{"locked"}}}, Root: root, State: filepath.Join(base, "state")}
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
	return e, g
}
func shellRequest(command, id string) Request {
	return Request{Operation: "shell", ResourceID: "project", Path: ".", Command: command, RequestID: id, Identity: wf.EditIdentity{UserID: "owner"}}
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
func TestLocalShellRequiresSeparateGrant(t *testing.T) {
	e, err := Open("laptop", []Grant{{Resource: Resource{ID: "project", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}}, Root: t.TempDir(), State: filepath.Join(t.TempDir(), "state")}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if got := e.Execute(t.Context(), shellRequest("echo unapproved", "no-grant")); got.Status != 403 {
		t.Fatalf("shell grant bypass %+v", got)
	}
	if err := (Hello{Version: Version, DeviceID: "laptop", Resources: []Resource{{ID: "project", Shell: true}}}).Validate(); err == nil {
		t.Fatal("read-only shell grant admitted")
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
