package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// Drive the durable document and its owner-facing HTTP actions. Claiming a
// steer and claiming a new turn share the same lock, so only one can own it.
func TestChatDeliveryModeDurableQueueActions(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	enqueue := func(id, text string) queuedConversationTurn {
		t.Helper()
		keyed := context.WithValue(ctx, chatSubmissionContextKey{}, chatSubmissionContext{ID: id})
		turn, _, err := api.enqueueConversationTurn(keyed, "owner", "chat", QueryRequest{Query: text, AgentMode: "multi-agent", DeliveryMode: "queue"})
		if err != nil {
			t.Fatal(err)
		}
		return turn
	}
	first := enqueue("first-client", "first")
	second := enqueue("second-client", "second")
	// No background worker is involved in owner actions in this harness.
	api.conversationTurnQueueOwners = map[string]string{}
	request := func(method, id, user string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/sessions/chat/queued-messages", nil)
		r = r.WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: user}))
		r = mux.SetURLVars(r, map[string]string{"session_id": "chat", "message_id": id})
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	listed := request(http.MethodGet, "", "owner", api.handleQueuedConversationMessages)
	var response struct {
		Messages []PendingQueuedMessage `json:"messages"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Messages) != 2 || response.Messages[0].MessageID != first.ID || response.Messages[1].Content != "second" {
		t.Fatalf("queue = %+v", response)
	}
	if rejected := request(http.MethodDelete, first.ID, "other-owner", api.handleRemoveQueuedConversationMessage); rejected.Code != http.StatusNotFound {
		t.Fatalf("cross-owner removal returned %d", rejected.Code)
	}
	if removed := request(http.MethodDelete, first.ID, "owner", api.handleRemoveQueuedConversationMessage); removed.Code != http.StatusAccepted {
		t.Fatalf("remove: %d %s", removed.Code, removed.Body.String())
	}
	if got := api.mustReadTurnQueueForTest(t, "owner"); len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("after removal = %+v", got)
	}
	steered, status, err := api.changeQueuedConversationMessage(ctx, "owner", "chat", second.ID, true)
	if err != nil || status != http.StatusOK || !steered.Steering {
		t.Fatalf("steer reservation: %+v %d %v", steered, status, err)
	}
	if _, ok := api.claimNextConversationTurn(ctx, "owner", "chat"); ok {
		t.Fatal("normal worker also claimed a steered message")
	}
	if _, status, err := api.changeQueuedConversationMessage(ctx, "owner", "chat", second.ID, true); err != nil || status != http.StatusAccepted {
		t.Fatalf("retry must adopt the same steer: %d %v", status, err)
	}
	if _, status, _ := api.changeQueuedConversationMessage(ctx, "owner", "chat", second.ID, false); status != http.StatusConflict {
		t.Fatalf("removing a dispatching message returned %d", status)
	}
	// The durable queue still owns waiting messages after replacement/reload,
	// and an explicit Queue next does not silently expire during a long task.
	old := queuedConversationTurn{ID: "long-task", UserID: "owner", SessionID: "chat", SubmissionID: "waiting-client", Request: QueryRequest{DeliveryMode: "queue", Query: "wait"}, CreatedAt: time.Now().Add(-time.Hour)}
	if got := dropStaleWaitingTurns([]queuedConversationTurn{old}, time.Now()); len(got) != 1 {
		t.Fatal("explicit queued turn expired")
	}
}
