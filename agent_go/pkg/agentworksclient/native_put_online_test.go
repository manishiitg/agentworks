package agentworksclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Own-session proof cannot be replaced by model fields. Successful intent
// schedules exactly one existing async capture job, never blocks on readiness.
func TestNativePutOnlineUsesExactLeaseAndQueuesCaptureOnlyOnSuccess(t *testing.T) {
	var requests, jobs atomic.Int32
	var accepted atomic.Bool
	accepted.Store(true)
	started, release := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["action"] == "offline_copy_status" {
			jobs.Add(1)
			started <- struct{}{}
			<-release
			fmt.Fprint(w, `{"shared":false}`)
			return
		}
		requests.Add(1)
		if r.URL.Path != "/api/external/v1/agents/attachment" || in["action"] != "put_online" || in["agent_name"] != "own" || in["attachment_id"] != "lease" || in["audience"] != "only_me" || in["agent_key"] != nil || in["session"] != nil || in["provider"] != nil || in["account"] != nil || in["path"] != nil {
			t.Errorf("Put online identity came from caller: %v", in)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"requested": accepted.Load(), "note": "preparing"})
	}))
	defer srv.Close()
	defer close(release)
	client, err := New(srv.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{HostKey: "process", client: client, Attachment: AgentAttachment{ID: "lease", AgentName: "own", Provider: "claude", SessionID: "exact"}}
	b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{localBridgeKey("claude", "exact"): s}, changed: make(chan struct{})}
	in := LocalBridgeRequest{Action: "put_online", Provider: "claude", SessionID: "exact", HostKey: "other", AttachmentID: "lease", Arguments: map[string]any{"agent_key": "forged", "provider": "codex", "session": "other", "attachment_id": "forged", "path": "/outside", "account": "forged"}}
	b.handle(t.Context(), t.Context(), in)
	if requests.Load() != 0 {
		t.Fatal("wrong process reached server")
	}
	in.HostKey = "process"
	in.AttachmentID = "old"
	b.handle(t.Context(), t.Context(), in)
	if requests.Load() != 0 {
		t.Fatal("old lease reached server")
	}
	in.AttachmentID = "lease"
	done := make(chan LocalBridgeReply, 1)
	go func() { done <- b.handle(t.Context(), t.Context(), in) }()
	select {
	case out := <-done:
		if out.Error != "" || !out.Attached || len(out.Result) == 0 {
			t.Fatalf("intent failed: %v", out)
		}
	case <-time.After(time.Second):
		t.Fatal("Put online waited for capture readiness")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("idle source did not queue capture")
	}
	release <- struct{}{}
	b.workers.Wait()
	accepted.Store(false)
	out := b.handle(t.Context(), t.Context(), in)
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	b.workers.Wait()
	if requests.Load() != 2 || jobs.Load() != 1 {
		t.Fatalf("non-success queued capture: requests=%d jobs=%d", requests.Load(), jobs.Load())
	}
}
