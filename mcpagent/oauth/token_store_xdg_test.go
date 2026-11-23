package oauth

import (
	"path/filepath"
	"testing"
)

// The per-user token path is built as "~/.config/mcpagent/tokens/..." by
// several callers; on a host whose ~/.config is not writable the deployment
// sets XDG_CONFIG_HOME, and every one of them must land in the same place.
func TestExpandTokenPathHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/srv/state/xdg")
	got := ExpandTokenPath("~/.config/mcpagent/tokens/u1/notion.json")
	if got != filepath.Join("/srv/state/xdg", "mcpagent", "tokens", "u1", "notion.json") {
		t.Fatalf("got %q", got)
	}
	if got := ExpandTokenPath("/abs/path.json"); got != "/abs/path.json" {
		t.Fatalf("absolute paths must pass through, got %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/svc")
	if got := ExpandTokenPath("~/.config/mcpagent/tokens/u1/notion.json"); got != "/home/svc/.config/mcpagent/tokens/u1/notion.json" {
		t.Fatalf("without XDG the historical home path must be used, got %q", got)
	}
}
