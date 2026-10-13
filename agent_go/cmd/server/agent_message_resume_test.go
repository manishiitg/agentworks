package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the real sweep and workspace HTTP path with a large idle history.
// Any needless worker is held at its first extra read, making the regression
// deterministic rather than relying on sleeps or a throughput threshold.
func TestAgentMessageResumeSkipsIdleHistory(t *testing.T) {
	env := newTriggerLinkEnv(t)
	s := agentMessageStore{}
	for i := 0; i < 314; i++ {
		status := "processed"
		if i%2 == 0 {
			status = "stored"
		}
		s.Conversations = append(s.Conversations, agentConversation{ID: fmt.Sprintf("idle-%d", i), Endpoints: [2]agentMessageEndpoint{{External: true}, {External: true}}, Messages: []agentMessage{{From: 0, Delivery: status}}})
	}
	// Even a malformed old pending external message cannot start an internal turn.
	s.Conversations[0].Messages[0].Delivery = "pending"
	if err := saveAgentMessageStore(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, agentMessagesPath) && reads.Add(1) > 1 {
			<-release
		}
		env.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	env.api.resumeAgentMessages()
	if n := reads.Load(); n != 1 {
		t.Fatalf("idle sweep read store %d times, want one", n)
	}
	agentMessageWorkers.Range(func(key, _ any) bool {
		if strings.HasPrefix(key.(string), "idle-") {
			t.Errorf("idle sweep started worker %s", key)
		}
		return true
	})
}
