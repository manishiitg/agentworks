package agentworksclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A stream that goes silent (a half-open connection after sleep or a network change) is dropped so the caller reconnects.
func TestAgentStreamEndsWhenTheServerGoesSilent(t *testing.T) {
	old := streamIdleLimit
	streamIdleLimit = 150 * time.Millisecond
	t.Cleanup(func() { streamIdleLimit = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"connected\"}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // then nothing: no heartbeat
	}))
	defer srv.Close()
	client, err := New(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- client.StreamAgentMessages(context.Background(), AgentAttachment{AgentName: "a", ID: "att"}, func(AgentLiveMessage) error { return nil })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent stream ended without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a silent stream was never dropped")
	}
}

// An offer that was retired from the local list is remembered, so the server still holding it as stored does not make it
// a new late arrival (a second turn); a message never seen before is accepted.
func TestRetiredOfferIsNotTakenForALateArrival(t *testing.T) {
	s := &localSession{Offers: map[string]localOffer{
		"old": {AgentLiveMessage: AgentLiveMessage{MessageID: "old", Delivery: "delivered"}, Offered: true, OfferedAt: time.Now().Add(-8 * 24 * time.Hour)},
	}}
	compactLocalOffers(s, false)
	if _, still := s.Offers["old"]; still {
		t.Fatal("the old offer was not retired")
	}
	recent := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	if lateButNew(s, AgentLiveMessage{MessageID: "old", SentAt: recent}) {
		t.Fatal("a retired offer came back as a new message")
	}
	if !lateButNew(s, AgentLiveMessage{MessageID: "never-seen", SentAt: recent}) {
		t.Fatal("a message never seen before was refused")
	}
}
