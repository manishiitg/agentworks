package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestServerTmuxStartupClearsParentContext(t *testing.T) {
	t.Setenv("TMUX", "/outer/socket,123,0")
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("TMUX_TMPDIR", "/private/server")
	if !clearInheritedTmuxEnvironment() {
		t.Fatal("inherited tmux context was not detected")
	}
	for _, key := range []string{"TMUX", "TMUX_PANE"} {
		if _, ok := os.LookupEnv(key); ok {
			t.Fatalf("%s still selects the enclosing terminal", key)
		}
	}
	if got := os.Getenv("TMUX_TMPDIR"); got != "/private/server" {
		t.Fatalf("private socket directory changed: %q", got)
	}
	if clearInheritedTmuxEnvironment() {
		t.Fatal("a second normalization should be a no-op")
	}
}

// The child is a fresh backend process with the enclosing terminal's TMUX and
// its own private TMUX_TMPDIR. Launch like Muse's adapter (bare new-session,
// inherited environment), then supervise through the real server helper.
func TestServerTmuxStartupSocketHelper(t *testing.T) {
	if os.Getenv("AGENTWORKS_TEST_TMUX_STARTUP") != "1" {
		return
	}
	if !clearInheritedTmuxEnvironment() {
		t.Fatal("expected an enclosing terminal context")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	launch := exec.Command(tmux, "-f", "/dev/null", "new-session", "-d", "-s", "muse-socket-regression", "sleep 60")
	if out, err := launch.CombinedOutput(); err != nil {
		t.Fatalf("launch: %v: %s", err, out)
	}
	// Only the isolated server created by this test is ever stopped.
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", os.Getenv("AGENTWORKS_TEST_PRIVATE_SOCKET"), "kill-server").Run()
	})
	out, err := runTerminalTmuxOutputCommand(context.Background(), "display-message", "-p", "-t", "muse-socket-regression", "#{socket_path}")
	if err != nil {
		t.Fatalf("supervisor cannot find adapter-launched pane: %v", err)
	}
	got, err := os.Stat(strings.TrimSpace(out))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(os.Getenv("AGENTWORKS_TEST_PRIVATE_SOCKET"))
	if err != nil || !os.SameFile(got, want) {
		t.Fatalf("launch escaped the private socket: %q (%v)", out, err)
	}
	if err := exec.Command(tmux, "-S", os.Getenv("AGENTWORKS_TEST_OUTER_SOCKET"), "has-session", "-t", "muse-socket-regression").Run(); err == nil {
		t.Fatal("adapter pane leaked into the enclosing terminal's server")
	}
}

func TestServerTmuxStartupKeepsNestedLaunchAndSupervisionTogether(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux socket regression requires Unix")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required for the startup socket regression")
	}
	// Keep Unix socket paths below their platform length limit.
	dir, err := os.MkdirTemp("/tmp", "aw-ts-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	outer := filepath.Join(dir, "outer")
	start := exec.Command(tmux, "-f", "/dev/null", "-S", outer, "new-session", "-d", "-s", "enclosing-terminal", "sleep 60")
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("outer terminal: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(tmux, "-S", outer, "kill-server").Run() })
	out, err := exec.Command(tmux, "-S", outer, "display-message", "-p", "#{socket_path},#{pid},#{session_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	privateDir := filepath.Join(dir, "private")
	if err := os.Mkdir(privateDir, 0700); err != nil {
		t.Fatal(err)
	}
	uid := strconv.Itoa(os.Getuid())
	privateSocket := filepath.Join(privateDir, "tmux-"+uid, "default")
	t.Cleanup(func() { _ = exec.Command(tmux, "-S", privateSocket, "kill-server").Run() })
	t.Setenv("TMUX", strings.TrimSpace(string(out)))
	t.Setenv("TMUX_PANE", "%0")
	t.Setenv("TMUX_TMPDIR", privateDir)
	t.Setenv("AGENTWORKS_TEST_TMUX_STARTUP", "1")
	t.Setenv("AGENTWORKS_TEST_PRIVATE_SOCKET", privateSocket)
	t.Setenv("AGENTWORKS_TEST_OUTER_SOCKET", outer)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, exe, "-test.run=^TestServerTmuxStartupSocketHelper$", "-test.v")
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("nested backend regression: %v\n%s", err, out)
	}
	if err := exec.Command(tmux, "-S", outer, "has-session", "-t", "enclosing-terminal").Run(); err != nil {
		t.Fatal("test affected its enclosing terminal")
	}
}
