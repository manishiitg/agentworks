package agentworksclient

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Reproduce a provider probe that outlasts the hook deadline: local readiness
// must not wait behind saved-chat discovery or take the global request lock.
func TestLocalBridgeReadyWhileSavedProviderProbeWaits(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-ready-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err = PrepareLocalBridgeRoot(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "login.json")
	if err = SaveConfig(path, Config{Server: "https://example.com", Token: "test-only"}); err != nil {
		t.Fatal(err)
	}
	s := &localSession{AccountSenders: true, HostKey: "saved-process", ConfigPath: path, Attachment: AgentAttachment{ID: "saved-lease", AgentName: "saved-chat", Provider: "codex", SessionID: "saved-native"}}
	if err = atomicLiveJSON(filepath.Join(root, "sessions.json"), map[string]*localSession{"codex:saved-native": s}); err != nil {
		t.Fatal(err)
	}
	probing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{Version: "ready-first", HostAlive: func(string, string) bool { once.Do(func() { close(probing) }); <-release; return false }})
	}()
	defer func() { close(release); cancel(); <-done }()
	select {
	case <-probing:
	case <-time.After(5 * time.Second):
		t.Fatal("saved provider was not probed")
	}
	probeCtx, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	out, err := CallLocalBridge(probeCtx, root, LocalBridgeRequest{Action: "ping"})
	if err != nil || out.Version != "ready-first" {
		t.Fatalf("local readiness blocked by unrelated saved chat: %+v %v", out, err)
	}
}
