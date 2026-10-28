package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
)

func withFastResponseWindow(t *testing.T, window time.Duration) {
	t.Helper()
	previous := liveInputFastResponseWindow
	liveInputFastResponseWindow = window
	t.Cleanup(func() { liveInputFastResponseWindow = previous })
}

// submitLiveInput runs one /query submission through the journal and the
// live-input path the way handleQuery does.
func submitLiveInput(t *testing.T, api *StreamingAPI, key, sessionID, message string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/query", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	r.Header.Set("Idempotency-Key", key)
	recorder := httptest.NewRecorder()
	w, r, finish, ok := api.beginChatSubmission(recorder, r, sessionID, "p", message)
	if !ok {
		return recorder, false
	}
	handled := api.tryDeliverQueryAsLiveInput(w, r, sessionID, message, "query-1", nil)
	finish()
	return recorder, handled
}

func journalState(t *testing.T, store *chatSubmissionStore, key string) string {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(chatHistoryRoot("alice"), "submissions", submissionDigest(key)+".json"))
	raw, ok, err := store.read(context.Background(), path)
	if err != nil || !ok {
		t.Fatalf("journal record for %s missing: ok=%t err=%v", key, ok, err)
	}
	var record chatSubmissionRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	return record.State
}

func waitJournalState(t *testing.T, store *chatSubmissionStore, key, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if journalState(t, store, key) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("journal state = %q, want %q", journalState(t, store, key), want)
}

func writeSentToCLI(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": queryStatusLiveInputDelivered, "delivery_status": "sent_to_cli"})
}

func TestLiveInputFastDeliveryAnswersUnchanged(t *testing.T) {
	withFastResponseWindow(t, time.Second)
	store := newTestChatSubmissionStore()
	api := &StreamingAPI{internalChatSubmissionStore: store,
		internalLiveInputDeliver: func(w http.ResponseWriter, _ *http.Request, _, _, _ string) bool {
			writeSentToCLI(w)
			return true
		},
	}
	recorder, handled := submitLiveInput(t, api, "fast-key", "s-fast", "hello")
	if !handled || recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"sent_to_cli"`) {
		t.Fatalf("fast delivery changed: handled=%t code=%d body=%s", handled, recorder.Code, recorder.Body.String())
	}
	if got := journalState(t, store, "fast-key"); got != "delivery_confirmed" {
		t.Fatalf("journal state = %q", got)
	}
}

func TestLiveInputSlowDeliveryAnswersEarlyThenConfirms(t *testing.T) {
	withFastResponseWindow(t, 50*time.Millisecond)
	store := newTestChatSubmissionStore()
	release := make(chan struct{})
	var calls atomic.Int32
	api := &StreamingAPI{internalChatSubmissionStore: store,
		internalLiveInputDeliver: func(w http.ResponseWriter, r *http.Request, _, _, _ string) bool {
			calls.Add(1)
			<-release
			if r.Context().Err() != nil {
				t.Error("delivery context was canceled with the request")
			}
			writeSentToCLI(w)
			return true
		},
	}
	started := time.Now()
	recorder, handled := submitLiveInput(t, api, "slow-key", "s-slow", "hello")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("request held %s behind a slow delivery", elapsed)
	}
	if !handled || recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"`+liveInputDeliveryInProgress+`"`) || !strings.Contains(recorder.Body.String(), `"accepted"`) {
		t.Fatalf("early reply wrong: handled=%t code=%d body=%s", handled, recorder.Code, recorder.Body.String())
	}
	if got := journalState(t, store, "slow-key"); got != "accepted" {
		t.Fatalf("journal state before delivery = %q, want accepted", got)
	}

	// A same-key retry while the send is in flight replays the receipt and
	// never sends a second time.
	retry, _ := submitLiveInput(t, api, "slow-key", "s-slow", "hello")
	if retry.Code >= 400 {
		t.Fatalf("same-key retry rejected: %d %s", retry.Code, retry.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("delivery ran %d times, want 1", calls.Load())
	}

	close(release)
	waitJournalState(t, store, "slow-key", "delivery_confirmed")
	if calls.Load() != 1 {
		t.Fatalf("delivery ran %d times after completion, want 1", calls.Load())
	}
}

func TestLiveInputSlowDeliveryFailureMarksUncertainAndFailsReceipt(t *testing.T) {
	withFastResponseWindow(t, 50*time.Millisecond)
	store := newTestChatSubmissionStore()
	events := internalevents.NewEventStore(10)
	defer events.Stop()
	api := &StreamingAPI{internalChatSubmissionStore: store, eventStore: events,
		internalLiveInputDeliver: func(w http.ResponseWriter, r *http.Request, _, _, _ string) bool {
			time.Sleep(200 * time.Millisecond)
			writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
			return true
		},
	}
	recorder, handled := submitLiveInput(t, api, "fail-key", "s-fail", "hello")
	if !handled || recorder.Code != http.StatusAccepted {
		t.Fatalf("early reply wrong: handled=%t code=%d body=%s", handled, recorder.Code, recorder.Body.String())
	}
	waitJournalState(t, store, "fail-key", "delivery_uncertain")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range events.GetAllEventsRaw("s-fail") {
			if event.Type == string(pkgevents.LiveInputConfirmed) && strings.Contains(event.ID, "fail-key") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no failed live_input_confirmed receipt for the late delivery")
}

// PLAT-746-163: a send held past the fast window (a 15 s cold file capture) that then found no live target
// (no_active_session after a restart) was marked "Not delivered" and the chat kept its working state, because the
// late path only started a turn when recovered queue text existed. A fresh message now starts the normal new turn
// (live input off, no second provider send) from the validated request or the session's last query; with no base
// request it stays uncertain and the idle chat gets a terminal failure so its working state ends.
func TestLiveInputSlowNoTargetAfterWindow(t *testing.T) {
	cases := []struct {
		name                    string
		validated, cached, busy bool
		wantTurn                bool
	}{
		{name: "no_base_request_stays_uncertain"},
		{name: "failure_keeps_a_running_turn_working", busy: true},
		{name: "cached_last_query_starts_turn", cached: true, wantTurn: true},
		{name: "validated_request_starts_turn", validated: true, wantTurn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFastResponseWindow(t, 50*time.Millisecond)
			store := newTestChatSubmissionStore()
			events := internalevents.NewEventStore(10)
			defer events.Stop()
			started := make(chan QueryRequest, 1)
			var deliveries atomic.Int32
			api := &StreamingAPI{internalChatSubmissionStore: store, eventStore: events,
				internalLiveInputDeliver: func(http.ResponseWriter, *http.Request, string, string, string) bool {
					deliveries.Add(1)
					time.Sleep(200 * time.Millisecond)
					return false
				},
				internalQueryHandler: func(w http.ResponseWriter, r *http.Request) {
					var req QueryRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					started <- req
					_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "query_id": "next-turn"})
				},
				lastQueryRequests: map[string]QueryRequest{}, sessionBusy: map[string]bool{"s-gone": tc.busy},
			}
			// A proven no-target result is never sent to the provider a second time.
			defer func() {
				if n := deliveries.Load(); n != 1 {
					t.Errorf("provider delivery ran %d times, want 1", n)
				}
			}()
			if tc.cached {
				api.lastQueryRequests["s-gone"] = QueryRequest{Query: "earlier", AgentMode: "workflow_phase"}
			}
			r := httptest.NewRequest("POST", "/api/query", nil)
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
			r.Header.Set("Idempotency-Key", "gone-key")
			recorder := httptest.NewRecorder()
			w, r, finish, ok := api.beginChatSubmission(recorder, r, "s-gone", "p", "hello")
			if !ok {
				t.Fatal("submission not accepted")
			}
			if tc.validated {
				r = r.WithContext(context.WithValue(r.Context(), retainedQueueInputKey{}, retainedQueueInput{Request: QueryRequest{Query: "hello", AgentMode: "workflow_phase"}}))
			}
			handled := api.tryDeliverQueryAsLiveInput(w, r, "s-gone", "hello", "query-1", nil)
			finish()
			if !handled || recorder.Code != http.StatusAccepted {
				t.Fatalf("early reply wrong: handled=%t code=%d", handled, recorder.Code)
			}
			if !tc.wantTurn {
				waitJournalState(t, store, "gone-key", "delivery_uncertain")
				ended := false
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) && !ended {
					for _, event := range events.GetAllEventsRaw("s-gone") {
						ended = ended || event.Type == string(pkgevents.EventTypeUnifiedCompletion)
					}
					time.Sleep(10 * time.Millisecond)
				}
				if ended == tc.busy {
					t.Fatalf("working state ended=%t with a running turn=%t", ended, tc.busy)
				}
				return
			}
			select {
			case req := <-started:
				if req.Query != "hello" || !req.DisableLiveInputDelivery || req.AgentMode != "workflow_phase" {
					t.Fatalf("new turn request wrong: %+v", req)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no new turn started after a late no-target delivery")
			}
			path := filepath.ToSlash(filepath.Join(chatHistoryRoot("alice"), "submissions", submissionDigest("gone-key")+".json"))
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				raw, _, _ := store.read(context.Background(), path)
				if strings.Contains(raw, "next-turn") {
					if state := journalState(t, store, "gone-key"); state != "accepted" {
						t.Fatalf("journal state = %q, want accepted", state)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("journal never recorded the new turn: state=%q", journalState(t, store, "gone-key"))
		})
	}
}
