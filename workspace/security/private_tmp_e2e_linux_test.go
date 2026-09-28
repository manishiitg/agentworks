//go:build linux

package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runs the real Landlock launcher (AGENTWORKS_LANDLOCK_RUNNER) on a Linux host:
// two Crews of different users must not see each other's /tmp, home or
// scratch. Opt-in because it needs the launcher binary and a Landlock kernel.
func TestPrivateTmpBetweenCrewsE2E(t *testing.T) {
	if os.Getenv("AGENTWORKS_PRIVATE_TMP_E2E") != "1" {
		t.Skip("set AGENTWORKS_PRIVATE_TMP_E2E=1 and AGENTWORKS_LANDLOCK_RUNNER to run")
	}
	base := t.TempDir()
	crewA := filepath.Join(base, "_users", "alice", "Chats", "Work", "projects", "a")
	crewB := filepath.Join(base, "_users", "bob", "Chats", "Work", "projects", "b")
	for _, dir := range []string{crewA, crewB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	leak := filepath.Join("/tmp", "agentworks-private-tmp-e2e-"+filepath.Base(base))
	defer os.Remove(leak)
	run := func(crew, script string) (string, error) {
		t.Helper()
		iso := &Isolator{ReadPaths: []string{crew}, WritePaths: []string{crew}, WorkDir: crew, BaseDir: base}
		cmd, cleanup, err := iso.ExecuteIsolated(context.Background(), script, nil)
		if err != nil {
			t.Fatalf("sandbox: %v", err)
		}
		defer cleanup()
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, _ := run(crewA, `echo "HOME=$HOME TMPDIR=$TMPDIR"
git config --global user.name alice && echo GIT_OK
echo token-a > "$HOME/.git-credentials" && echo HOME_WRITE_OK
python3 -c 'import tempfile; f=tempfile.NamedTemporaryFile(); print("PY_TMP_OK", f.name)'
echo leak > `+leak+` && echo TMP_WRITE_ALLOWED
ls /tmp >/dev/null 2>&1 && echo TMP_LIST_ALLOWED
true`)
	t.Logf("crew A:\n%s", out)
	for _, want := range []string{"HOME=" + filepath.Join(crewA, SandboxPersistentDirName, "home"), "GIT_OK", "HOME_WRITE_OK", "PY_TMP_OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("crew A missing %q", want)
		}
	}
	for _, bad := range []string{"TMP_WRITE_ALLOWED", "TMP_LIST_ALLOWED"} {
		if strings.Contains(out, bad) {
			t.Errorf("crew A: %s", bad)
		}
	}

	// A file someone left in /tmp outside the sandbox is out of reach too.
	if err := os.WriteFile(leak, []byte("planted"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = run(crewB, `cat `+leak+` 2>/dev/null && echo READ_PLANTED
cat "`+filepath.Join(crewA, SandboxPersistentDirName, "home", ".git-credentials")+`" 2>/dev/null && echo READ_OTHER_HOME
cat /tmp/.git-credentials 2>/dev/null && echo READ_SHARED_CREDENTIALS
echo "HOME=$HOME"
true`)
	t.Logf("crew B:\n%s", out)
	for _, bad := range []string{"READ_PLANTED", "READ_OTHER_HOME", "READ_SHARED_CREDENTIALS", "token-a"} {
		if strings.Contains(out, bad) {
			t.Errorf("crew B could see another agent's data: %s", bad)
		}
	}
	if !strings.Contains(out, "HOME="+filepath.Join(crewB, SandboxPersistentDirName, "home")) {
		t.Errorf("crew B HOME wrong")
	}
}
