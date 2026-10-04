package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	codexcliadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// TestCostSummaryHTTPCapturesRealCodexCLITmuxTurn is the tmux-transport
// HTTP cost e2e for the OpenAI Codex CLI. It mirrors the claude-code
// tmux test in cost_http_e2e_real_test.go: drives a real codex turn
// through tmux, then asserts tokens + cost surface on /api/cost/summary.
//
// Why this exists: codex's interactive adapter sources tokens from a
// sidecar JSONL rollout file the CLI writes under ~/.codex/sessions/.
// The parser readCodexTranscriptUsage has unit coverage with a synthetic
// fixture, but no end-to-end test verifies that:
//   - the real Codex CLI emits that rollout in the format we expect,
//   - the parser hits it before the turn's grace window closes,
//   - tokens + cost_usd_estimated flow through costObserver to
//     /api/cost/summary.
//
// Codex is metered (no flat subscription), so TotalCostUSD must be > 0.
//
// Gated on RUN_CODEX_CLI_INTERACTIVE_E2E=1 + codex binary in PATH.
func TestCostSummaryHTTPCapturesRealCodexCLITmuxTurn(t *testing.T) {
	if os.Getenv("RUN_CODEX_CLI_INTERACTIVE_E2E") == "" {
		t.Skip("set RUN_CODEX_CLI_INTERACTIVE_E2E=1 to run this cost HTTP e2e")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("codex binary not found: %v", err)
	}
	model := strings.TrimSpace(os.Getenv("CODEX_CLI_REAL_CONTRACT_MODEL"))
	if model == "" {
		model = "gpt-5.4-mini"
	}
	t.Cleanup(func() { _ = codexcliadapter.CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := codexcliadapter.NewCodexCLIAdapter("", model, &e2eMockLogger{})
	ownerSessionID := "codex-cost-e2e-" + time.Now().Format("150405")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly the word OK and nothing else."},
			}},
		},
		codexcliadapter.WithInteractiveSessionID(ownerSessionID),
		codexcliadapter.WithPersistentInteractiveSession(true),
		codexcliadapter.WithDisableShellTool(),
		codexcliadapter.WithApprovalPolicy("never"),
		codexcliadapter.WithReasoningEffort("low"),
	)
	if err != nil {
		t.Fatalf("codex tmux GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		t.Fatal("response missing GenerationInfo")
	}
	gi := resp.Choices[0].GenerationInfo

	prompt := derefInt(gi.PromptTokens, gi.InputTokens)
	completion := derefInt(gi.CompletionTokens, gi.OutputTokens)
	if prompt == 0 && resp.Usage != nil && resp.Usage.InputTokens > 0 {
		prompt = resp.Usage.InputTokens
	}
	if completion == 0 && resp.Usage != nil && resp.Usage.OutputTokens > 0 {
		completion = resp.Usage.OutputTokens
	}
	if prompt == 0 {
		t.Fatalf("PromptTokens = 0 — codex sidecar parser failed. gi.Additional=%v", gi.Additional)
	}
	if completion == 0 {
		t.Fatalf("CompletionTokens = 0 — codex sidecar parser regression. gi.Additional=%v", gi.Additional)
	}

	// Verify the adapter populates CodingProviderIntermediateMessages
	// from the rollout JSONL. A real codex turn that just replies
	// "OK" will at minimum produce one assistant text message in the
	// rollout (response_item:message with role=assistant).
	intermediate, hasIntermediate := llmtypes.ExtractCodingProviderIntermediateMessages(gi)
	if !hasIntermediate || len(intermediate.Messages) == 0 {
		t.Fatalf("CodingProviderIntermediateMessages empty — adapter should reconstruct CLI turn trail from rollout JSONL (handle=%+v)", gi.CodingProviderSessionHandle)
	}
	if intermediate.Transport != llmtypes.CodingProviderTransportTmux {
		t.Fatalf("intermediate.Transport = %q, want %q", intermediate.Transport, llmtypes.CodingProviderTransportTmux)
	}
	t.Logf("✅ adapter populated %d intermediate message(s) from codex rollout", len(intermediate.Messages))

	additional := map[string]interface{}{}
	for k, v := range gi.Additional {
		additional[k] = v
	}
	effectiveModel, _ := extractCostAndEffectiveModel(additional)
	if effectiveModel == "" {
		effectiveModel = model
	}

	tokenEvent := &unifiedevents.TokenUsageEvent{
		ModelID:          effectiveModel,
		Provider:         "codexcli",
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		GenerationInfo:   additional,
	}

	wsServer := costledger.NewTestServer(t)
	defer wsServer.Close()
	api := &StreamingAPI{
		costLedger: costledger.NewLedger(wsServer.URL),
	}
	observer := newCostObserver(api.costLedger, "test-session-codex-tmux", "test-user", "chat")
	if err := observer.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
		Type:      unifiedevents.TokenUsage,
		Timestamp: time.Now(),
		Component: "test",
		Data:      tokenEvent,
	}); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cost/summary", nil)
	rec := httptest.NewRecorder()
	api.handleCostSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/cost/summary status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var summary costledger.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v\nbody=%s", err, rec.Body.String())
	}

	if summary.Total.CallCount == 0 {
		t.Fatal("Total.CallCount = 0; expected 1 codex tmux turn")
	}
	if summary.Total.PromptTokens == 0 {
		t.Fatalf("Total.PromptTokens = 0 — codex sidecar token-extraction broken end-to-end")
	}
	if summary.Total.CompletionTokens == 0 {
		t.Fatalf("Total.CompletionTokens = 0 — codex sidecar token-extraction broken end-to-end")
	}
	if summary.Total.TotalCostUSD <= 0 {
		t.Fatalf("Total.TotalCostUSD = %v — cost computation broken. Codex is metered; cost must be > 0. effectiveModel=%q gi.Additional=%v",
			summary.Total.TotalCostUSD, effectiveModel, gi.Additional)
	}
	if _, ok := summary.ByModel[effectiveModel]; !ok {
		keys := []string{}
		for k := range summary.ByModel {
			keys = append(keys, k)
		}
		t.Fatalf("summary.ByModel missing %q; keys=%v", effectiveModel, keys)
	}

	t.Logf("✅ /api/cost/summary codex tmux turn: prompt=%d completion=%d cost=$%.6f model=%q",
		summary.Total.PromptTokens, summary.Total.CompletionTokens, summary.Total.TotalCostUSD, effectiveModel)
}
