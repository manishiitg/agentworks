package handlers

import (
	"strings"
	"testing"
)

func TestExtensionTransportDoesNotConsumeHeadlessCapacity(t *testing.T) {
	bt := browserSessionTracker
	bt.mu.Lock()
	previous := bt.sessions
	bt.sessions = make(map[string]*browserSessionEntry)
	bt.mu.Unlock()
	perChat, global := MaxBrowserSessionsPerChat, MaxBrowserSessionsGlobal
	MaxBrowserSessionsPerChat, MaxBrowserSessionsGlobal = 1, 1
	t.Cleanup(func() {
		bt.mu.Lock()
		bt.sessions = previous
		bt.mu.Unlock()
		MaxBrowserSessionsPerChat, MaxBrowserSessionsGlobal = perChat, global
	})
	env := map[string]string{"MCP_SESSION_ID": "test-owner"}
	if msg := CheckBrowserSessionLimit("agent-browser --session session-native--browser open https://example.test", env); msg != "" {
		t.Fatal(msg)
	}
	for _, cmd := range []string{"open https://example.test", "tab new", "snapshot", "record start take.webm", "record stop"} {
		if msg := CheckBrowserSessionLimit("agent-browser --session session-relay--browser "+cmd+" --cdp ws://127.0.0.1/cdp/private", env, "extension"); msg != "" {
			t.Fatalf("%s: %s", cmd, msg)
		}
	}
	bt.mu.Lock()
	unchanged := len(bt.sessions) == 1 && bt.sessions["session-native--browser"] != nil
	bt.mu.Unlock()
	if !unchanged {
		t.Fatal("relay consumed a global slot or evicted a headless browser")
	}
	for _, transport := range []string{"", "cdp"} {
		if msg := CheckBrowserSessionLimit("agent-browser --session session-other--browser open https://example.test --cdp ws://localhost/cdp/test", env, transport); !strings.Contains(msg, "max 1 per workflow") {
			t.Fatalf("headless capacity bypassed: %q", msg)
		}
	}
	if msg := CheckBrowserSessionLimit("agent-browser --session session-other--browser open https://example.test", env, "extension"); msg == "" {
		t.Fatal("transport marker bypassed a command without a relay endpoint")
	}
}
