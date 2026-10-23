package agentworksclient

import (
	"context"
	"testing"
	"time"
)

// A stopped daemon preserves the approved lease; a person detach ends it.
// Both wake a pending SDK next, but only detach may report unattached.
func TestPiPendingNextDistinguishesBridgeStopFromDetach(t *testing.T) {
	for _, stopped := range []bool{true, false} {
		name := "detach"
		if stopped {
			name = "bridge stop"
		}
		t.Run(name, func(t *testing.T) {
			session := &localSession{Attachment: AgentAttachment{Provider: "pi", SessionID: "sdk-session", ID: "person-lease"}, HostKey: "host", client: &Client{}}
			key := localBridgeKey("pi", "sdk-session")
			b := &localBridge{sessions: map[string]*localSession{key: session}, changed: make(chan struct{})}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			replies := make(chan LocalBridgeReply, 1)
			go func() {
				replies <- b.handle(context.Background(), ctx, LocalBridgeRequest{Action: "next", Provider: "pi", SessionID: "sdk-session", HostKey: "host", AttachmentID: "person-lease", Transport: "pi_extension"})
			}()
			for {
				b.mu.Lock()
				waiting := !session.piNextAt.IsZero()
				if waiting {
					if stopped {
						b.closed = true
					} else {
						delete(b.sessions, key)
					}
					b.wake()
				}
				b.mu.Unlock()
				if waiting {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("SDK next did not begin its local wait")
				case <-time.After(time.Millisecond):
				}
			}
			select {
			case reply := <-replies:
				if stopped {
					if reply.Error == "" {
						t.Fatal("daemon stop falsely reported an ended attachment")
					}
					if b.sessions[key] != session || session.Attachment.ID != "person-lease" || session.Offline {
						t.Fatal("daemon stop changed the approved lease")
					}
				} else if reply.Error != "" || reply.Attached {
					t.Fatal("detach did not report an ended attachment")
				}
			case <-ctx.Done():
				t.Fatal("SDK next did not wake")
			}
		})
	}
}

// Route observations must come from the exact fenced SDK adapter. They cannot
// revive an offline identity or claim that a queued message was admitted.
func TestPiRouteRequiresFencedSDKConsumer(t *testing.T) {
	session := &localSession{Attachment: AgentAttachment{Provider: "pi", SessionID: "sdk-session", ID: "person-lease"}, HostKey: "host", client: &Client{}}
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
	if session.ReadPosition != 0 {
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
