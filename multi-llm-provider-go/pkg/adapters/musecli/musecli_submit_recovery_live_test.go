package musecli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise real Muse's editor and native journal without a model or account.
// The proxy turns the first submit into newlines, matching the stuck draft
// observed after a screen-changing Enter that never produced native intake.
func TestMuseCLIRealUnsentDraftEnterRecovery(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("requires -coding-cli-p0-live and MUSE_CLI_TEST_BINARY")
	}
	binary := os.Getenv("MUSE_CLI_TEST_BINARY")
	if binary == "" {
		t.Skip("set MUSE_CLI_TEST_BINARY to the installed Muse binary")
	}
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "muse-submit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// Keep the Unix socket path short on macOS.
	socket := filepath.Join(os.TempDir(), "muse-submit-"+museRandomSessionSuffix())
	t.Cleanup(func() { _ = exec.Command(realTmux, "-S", socket, "kill-server").Run() })
	config, data, work := filepath.Join(dir, "config"), filepath.Join(dir, "data"), filepath.Join(dir, "work")
	for _, path := range []string{filepath.Join(config, "muse"), data, work} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, "muse", "settings.json"), []byte(`{"schema_version":1,"provider":"echo","mcpServers":{},"tui":{"voice_enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	proxy := `#!/bin/sh
printf '%s\n' "$*" >> "$MUSE_TEST_CALLS"
case "$1:$*" in
 send-keys:*Enter*)
  if [ ! -f "$MUSE_TEST_FIRST" ]; then
   touch "$MUSE_TEST_FIRST"
   exec "$MUSE_TEST_TMUX" -S "$MUSE_TEST_SOCKET" send-keys -t fixture C-j C-j
  fi ;;
esac
exec "$MUSE_TEST_TMUX" -S "$MUSE_TEST_SOCKET" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(proxy), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("MUSE_TEST_CALLS", calls)
	t.Setenv("MUSE_TEST_FIRST", filepath.Join(dir, "first"))
	t.Setenv("MUSE_TEST_TMUX", realTmux)
	t.Setenv("MUSE_TEST_SOCKET", socket)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, realTmux, "-S", socket, "new-session", "-d", "-s", "fixture", "-x", "200", "-y", "50", "-c", work, binary, "--trust-workspace", "--provider", "echo")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated echo launch: %v: %s", err, out)
	}
	if _, err := museWaitSettled(ctx, "fixture", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	prompt := strings.Repeat("A", 304) + "\n\nCompare these fixture totals."
	since := time.Now()
	if err := museSendPrompt(ctx, "fixture", prompt); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(calls)
	if museIntakeFound(data, since, promptSnippet(prompt), work) {
		t.Fatal("the intercepted submit unexpectedly accepted a message")
	}
	if err := museConfirmSubmitOrResubmit(ctx, data, "fixture", prompt, since, work); err != nil {
		t.Fatal(err)
	}
	_, logPath, err := museDiscoverSessionSince(data, since, promptSnippet(prompt), work)
	if err != nil {
		t.Fatalf("recovery never reached native intake: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, line := range strings.Split(string(raw), "\n") {
		var row museIntakeRecord
		if json.Unmarshal([]byte(line), &row) == nil && row.PayloadType == "runtime.user_intent.accepted" {
			accepted++
			if len(row.Payload.RefillBlocks) != 1 || strings.TrimSpace(row.Payload.RefillBlocks[0].Text) != prompt {
				t.Fatal("recovery changed or duplicated the draft")
			}
		}
	}
	after, _ := os.ReadFile(calls)
	if accepted != 1 || strings.Count(string(after), " -l ") != strings.Count(string(before), " -l ") || strings.Contains(string(after), "C-u") || strings.Contains(string(after), "C-c") {
		t.Fatalf("want one accepted message with no retyping or clearing: accepted=%d", accepted)
	}
}
