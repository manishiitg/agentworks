package agentworksclient

import (
	"context"
	"errors"
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
		_, _ = w.Write([]byte("data: {\"type\":\"connected\",\"attachment_id\":\"att\"}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // then nothing: no heartbeat
	}))
	defer srv.Close()
	client, err := New(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connected := make(chan struct{})
	var connectedAt time.Time
	done := make(chan error, 1)
	go func() {
		done <- client.StreamAgentMessages(ctx, AgentAttachment{AgentName: "a", ID: "att"}, func(AgentLiveMessage) error { return nil }, func() {
			connectedAt = time.Now()
			close(connected)
		})
	}()
	select {
	case err := <-done:
		select {
		case <-connected:
		default:
			t.Fatalf("stream failed before accepting the matching attachment: %v", err)
		}
		if !errors.Is(err, context.Canceled) || ctx.Err() != nil || time.Since(connectedAt) < streamIdleLimit {
			t.Fatalf("stream did not end through its idle timer: %v (parent=%v, connected=%s)", err, ctx.Err(), time.Since(connectedAt))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a silent stream was never dropped")
	}
}
