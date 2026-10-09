package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// A pending agent question belongs to the person whose chat asked it: another
// signed-in user can neither list nor answer it.
func TestPendingHumanFeedbackIsScopedToTheSessionOwner(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	const session = "alice-chat"
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{session: {SessionID: session, UserID: "alice"}}}
	id := "hf-" + uuid.NewString()
	store := virtualtools.GetHumanFeedbackStore()
	if err := store.CreatePendingRequest(id, "Send the emails?", "", session, []string{"Yes", "No"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	list := func(user string) string {
		w := httptest.NewRecorder()
		api.handleListPendingHumanFeedback(w, adminRequest(http.MethodGet, "/api/human-feedback/pending", "", &UserClaims{UserID: user}, nil))
		return w.Body.String()
	}
	submit := func(user string) int {
		w := httptest.NewRecorder()
		api.handleSubmitHumanFeedback(w, adminRequest(http.MethodPost, "/api/human-feedback/submit", `{"unique_id":"`+id+`","response":"Yes"}`, &UserClaims{UserID: user}, nil))
		return w.Code
	}
	if strings.Contains(list("bob"), id) {
		t.Fatal("another user listed the question")
	}
	if code := submit("bob"); code != http.StatusNotFound {
		t.Fatalf("another user answered the question: %d", code)
	}
	if !strings.Contains(list("alice"), id) {
		t.Fatal("the owner cannot see her question")
	}
	if code := submit("alice"); code != http.StatusOK {
		t.Fatalf("the owner could not answer: %d", code)
	}
}
