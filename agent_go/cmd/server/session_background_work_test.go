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

// PLAT-705: the chat's background-work list and per-item Stop are scoped to the
// session's own user; another user can neither see nor stop that work.
func TestSessionBackgroundWorkIsOwnerScoped(t *testing.T) {
	api := lifecycleTestAPI()
	api.activeSessions = map[string]*ActiveSessionInfo{"chat-a": {SessionID: "chat-a", UserID: "alice"}}
	canceled := false
	step := &BackgroundAgent{ID: "workflow-step-0-x", Name: "Weekly knowledge refresh", Kind: "workflow_step", Status: BGAgentRunning, CreatedAt: time.Now(), cancel: func() { canceled = true }}
	api.bgAgentRegistry.Register("chat-a", step)
	step.MarkTerminalNotified() // no event store in this test
	// A child of the step is stopped with it and not listed on its own.
	child := &BackgroundAgent{ID: "child", Name: "Collect sources", ParentExecutionID: step.ID, Status: BGAgentRunning, CreatedAt: time.Now()}
	api.bgAgentRegistry.Register("chat-a", child)
	child.MarkTerminalNotified()

	router := mux.NewRouter()
	router.HandleFunc("/api/sessions/{session_id}/background-work", api.handleGetSessionBackgroundWork).Methods("GET")
	router.HandleFunc("/api/sessions/{session_id}/background-work/{execution_id}/stop", api.handleStopSessionBackgroundWork).Methods("POST")
	call := func(method, path, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	if w := call("GET", "/api/sessions/chat-a/background-work", "mallory"); w.Code != http.StatusNotFound {
		t.Fatalf("another user listed the chat's work: %d", w.Code)
	}
	if w := call("POST", "/api/sessions/chat-a/background-work/workflow-step-0-x/stop", "mallory"); w.Code != http.StatusNotFound || canceled {
		t.Fatalf("another user stopped the chat's work: code=%d canceled=%v", w.Code, canceled)
	}

	w := call("GET", "/api/sessions/chat-a/background-work", "alice")
	var body struct {
		Items []SessionBackgroundWorkItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Items) != 1 || body.Items[0].Kind != "step" || !body.Items[0].CanStop || body.Items[0].Status != "Working on Collect sources" {
		t.Fatalf("owner list = %d %s", w.Code, w.Body.String())
	}
	if w := call("POST", "/api/sessions/chat-a/background-work/workflow-step-0-x/stop", "alice"); w.Code != http.StatusOK || !canceled {
		t.Fatalf("owner stop: code=%d canceled=%v", w.Code, canceled)
	}
	if step.GetStatus() != BGAgentCanceled || child.GetStatus() != BGAgentCanceled {
		t.Fatalf("step=%s child=%s, want both canceled", step.GetStatus(), child.GetStatus())
	}
}
