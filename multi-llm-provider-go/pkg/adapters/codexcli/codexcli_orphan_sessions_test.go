package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReapOrphanCodexTmuxSessionsKillsOnlyThisOwnersOldPanes(t *testing.T) {
	fakeBin := t.TempDir()
	killed := filepath.Join(fakeBin, "killed")
	script := `#!/bin/sh
if [ "$1" = "list-sessions" ]; then
  printf 'mlp-codex-cli-int-old-aaaa\tproduct-1\n'
  printf 'mlp-codex-cli-int-other-bbbb\tproduct-2\n'
  printf 'mlp-codex-cli-int-new-cccc\tproduct-1\n'
  printf 'untagged-session\t\n'
  exit 0
fi
if [ "$1" = "kill-session" ]; then
  printf '%s\n' "$3" >> "$KILLED"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KILLED", killed)

	got := reapOrphanCodexTmuxSessions(context.Background(), "product-1", "mlp-codex-cli-int-new-cccc", func(string, ...interface{}) {})
	if want := []string{"mlp-codex-cli-int-old-aaaa"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reaped %v, want %v", got, want)
	}
	data, err := os.ReadFile(killed)
	if err != nil {
		t.Fatalf("no kill-session was issued: %v", err)
	}
	if string(data) != "mlp-codex-cli-int-old-aaaa\n" {
		t.Fatalf("kill-session targets = %q, want only the orphan", string(data))
	}

	if got := reapOrphanCodexTmuxSessions(context.Background(), "", "x", nil); got != nil {
		t.Fatalf("an empty owner reaped %v", got)
	}
	if got := listCodexTmuxSessionsForOwner(context.Background(), "product-3"); got != nil {
		t.Fatalf("unknown owner listed %v", got)
	}
}
