package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// A retained Muse terminal record whose process is gone (Stop, a died CLI, a provider switch) must not make the next send a 409 delivery_uncertain: the error
// proves nothing was sent, so the message starts a fresh turn (Code on server B, 2026-10-04).
func TestRetainedTerminalGoneStartsANewTurnInsteadOfDeliveryUncertain(t *testing.T) {
	const sessionID = "product-retained-gone"
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		time.Now(), sessionID, "main:"+sessionID, "mlp-muse-product-retained-gone",
	))

	for name, deliverErr := range map[string]error{
		"muse registry gone": errors.New("failed to submit live input to muse-cli: no active Muse interactive session registered for owner session " + sessionID),
		"session closed":     errors.New("muse session is closed"),
	} {
		api := &StreamingAPI{
			internalChatSubmissionStore: newTestChatSubmissionStore(),
			terminalStore:               terminalStore,
			internalRetainedTerminalInputHandler: func(context.Context, llmproviders.Provider, string, string, string) error {
				return deliverErr
			},
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/code/query", nil)
		if handled := api.deliverQueryAsLiveInputNow(recorder, request, sessionID, "hi", "query-1"); handled {
			t.Fatalf("%s: the send was answered (%d %s) instead of starting a new turn", name, recorder.Code, recorder.Body.String())
		}
		if recorder.Body.Len() != 0 {
			t.Fatalf("%s: nothing may be written before the new turn starts, got %s", name, recorder.Body.String())
		}
	}

	// An uncertain failure (the send may have landed) is still reported as delivery_uncertain.
	api := &StreamingAPI{
		internalChatSubmissionStore: newTestChatSubmissionStore(),
		terminalStore:               terminalStore,
		internalRetainedTerminalInputHandler: func(context.Context, llmproviders.Provider, string, string, string) error {
			return errors.New("tmux send-keys: timed out after the keys may have been typed")
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/code/query", nil)
	if handled := api.deliverQueryAsLiveInputNow(recorder, request, sessionID, "hi", "query-2"); !handled || recorder.Code != http.StatusConflict {
		t.Fatalf("an uncertain delivery must stay 409 delivery_uncertain; handled=%v code=%d", handled, recorder.Code)
	}
}
