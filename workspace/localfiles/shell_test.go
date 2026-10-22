package localfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func shellFixture(t *testing.T) (*Executor, Grant) {
	t.Helper()
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

// Commands run as the person: in the granted folder, with their environment (not the CLI's own AGENTWORKS_ variables), and with
// no sandbox, so a program that needs files or logins outside the folder (a cloud CLI reading its own settings) works.
func TestLocalShellRunsAsThePerson(t *testing.T) {
	e, g := shellFixture(t)
	t.Setenv("LOCAL_EXECUTOR_TEST_TOOL_SETTING", "from-the-persons-environment")
	t.Setenv("AGENTWORKS_TEST_CLI_TOKEN", "the-cli-login")
	outside := filepath.Join(filepath.Dir(g.Root), "outside")
	if err := os.WriteFile(outside, []byte("a file outside the shared folder"), 0600); err != nil {
		t.Fatal(err)
	}
	got := e.Execute(t.Context(), shellRequest("pwd; printf '%s|%s|' \"${LOCAL_EXECUTOR_TEST_TOOL_SETTING-unset}\" \"${AGENTWORKS_TEST_CLI_TOKEN-unset}\"; cat ../outside", "as-the-person"))
	if got.Status != 200 || got.Shell == nil || got.Shell.ExitCode != 0 {
		t.Fatalf("command failed: %+v %+v", got, got.Shell)
	}
	canonical, err := filepath.EvalSymlinks(g.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{canonical, "from-the-persons-environment|unset|", "a file outside the shared folder"} {
		if !strings.Contains(got.Shell.Stdout, want) {
			t.Fatalf("output lacks %q: %q", want, got.Shell.Stdout)
		}
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
