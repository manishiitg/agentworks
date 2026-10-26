package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit selectors and replies keep their network. Unqualified first sends
// are resolved separately against joined directories before any write.
func TestJoinedNetworkSelectorsAndReplyRouting(t *testing.T) {
	primary := localBridgeKey("claude", "11111111-2222-4333-8444-555555555555")
	own := &localSession{Attachment: AgentAttachment{AgentName: "worker"}}
	other := &localSession{Also: true, Network: "partner", ParentConfigPath: "/logins/partner.json", Attachment: AgentAttachment{AgentName: "worker"},
		ReadPosition: 1}
	b := &localBridge{sessions: map[string]*localSession{primary: own, localAlsoKey(primary, other.ParentConfigPath): other}}
	if got := b.extrasOf(primary); len(got) != 1 || got[0] != other {
		t.Fatal("joined network not found", got)
	}
	for _, c := range []struct {
		in   LocalBridgeRequest
		want *localSession
	}{
		{LocalBridgeRequest{Action: "send", Target: "reviewer"}, own},
		{LocalBridgeRequest{Action: "send", Target: "reviewer@partner"}, other},
		{LocalBridgeRequest{Action: "send", Target: "reviewer@unknown"}, own},
		{LocalBridgeRequest{Action: "send", Target: "ops@example.com"}, own},
		{LocalBridgeRequest{Action: "reply_inbox", InboxID: "inbox-partner", Network: "partner"}, other},
		{LocalBridgeRequest{Action: "peers", Network: "partner"}, other},
	} {
		if got := b.networkFor(primary, own, c.in); got != c.want {
			t.Fatalf("%+v went to the wrong network", c.in)
		}
	}
	if networkTarget("reviewer@partner", "partner") != "reviewer" || networkTarget("ops@example.com", "partner") != "ops@example.com" {
		t.Fatal("the network suffix was not removed, or another name was changed")
	}
	if b.networkFor(primary, own, LocalBridgeRequest{Action: "read", Network: "nowhere"}) != nil {
		t.Fatal("an unknown network was treated as the chat's own")
	}
}

// PLAT-856-372: signing out one saved account must leave other accounts' chats
// and the shared helper running, even when they use the same server.
func TestNamedSignOutKeepsOtherLoginConnections(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-signout-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	var detaches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"type\":\"connected\",\"attachment_id\":%q}\n\n", r.URL.Query().Get("attachment_id"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["action"] == "detach" {
			if in["agent_name"] == "own-chat" {
				t.Error("named logout detached another account's primary chat")
			}
			detaches.Add(1)
		}
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	ownLogin, namedLogin := filepath.Join(root, "default.json"), filepath.Join(root, "partner.json")
	for _, path := range []string{ownLogin, namedLogin} {
		if err = SaveConfig(path, Config{Server: server.URL, Token: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	makeSession := func(name, session, login string) *localSession {
		child := filepath.Join(root, name+"-child.json")
		if err := SaveConfig(child, Config{Server: server.URL, Token: "fixture"}); err != nil {
			t.Fatal(err)
		}
		return &localSession{AccountSenders: true, HostKey: "host", ConfigPath: child, ParentConfigPath: login, ReadPosition: 27,
			Attachment: AgentAttachment{ID: "lease-" + name, AgentID: "agent-" + name, AgentName: name, Provider: "claude", SessionID: session}}
	}
	own := makeSession("own-chat", "11111111-2222-4333-8444-555555555555", ownLogin)
	extra := makeSession("chosen-join", own.Attachment.SessionID, namedLogin)
	extra.Also, extra.Network = true, "partner"
	stray := makeSession("stray-chat", "66666666-7777-4888-8999-aaaaaaaaaaaa", namedLogin)
	ownKey, strayKey := localBridgeKey("claude", own.Attachment.SessionID), localBridgeKey("claude", stray.Attachment.SessionID)
	extraKey := localAlsoKey(ownKey, namedLogin)
	if err = atomicLiveJSON(filepath.Join(root, "sessions.json"), map[string]*localSession{ownKey: own, extraKey: extra, strayKey: stray}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{Version: "scope", HostAlive: func(string, string) bool { return true }})
	}()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "ping"}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "sign_out_login", ConfigPath: namedLogin}); err != nil {
		t.Fatal(err)
	}
	ping, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "ping"})
	if err != nil || ping.Version != "scope" {
		t.Fatal("signing out a named login stopped the shared helper", err)
	}
	status, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: own.Attachment.SessionID, HostKey: "host"})
	if err != nil || !status.Attached || status.Attachment.ID != own.Attachment.ID || len(status.Networks) != 0 {
		t.Fatalf("unrelated primary chat changed or signed-out join remains: %+v %v", status, err)
	}
	var saved map[string]*localSession
	if err = readLiveJSON(filepath.Join(root, "sessions.json"), &saved); err != nil || len(saved) != 1 || saved[ownKey] == nil || saved[ownKey].ReadPosition != 27 {
		t.Fatal("sign-out did not retain only the other login's identity and read position", err)
	}
	var stopped map[string]bool
	if err = readLiveJSON(filepath.Join(root, "disconnected.json"), &stopped); err != nil || !stopped[strayKey] || !stopped[extraKey] || stopped[ownKey] {
		t.Fatal("signed-out chats can recover automatically, or another chat was stopped", err)
	}
	if detaches.Load() != 2 {
		t.Fatal("both the selected primary and chosen join must leave that login")
	}
	if _, err = os.Stat(own.ConfigPath); err != nil {
		t.Fatal("another login's child connection was removed", err)
	}
	for _, path := range []string{extra.ConfigPath, stray.ConfigPath} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("signed-out child connection remains", err)
		}
	}
}
