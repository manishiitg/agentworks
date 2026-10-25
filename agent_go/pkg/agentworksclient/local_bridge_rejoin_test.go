package agentworksclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestLoginRecoveryBoundsAuthFailuresAndKeepsExplicitDetach(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"insufficient_scope","message":"Login refused"}}`))
	}))
	defer srv.Close()
	root := t.TempDir()
	path := filepath.Join(root, "login.json")
	cfg := Config{Server: srv.URL, Token: "first-login"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	client, _ := New(srv.URL, "old-child")
	s := &localSession{AccountSenders: true, Offline: true, HostKey: "process", ParentConfigPath: path, ConfigPath: path, client: client, Attachment: AgentAttachment{ID: "lease", AgentID: "agent", AgentName: "reviewer", Provider: "claude", SessionID: "chat"}}
	b := &localBridge{root: root, sessions: map[string]*localSession{localBridgeKey("claude", "chat"): s}, changed: make(chan struct{}), options: LocalBridgeOptions{HostAlive: func(string, string) bool { return true }}}
	var retry loginRecovery
	for i := 0; i < 5; i++ {
		if b.recoverSessionLogin(context.Background(), s, &retry) {
			t.Fatal("refused login recovered")
		}
	}
	if n := requests.Load(); n != 3 {
		t.Fatalf("authentication retries=%d, want 3", n)
	}
	cfg.Token = "fresh-login"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	b.recoverSessionLogin(context.Background(), s, &retry)
	if requests.Load() != 4 {
		t.Fatal("fresh login did not reset retries")
	}
	delete(b.sessions, localBridgeKey("claude", "chat"))
	b.recoverSessionLogin(context.Background(), s, &retry)
	if requests.Load() != 4 {
		t.Fatal("explicitly detached chat was recovered")
	}
	b.sessions[localBridgeKey("claude", "chat")] = s
	s.ServerClosed = true
	b.recoverSessionLogin(context.Background(), s, &retry)
	if requests.Load() != 4 {
		t.Fatal("server-closed chat was recovered")
	}
}
