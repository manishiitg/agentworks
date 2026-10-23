package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Real OAuth families, authenticated HTTP/SSE and the private helper socket pin
// recovery of a disconnected name and a replaced login without losing its inbox.
func TestAgentRejoinKeepsInboxAndReloadsLogin(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "rejoin-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == cliOAuthTokenPath {
			env.api.handleCLIOAuthToken(w, r)
			return
		}
		if r.URL.Path == cliOAuthRevokePath {
			env.api.handleCLIOAuthRevoke(w, r)
			return
		}
		AuthMiddleware(mux).ServeHTTP(w, r)
	}))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	ctx := context.Background()
	issue := func() (agentworksclient.Config, string) {
		t.Helper()
		device, code, e := store.CreateCLIDevice(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if e = store.DecideCLIDevice(ctx, code, &UserClaims{UserID: "alice", Username: "alice"}, true); e != nil {
			t.Fatal(e)
		}
		grant, access, refresh, e := store.PollCLIDevice(ctx, device, srv.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		return agentworksclient.Config{Server: srv.URL, Token: access, RefreshToken: refresh, ExpiresAt: time.Now().Add(time.Hour)}, grant.FamilyID
	}
	first, _ := issue()
	next, nextFamily := issue()
	path := filepath.Join(t.TempDir(), "login.json")
	if err := agentworksclient.SaveConfig(path, first); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "aw-rejoin-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	var oldDead atomic.Bool
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- agentworksclient.ServeLocalBridge(runCtx, root, agentworksclient.LocalBridgeOptions{HostAlive: func(_ string, host string) bool { return host != "old-host" || !oldDead.Load() }})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	await := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("rejoin did not finish")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	await(func() bool {
		_, e := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"})
		return e == nil
	})
	old := agentworksclient.LocalBridgeRequest{Action: "attach", Provider: "claude", SessionID: "old-chat", HostKey: "old-host", AgentName: "reviewer", ConfigPath: path, OwnAttach: true}
	attached, err := agentworksclient.CallLocalBridge(ctx, root, old)
	if err != nil || !attached.Attached {
		t.Fatalf("initial attach: %+v %v", attached, err)
	}
	online := func(id string) bool {
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		s, e := readAgentMessageStore(ctx)
		if e != nil {
			return false
		}
		for _, a := range s.Agents {
			if a.ID == id {
				return namedAgentOnline(a)
			}
		}
		return false
	}
	await(func() bool { return online(attached.Attachment.AgentID) })
	newer, _ := agentworksclient.New(srv.URL, next.Token)
	if _, err := newer.Call(ctx, "messages", map[string]any{"action": "register", "agent_name": "reviewer"}); err == nil {
		t.Fatal("another live chat's name was reclaimed")
	}
	prior, _ := agentworksclient.New(srv.URL, first.Token)
	if _, err := prior.Call(ctx, "messages", map[string]any{"action": "register", "agent_name": "sender"}); err != nil {
		t.Fatal(err)
	}
	raw, err := prior.Call(ctx, "messages", map[string]any{"action": "send", "agent_name": "sender", "target": "agent:reviewer", "message": "Keep this inbox", "submission_id": "rejoin-one"})
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		ID    string `json:"message_id"`
		Inbox string `json:"inbox_id"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	replacement := old
	replacement.SessionID = "new-chat"
	replacement.HostKey = "new-host"
	if err := agentworksclient.SaveConfig(path, next); err != nil {
		t.Fatal(err)
	}
	if _, err := agentworksclient.CallLocalBridge(ctx, root, replacement); err == nil {
		t.Fatal("a locally alive chat's name was taken")
	}
	oldDead.Store(true)
	recovered, err := agentworksclient.CallLocalBridge(ctx, root, replacement)
	if err != nil || !recovered.Attached || recovered.Attachment.AgentID != attached.Attachment.AgentID {
		t.Fatalf("dead-chat rejoin: %+v %v", recovered, err)
	}
	await(func() bool { return online(recovered.Attachment.AgentID) })
	// Replace the browser login while this exact helper and chat remain running.
	fresh, _ := issue()
	if err := agentworksclient.SaveConfig(path, fresh); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeFamily(ctx, nextFamily, "alice"); err != nil {
		t.Fatal(err)
	}
	notifyAgentLiveChanged()
	status := replacement
	status.Action = "status"
	var latest agentworksclient.LocalBridgeReply
	await(func() bool {
		latest, _ = agentworksclient.CallLocalBridge(ctx, root, status)
		return latest.Attached && latest.Attachment.ID != recovered.Attachment.ID && online(recovered.Attachment.AgentID)
	})
	if latest.Attachment.AgentID != attached.Attachment.AgentID || latest.Attachment.SessionID != "new-chat" {
		t.Fatalf("login recovery moved identity: %+v", latest.Attachment)
	}
	read := status
	read.Action = "read"
	read.InboxID = sent.Inbox
	inbox, err := agentworksclient.CallLocalBridge(ctx, root, read)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range inbox.Messages {
		if m.MessageID == sent.ID && m.Message == "Keep this inbox" {
			found = true
		}
	}
	if !found {
		t.Fatalf("original inbox lost: %+v", inbox)
	}
}
