package agycli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Force load(A), load(B), paste(A), paste(B) on an isolated real tmux server.
// An unnamed buffer crosses the prompts; per-session send locks cannot help.
func TestAgyPasteIsolatedAcrossConcurrentSessions(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux required")
	}
	socket := fmt.Sprintf("agy-paste-qa-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, realTmux, append([]string{"-L", socket, "-f", "/dev/null"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return string(out)
	}
	defer func() { _ = exec.Command(realTmux, "-L", socket, "kill-server").Run() }()
	for _, name := range []string{"a", "b"} {
		run("new-session", "-d", "-s", name, "-x", "200", "-y", "50", "cat")
	}
	dir := t.TempDir()
	// Forward every command to the real isolated server; only scheduling is
	// controlled, so the production paste and echo check run unchanged.
	shim := `#!/bin/sh
command=$1
target=
previous=
for arg do
  if [ "$previous" = -t ]; then target=$arg; fi
  previous=$arg
done
wait_for() { while [ ! -f "$AGY_PASTE_GATE/$1" ]; do sleep 0.01; done; }
if [ "$command" = load-buffer ] && [ "$target" = b ]; then wait_for loaded-a; fi
if [ "$command" = paste-buffer ] && [ "$target" = b ]; then wait_for pasted-a; fi
"$AGY_PASTE_TMUX" -L "$AGY_PASTE_SOCKET" "$@"
status=$?
if [ "$status" != 0 ]; then exit "$status"; fi
if [ "$command" = load-buffer ]; then
  touch "$AGY_PASTE_GATE/loaded-$target"
  if [ "$target" = a ]; then wait_for loaded-b; fi
fi
if [ "$command" = paste-buffer ]; then touch "$AGY_PASTE_GATE/pasted-$target"; fi
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGY_PASTE_GATE", dir)
	t.Setenv("AGY_PASTE_TMUX", realTmux)
	t.Setenv("AGY_PASTE_SOCKET", socket)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	results := make(chan error, 2)
	for _, name := range []string{"a", "b"} {
		go func() { results <- agyPasteToSidecar(ctx, name, "QA_ONLY_"+name+"\nsecond line") }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("paste: %v", err)
		}
	}
	for _, name := range []string{"a", "b"} {
		pane := run("capture-pane", "-p", "-J", "-t", name)
		other := "b"
		if name == "b" {
			other = "a"
		}
		if !strings.Contains(pane, "QA_ONLY_"+name) || strings.Contains(pane, "QA_ONLY_"+other) {
			t.Fatalf("session %s received crossed content: %q", name, pane)
		}
	}
	if buffers := strings.TrimSpace(run("list-buffers")); buffers != "" {
		t.Fatal("paste left a prompt buffer behind")
	}
}
