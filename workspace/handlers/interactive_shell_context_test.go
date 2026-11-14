package handlers

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// PLAT-746-1005: actual isolated Bash and tmux, never the app's terminal.
// Protect redirects and history omissions from wrong command attribution.
func TestInteractiveShellContextCommandFencesAndTmuxTransport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed Bash terminal is not available")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, script := range []string{"probe-history.py", "probe-tmux.py"} {
		command := exec.Command("python3", filepath.Join(root, "scripts", "fixtures", "chat-context", script))
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
	}
}
