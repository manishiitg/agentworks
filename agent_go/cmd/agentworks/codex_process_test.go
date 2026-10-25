package main

import (
	"fmt"
	"os"
	"testing"
)

// A stale desktop core survived an update and preceded the actual app's core
// in ps. Discovery must use the socket owner's local core, just as MCP does.
func TestCodexDiscoveryUsesSocketOwnersCore(t *testing.T) {
	ps := fmt.Sprintf(`10 100 %[1]d /Applications/Codex.app/Contents/Resources/codex app-server --listen stdio://
20 200 %[1]d /Applications/Codex.app/Contents/Resources/codex app-server --analytics-default-enabled
30 200 %[1]d /Applications/Codex.app/Contents/Resources/codex exec-server --remote https://example.com
50 200 %[2]d /Applications/Codex.app/Contents/Resources/codex app-server
`, os.Getuid(), os.Getuid()+1)
	key := func(pid int) (string, error) {
		if pid != 20 {
			t.Fatal("selected unrelated core", pid)
		}
		return "current-process", nil
	}
	host, err := codexCoreHostForPeer(ps, 200, key)
	if err != nil || host != "current-process" {
		t.Fatal(host, err)
	}
	if _, err = codexCoreHostForPeer(ps, 300, key); err == nil {
		t.Fatal("selected a core outside the actual socket owner")
	}
	if _, err = codexCoreHostForPeer(ps+fmt.Sprintf("40 200 %d /Applications/Codex.app/Contents/Resources/codex app-server\n", os.Getuid()), 200, func(int) (string, error) { return "key", nil }); err == nil {
		t.Fatal("selected one of two ambiguous local cores")
	}
}
