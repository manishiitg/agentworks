package musecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Durable-ack P0 (builder docs/refactor/durable_ack_p0.md, PLAT-352): the submit is attempted once and the pane never decides delivery. A TUI that answers
// the Enter with its "Message not sent -- another run is still starting" notice must NOT make the adapter clear and retype the message: each retyped copy was in
// fact accepted and ran (one message ran three or more times on Excellence, 2026-10-04). Delivery is then judged by museWaitIntake from the native log.
//
// A stand-in `tmux` plays the TUI: it shows the notice after Enter and counts what it is sent.
func TestMuseSendPromptDoesNotRetypeAfterARefusalNotice(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	log := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$@" >> '` + log + `'
case "$1" in
  capture-pane)
    case "$(cat '` + state + `' 2>/dev/null)" in
      refused) printf '! Message not sent — another run is still starting\n❯ hello world\n' ;;
      typed)   printf '❯ hello world\n' ;;
      *)       printf '❯ \n' ;;
    esac ;;
  send-keys)
    case "$*" in
      *" -l "*) echo typed > '` + state + `' ;;
      *Enter*)  echo refused > '` + state + `' ;;
      *C-u*)    echo cleared > '` + state + `' ;;
    esac ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := museSendPrompt(ctx, "mlp-muse-test", "hello world"); err != nil {
		t.Fatalf("a refusal notice is provisional and must not fail the submit: %v", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var typed, enters, clears int
	for _, line := range strings.Split(string(calls), "\n") {
		switch {
		case strings.HasPrefix(line, "send-keys") && strings.Contains(line, " -l "):
			typed++
		case strings.HasPrefix(line, "send-keys") && strings.Contains(line, "Enter"):
			enters++
		case strings.HasPrefix(line, "send-keys") && strings.Contains(line, "C-u"):
			clears++
		}
	}
	if typed != 1 || enters != 1 || clears != 0 {
		t.Fatalf("the message must be typed once and submitted once with no clearing; typed=%d submits=%d clears=%d\n%s", typed, enters, clears, calls)
	}
}
