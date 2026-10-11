package agentworksclient

import (
	"context"
	"testing"
	"time"
)

// Route observations must come from the exact fenced SDK adapter. They cannot
// revive an offline identity or claim that a queued message was admitted.
func TestPiRouteRequiresFencedSDKConsumer(t *testing.T) {
	session := &localSession{Attachment: AgentAttachment{Provider: "pi", SessionID: "sdk-session", ID: "person-lease"}, HostKey: "host", client: &Client{}, Offers: map[string]localOffer{}}
	b := &localBridge{sessions: map[string]*localSession{localBridgeKey("pi", "sdk-session"): session}, changed: make(chan struct{})}
	request := LocalBridgeRequest{Action: "status", Provider: "pi", SessionID: "sdk-session", HostKey: "host", AttachmentID: "person-lease"}
	if b.handle(context.Background(), context.Background(), request).NativeDelivery {
		t.Fatal("unseen SDK claims native delivery")
	}
	request.Action, request.Transport = "next", "pi_extension"
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	missing := request
	missing.AttachmentID = ""
	_ = b.handle(context.Background(), cancelled, missing)
	if !session.piNextAt.IsZero() {
		t.Fatal("missing lease changed adapter observation")
	}
	wrong := request
	wrong.HostKey = "other-process"
	_ = b.handle(context.Background(), cancelled, wrong)
	if !session.piNextAt.IsZero() {
		t.Fatal("wrong host changed adapter observation")
	}
	wrong = request
	wrong.AttachmentID = "other-lease"
	_ = b.handle(context.Background(), cancelled, wrong)
	if !session.piNextAt.IsZero() {
		t.Fatal("wrong lease changed adapter observation")
	}
	_ = b.handle(context.Background(), cancelled, request)
	request.Action = "status"
	if !b.handle(context.Background(), context.Background(), request).NativeDelivery {
		t.Fatal("valid SDK observation not reported")
	}
	if len(session.Offers) != 0 {
		t.Fatal("route observation admitted or fabricated messages")
	}
	session.Offline = true
	if piNativeActive(session) {
		t.Fatal("observation revived offline identity")
	}
	session.Offline = false
	session.piNextAt = time.Now().Add(-time.Minute)
	if piNativeActive(session) {
		t.Fatal("stale observation claims live delivery")
	}
}
