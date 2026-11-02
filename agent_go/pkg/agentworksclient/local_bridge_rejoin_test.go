package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// PLAT-856: a login_changed event followed by successful validation of the
// existing lease must not silently leave that chat without a stream worker.
// PLAT-856-273: neither may one refused stream (the server answers 403 for any
// attachment lookup error); the chat then waited for its 25 s timer for days.
func TestStreamReconnectsWhenLoginRecoveryKeepsExistingAttachment(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(fmt.Sprintf("refused=%v", refused), func(t *testing.T) { testStreamReconnects(t, refused) })
	}
}

func testStreamReconnects(t *testing.T, refused bool) {
	var streams, validations atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/external/v1/agents/events" {
			if r.URL.Query().Get("attachment_id") != "lease" {
				t.Error("reconnect changed the attachment")
			}
			n := streams.Add(1)
			if n == 1 && refused {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":{"code":"attachment_refused","message":"Exact attachment unavailable or access denied."}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"connected\",\"attachment_id\":\"lease\"}\n\n")
			if n == 1 {
				fmt.Fprint(w, "data: {\"type\":\"login_changed\"}\n\n")
				return
			}
			fmt.Fprint(w, "data: {\"type\":\"inbox_changed\",\"attachment_id\":\"lease\",\"sequence\":1}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var args map[string]any
		if r.URL.Path != "/api/external/v1/agents/attachment" || json.NewDecoder(r.Body).Decode(&args) != nil || args["attachment_id"] != "lease" {
			t.Errorf("unexpected recovery request: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if args["action"] == "metadata" || args["action"] == "label" {
			fmt.Fprint(w, `{}`)
			return
		}
		if args["action"] != "validate" {
			t.Errorf("unexpected recovery action: %v", args["action"])
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		validations.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	root := t.TempDir()
	path := filepath.Join(root, "login.json")
	if err := SaveConfig(path, Config{Server: srv.URL, Token: "saved-login"}); err != nil {
		t.Fatal(err)
	}
	client, _ := New(srv.URL, "saved-child")
	s := &localSession{AccountSenders: true, HostKey: "process", ParentConfigPath: path, ConfigPath: path, client: client, Attachment: AgentAttachment{ID: "lease", AgentID: "agent", AgentName: "reviewer", Provider: "claude", SessionID: "chat"}}
	b := &localBridge{root: root, sessions: map[string]*localSession{localBridgeKey("claude", "chat"): s}, changed: make(chan struct{}), options: LocalBridgeOptions{HostAlive: func(string, string) bool { return true }}}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); b.workers.Wait() }()
	b.start(ctx, s)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		b.mu.Lock()
		ready, changed := s.streamOnline && s.latestSequence == 1, b.changed
		b.mu.Unlock()
		if ready {
			wantValidations := int32(1)
			if refused {
				wantValidations = 0
			}
			if streams.Load() != 2 || validations.Load() != wantValidations {
				t.Fatalf("streams=%d validations=%d", streams.Load(), validations.Load())
			}
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("existing attachment did not reconnect and receive inbox hint: streams=%d validations=%d", streams.Load(), validations.Load())
		}
	}
}

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
