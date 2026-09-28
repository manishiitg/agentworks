//go:build linux

package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// Each workflow, Crew or Code project has its own managed browser. A
// sandboxed command may drive and read only its own: another owner's browser
// socket and Chrome profile are out of reach. Opt-in: needs agent-browser and
// AGENT_BROWSER_EXECUTABLE_PATH (the managed Chrome wrapper). Uses a
// throwaway profile root.
func TestBrowserScopedToOwnerE2E(t *testing.T) {
	if os.Getenv("AGENTWORKS_PRIVATE_TMP_BROWSER_E2E") != "1" {
		t.Skip("set AGENTWORKS_PRIVATE_TMP_BROWSER_E2E=1 to run")
	}
	root := t.TempDir()
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", filepath.Join(root, "profile"))
	sessionA := "workflow-aaaaaaaaaaaaaaa1--browser"
	sessionB := "workflow-bbbbbbbbbbbbbbb2--browser"
	dirA := filepath.Join(root, "Workflow", "a")
	dirB := filepath.Join(root, "Workflow", "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := func(dir, session, script string) string {
		t.Helper()
		iso := &Isolator{ReadPaths: []string{dir}, WritePaths: []string{dir}, WorkDir: dir, BaseDir: root, BrowserSession: session}
		cmd, cleanup, err := iso.ExecuteIsolated(context.Background(), script, nil)
		if err != nil {
			t.Fatalf("sandbox: %v", err)
		}
		defer cleanup()
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	argsA := `--session ` + sessionA + ` --profile ` + browserconfig.ProfilePathForSession(sessionA) + ` --idle-timeout 0 --args --no-sandbox,--disable-gpu`
	defer run(dirA, sessionA, `agent-browser `+argsA+` close >/dev/null 2>&1; true`)

	out := run(dirA, sessionA, `echo "SOCKDIR=$AGENT_BROWSER_SOCKET_DIR"
agent-browser `+argsA+` open "data:text/html,<title>owner-a</title>" >/dev/null 2>&1
echo "TITLE=$(agent-browser `+argsA+` get title 2>&1)"`)
	t.Logf("A:\n%s", out)
	if !strings.Contains(out, "TITLE=owner-a") || !strings.Contains(out, "SOCKDIR="+browserconfig.SocketDirForSession(sessionA)) {
		t.Fatal("A could not use its own browser in its own socket folder")
	}

	profileA := browserconfig.ProfilePathForSession(sessionA)
	out = run(dirB, sessionB, `ls `+browserconfig.SocketRoot+`/o/ 2>/dev/null | grep -q a && echo LISTED_A
ls `+browserconfig.SocketDirForSession(sessionA)+` >/dev/null 2>&1 && echo READ_A_SOCKETS
AGENT_BROWSER_SOCKET_DIR=`+browserconfig.SocketDirForSession(sessionA)+` agent-browser --session `+sessionA+` get title 2>&1 | grep -q owner-a && echo DROVE_A
ls "`+profileA+`" >/dev/null 2>&1 && echo READ_A_PROFILE
echo "B_SOCKDIR=$AGENT_BROWSER_SOCKET_DIR"
true`)
	t.Logf("B:\n%s", out)
	for _, bad := range []string{"LISTED_A", "READ_A_SOCKETS", "DROVE_A", "READ_A_PROFILE"} {
		if strings.Contains(out, bad) {
			t.Errorf("B reached A's browser: %s", bad)
		}
	}
	if !strings.Contains(out, "B_SOCKDIR="+browserconfig.SocketDirForSession(sessionB)) {
		t.Errorf("B not pointed at its own socket folder")
	}
}
