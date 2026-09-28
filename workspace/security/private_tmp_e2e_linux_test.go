//go:build linux

package security

import (
	"context"
	"net"
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
	runner, err := landlockRunnerPath()
	if err != nil || !privateTmpAvailable(runner) {
		t.Fatalf("private /tmp must be available on this host (runner err=%v, detail=%q)", err, privateTmpProbe.detail)
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
	// Stands in for the shared tmux server socket /tmp/tmux-<uid>/default.
	socketPath := filepath.Join("/tmp", "agentworks-e2e-"+filepath.Base(base)+".sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
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
python3 -c 'import socket; s=socket.socket(socket.AF_UNIX); s.connect("`+socketPath+`"); print("HOST_SOCKET_REACHABLE")' 2>/dev/null
test -d /tmp/.agent-browser && echo BROWSER_SOCKET_DIR_OK
tmux -S /tmp/tmux-$(id -u)/default ls >/dev/null 2>&1 && echo HOST_TMUX_REACHABLE
true`)
	t.Logf("crew A:\n%s", out)
	for _, want := range []string{"HOME=" + filepath.Join(crewA, SandboxPersistentDirName, "home"), "GIT_OK", "HOME_WRITE_OK", "PY_TMP_OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("crew A missing %q", want)
		}
	}
	if !strings.Contains(out, "BROWSER_SOCKET_DIR_OK") {
		t.Errorf("crew A lost the browser socket folder")
	}
	// /tmp is the command's own tmpfs: writing and listing it is fine, but a
	// write must never reach the host /tmp.
	if _, err := os.Stat(leak); err == nil {
		t.Errorf("crew A's /tmp write reached the host /tmp")
	}
	for _, bad := range []string{"HOST_SOCKET_REACHABLE", "HOST_TMUX_REACHABLE"} {
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

// Chrome must still start and load a page from a sandboxed command, launched
// the way production does (a project profile under the shared-profile root,
// --no-sandbox), with and without a private /tmp. Opt-in: needs
// agent-browser and AGENT_BROWSER_EXECUTABLE_PATH (the managed wrapper).
// Uses a throwaway profile root, never the real shared one.
func TestPrivateTmpBrowserE2E(t *testing.T) {
	if os.Getenv("AGENTWORKS_PRIVATE_TMP_BROWSER_E2E") != "1" {
		t.Skip("set AGENTWORKS_PRIVATE_TMP_BROWSER_E2E=1 to run")
	}
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", profile)
	crew := filepath.Join(root, "_users", "alice", "Chats", "Work", "projects", "b")
	if err := os.MkdirAll(crew, 0o755); err != nil {
		t.Fatal(err)
	}
	projectProfile := profile + "-projects/e2e"
	if err := os.MkdirAll(projectProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	iso := &Isolator{ReadPaths: []string{crew}, WritePaths: []string{crew}, WorkDir: crew, BaseDir: root}
	script := `A="--session pt$$ --profile ` + projectProfile + ` --idle-timeout 0 --args --no-sandbox,--disable-gpu"; agent-browser $A open "data:text/html,<title>ptmp-ok</title>" 2>&1 | tail -4; echo "TITLE=$(agent-browser $A get title 2>&1)"; agent-browser $A close >/dev/null 2>&1; true`

	cmd, cleanup, err := iso.ExecuteIsolated(context.Background(), script, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, _ := cmd.CombinedOutput()
	t.Logf("browser (private /tmp=%v):\n%s", os.Getenv(privateTmpDisabledEnv) != "true", out)
	if !strings.Contains(string(out), "TITLE=ptmp-ok") {
		t.Fatal("Chrome did not load the page inside the sandbox")
	}
}
