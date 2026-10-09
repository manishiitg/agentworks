package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// Muse is running a turn, the person switches the chat to Codex on the Models page and sends another message (Code on server B, 2026-10-04). The message must wait
// in the turn queue; it must not interrupt the running Muse turn and must not be steered into (or fail against) the old provider's terminal.
func TestProviderSwitchDuringARunningTurnQueuesTheMessage(t *testing.T) {
	const sessionID = "workflow-provider-switch-queue"
	env := newProviderAccountsEnv(t, "")
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		time.Now(), sessionID, "main:"+sessionID, "mlp-muse-workflow-provider-switch-queue",
	))
	env.api.terminalStore = terminalStore

	cancelled := false
	env.api.agentCancelFuncs = map[string]context.CancelFunc{sessionID: func() { cancelled = true }}
	release := env.api.lockSessionInputLane(sessionID) // the Muse turn holds the session's input lane
	defer release()

	req := QueryRequest{
		Query: "do you know what functions we have", AgentMode: "workflow_phase", SelectedFolder: "Workflow/w",
		Provider:  "codex-cli",
		LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "codex-cli", ModelID: "gpt-6-luna"}},
	}
	w := httptest.NewRecorder()
	httpReq := sharedSecretsRequest(http.MethodPost, "/api/query", "alice", req)
	httpReq.Header.Set("X-Session-ID", sessionID)
	env.api.handleQuery(w, httpReq)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "queued_for_turn") {
		t.Fatalf("a send after a provider switch during a running turn must queue; got %d %s", w.Code, w.Body.String())
	}
	if cancelled {
		t.Fatal("the running turn was cancelled; the provider change must wait for it")
	}
}
