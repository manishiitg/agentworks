//go:build linux

package security

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-872-84: a Crew CLI runs with the server's shared AI logins in its own layer. A shell it starts (CLIs exec
// /bin/sh or /bin/bash themselves) must neither read the login file nor see the login variable, and still work.
func TestWrappedShellsCannotReachTheCLIsLogins(t *testing.T) {
	runner, err := landlockRunnerPath()
	if err != nil || !landlockNamespacesAvailable() {
		t.Skip("needs the Landlock launcher with its namespaces (AGENTWORKS_LANDLOCK_RUNNER)")
	}
	work, logins := t.TempDir(), t.TempDir()
	token := filepath.Join(logins, "auth.json")
	if err := os.WriteFile(token, []byte("LOGIN-FILE-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(argv ...string) string {
		t.Helper()
		policy := LandlockPolicy{ReadPaths: []string{work, logins}, WritePaths: []string{work}, WorkDir: work, PrivateTmp: true,
			ShellWrap: &ShellWrap{Wrapper: runner, ScrubEnv: []string{"FAKE_LOGIN_TOKEN"},
				Inner: LandlockPolicy{ReadPaths: []string{work}, WritePaths: []string{work}, WorkDir: work, PrivateTmp: true}}}
		config := filepath.Join(t.TempDir(), "agentworks-landlock-test.json")
		raw, _ := json.Marshal(policy)
		if err := os.WriteFile(config, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(runner, append([]string{"--config", config, "--"}, argv...)...)
		cmd.Env = append(BuildSafeEnvironment(), "FAKE_LOGIN_TOKEN=LOGIN-ENV-SECRET")
		cmd.SysProcAttr = privateTmpSysProcAttr()
		out, _ := cmd.CombinedOutput()
		return string(out)
	}

	// The CLI itself (here: head, not a shell) reads its login.
	if got := run("/usr/bin/head", "-c", "17", token); !strings.Contains(got, "LOGIN-FILE-SECRET") {
		t.Fatalf("the CLI's own layer must read its login: %q", got)
	}
	// A shell the CLI starts, by full path and by name, and a shell inside it.
	got := run("/usr/bin/env", "/bin/sh", "-c", `cat `+token+` 2>&1; echo "ENV=$FAKE_LOGIN_TOKEN"; bash -c 'echo nested-ok; cat `+token+` 2>&1'; echo done > ok.txt`)
	if strings.Contains(got, "LOGIN-FILE-SECRET") {
		t.Errorf("a wrapped shell read the login file: %s", got)
	}
	if strings.Contains(got, "LOGIN-ENV-SECRET") {
		t.Errorf("a wrapped shell saw the login variable: %s", got)
	}
	if !strings.Contains(got, "nested-ok") {
		t.Errorf("a shell inside a wrapped shell must still run: %s", got)
	}
	if raw, _ := os.ReadFile(filepath.Join(work, "ok.txt")); strings.TrimSpace(string(raw)) != "done" {
		t.Errorf("a wrapped shell must still write the project: %s", got)
	}
}
