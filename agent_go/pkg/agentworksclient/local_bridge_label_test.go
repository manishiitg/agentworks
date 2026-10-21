package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Labels reuse the current lease and cannot block commands while HTTP waits.
func TestBridgeLabelRefreshKeepsLeaseAndDoesNotBlock(t *testing.T) {
	started, release := make(chan map[string]string, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args map[string]string
		_ = json.NewDecoder(r.Body).Decode(&args)
		started <- args
		<-release
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	defer close(release)
	client, err := New(srv.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{Attachment: AgentAttachment{ID: "lease", AgentID: "stable-id", AgentName: "worker", Label: "Earlier", Provider: "codex", SessionID: "session"}, client: client,
		Offers: map[string]localOffer{"message": {AgentLiveMessage: AgentLiveMessage{MessageID: "message", Delivery: "processed"}}}}
	b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{"chat": s}, changed: make(chan struct{}), options: LocalBridgeOptions{
		ChatLabel: func(AgentAttachment, string) string { return "Renamed" },
	}}
	done := make(chan struct{})
	go func() { b.refreshChatLabel(context.Background(), s); close(done) }()
	select {
	case args := <-started:
		if args["action"] != "label" || args["attachment_id"] != "lease" || args["agent_name"] != "worker" || args["delivery_route"] != "inbox" {
			t.Fatal(args)
		}
	case <-time.After(time.Second):
		t.Fatal("label update did not start")
	}
	if !b.lockWithin(100 * time.Millisecond) {
		t.Fatal("label HTTP held the global bridge lock")
	}
	b.mu.Unlock()
	release <- struct{}{}
	<-done
	if s.Attachment.ID != "lease" || s.Attachment.AgentID != "stable-id" || s.Attachment.Label != "Renamed" || s.Offers["message"].Delivery != "processed" {
		t.Fatal("label update changed delivery state")
	}
	// An unchanged label and a closed chat cause no HTTP request.
	b.refreshChatLabel(context.Background(), s)
	s.ServerClosed = true
	s.Attachment.Label = "Earlier"
	b.refreshChatLabel(context.Background(), s)
	select {
	case <-started:
		t.Fatal("unneeded label request")
	default:
	}
}
