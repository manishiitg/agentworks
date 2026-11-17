package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #3031: prove absence in the exact completed native journal before the normal
// dispatcher clears an unconfirmed steer and claims it as the next turn.
func TestEndedSteerAbsentFromExactJournalStartsNextTurn(t *testing.T) {
	// The SDK producer has a focused real-writer regression in the pinned
	// provider module; mcpagent wraps it with %w. Classification is not wording.
	w := httptest.NewRecorder()
	writeLiveInputUncertain(w, "", fmt.Errorf("changed delivery wording: %w", llmtypes.ErrCodingAgentTurnEndedBeforeInputAck))
	if w.Header().Get("X-Live-Input-Ended-Unconfirmed") != "true" {
		t.Fatal("typed wrapped receipt lost")
	}
	w = httptest.NewRecorder()
	writeLiveInputUncertain(w, "", fmt.Errorf("a write timed out; acceptance is unknown"))
	if w.Header().Get("X-Live-Input-Ended-Unconfirmed") != "" {
		t.Fatal("generic uncertainty became replayable")
	}
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	working, path := privateClaudeHistoryFixture(t)
	runtime := map[string]any{"provider": "claude-code", "external_session_id": "native-private", "agent_session_handle": map[string]any{"provider": map[string]string{"provider": "claude-code", "native_session_id": "native-private", "working_dir": working}}}
	conv := filepath.Join(root, filepath.FromSlash(chatHistoryConversationPath("owner", "ended-steer", time.Now())))
	if err := os.MkdirAll(filepath.Dir(conv), 0700); err != nil {
		t.Fatal(err)
	}
	writeRuntime := func(value any) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"session_id": "ended-steer", "user_id": "owner", "runtime": value})
		if err := os.WriteFile(conv, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRuntime(runtime)
	api := newConversationTurnQueueTestAPI(map[string]string{})
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	turn, _, err := api.enqueueConversationTurn(ctx, "owner", "ended-steer", QueryRequest{Query: "follow up", AgentMode: "multi-agent"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	turn.StartedAt = nil
	turn.UnconfirmedSteerAt = &at
	turn.SteerNativeBinding = queuedSteerNativeBinding(turn)
	turn.markDeliveryFailed("Delivery unconfirmed. Check the chat before resending.")
	if turn.SteerNativeBinding == "" {
		t.Fatal("fixture did not pin native runtime")
	}
	writeClaudeChatTurns(t, path, "previous input", "turn completed")
	// The same text recorded as USER means it arrived: never start it twice.
	writeClaudeChatTurns(t, path, "follow up", "answer")
	if api.reconcileEndedQueuedSteers(ctx, turn.SessionID, []queuedConversationTurn{turn}) {
		t.Fatal("accepted USER input was replayed")
	}
	writeClaudeChatTurns(t, path, withSessionMode(sessionModeOpen+"\nshared mode\n"+sessionModeClose, "follow up"), "answer")
	if api.reconcileEndedQueuedSteers(ctx, turn.SessionID, []queuedConversationTurn{turn}) {
		t.Fatal("mode-prefixed accepted USER input was replayed")
	}
	writeClaudeChatTurns(t, path, "previous input", "turn completed")
	replacement := map[string]any{"provider": "claude-code", "external_session_id": "different-native"}
	writeRuntime(replacement)
	if api.reconcileEndedQueuedSteers(ctx, turn.SessionID, []queuedConversationTurn{turn}) {
		t.Fatal("replacement session proved absence for old source")
	}
	writeRuntime(runtime)
	api.agentCancelFuncs = map[string]context.CancelFunc{turn.SessionID: func() {}}
	if api.reconcileEndedQueuedSteers(ctx, turn.SessionID, []queuedConversationTurn{turn}) {
		t.Fatal("active turn authorized replay")
	}
	delete(api.agentCancelFuncs, turn.SessionID)
	turns := []queuedConversationTurn{turn}
	if !api.reconcileEndedQueuedSteers(ctx, turn.SessionID, turns) {
		t.Fatal("complete final journal did not release absent steer")
	}
	if turns[0].DeliveryError != "" || turns[0].UnconfirmedSteerAt != nil || !turns[0].Request.DisableLiveInputDelivery {
		t.Fatalf("recovered state: %+v", turns[0])
	}
	if err := api.writeConversationTurnQueue(ctx, "owner", []queuedConversationTurn{turn}); err != nil {
		t.Fatal(err)
	}
	next, ok, err := api.claimOldestConversationTurn(ctx, turn.SessionID)
	if err != nil || !ok || next.ID != turn.ID || next.Request.Query != "follow up" || next.DeliveryError != "" || !next.Request.DisableLiveInputDelivery {
		t.Fatalf("original input was not claimed once: %+v %t %v", next, ok, err)
	}
	if _, ok, _ := api.claimOldestConversationTurn(ctx, turn.SessionID); ok {
		t.Fatal("same message claimed twice")
	}
}
