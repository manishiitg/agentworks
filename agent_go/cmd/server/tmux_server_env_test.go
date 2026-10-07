package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-663: a tmux server started with the service's environment (as servers started before the launch fix
// were) loses the service-only variables to the startup check, and keeps everything else. Real tmux.
func TestTmuxServerEnvCheckRemovesServiceTokens(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "p663") // a unix socket path must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	start := exec.Command(tmux, "-f", "/dev/null", "-S", sock, "new-session", "-d", "-s", "plat663", "sleep 30")
	start.Env = append(os.Environ(), "WORKSPACE_API_TOKEN=x", "SUPABASE_SERVICE_ROLE_KEY=x", "PLAT663_ORDINARY=kept")
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(tmux, "-S", sock, "kill-server").Run() })
	run := func(ctx context.Context, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, tmux, append([]string{"-S", sock}, args...)...).Output()
		return string(out), err
	}

	found, left, err := scrubTmuxServerEnvironment(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found, ",") != "SUPABASE_SERVICE_ROLE_KEY,WORKSPACE_API_TOKEN" || len(left) != 0 {
		t.Fatalf("found %v, left %v", found, left)
	}
	env, _ := run(context.Background(), "show-environment", "-g")
	if strings.Contains(env, "WORKSPACE_API_TOKEN=") || !strings.Contains(env, "PLAT663_ORDINARY=kept") {
		t.Fatalf("tmux global environment after the check is wrong (names: %v)", envNames(env))
	}
}

func envNames(env string) []string {
	var names []string
	for _, line := range strings.Split(env, "\n") {
		if name, _, ok := strings.Cut(line, "="); ok {
			names = append(names, name)
		}
	}
	return names
}
