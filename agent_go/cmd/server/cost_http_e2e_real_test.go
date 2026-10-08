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

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	anthropicadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// TestCostSummaryHTTPCapturesRealAnthropicTurn is the full-stack HTTP
// e2e: real Anthropic call → costObserver → ledger → HTTP
// /api/cost/summary. Asserts the handler returns valid JSON with
// non-zero PromptTokens, CompletionTokens, TotalCostUSD, and a
// ByModel entry for the real model.
//
// Gated on RUN_ANTHROPIC_REAL_E2E=1 + ANTHROPIC_API_KEY.
//
// This complements the lower-level
// TestCostLedgerCapturesRealAnthropicTurn (which exercises the
// observer/ledger pipeline directly) by adding the HTTP layer the
// frontend actually hits.
func TestCostSummaryHTTPCapturesRealAnthropicTurn(t *testing.T) {
	if os.Getenv("RUN_ANTHROPIC_REAL_E2E") == "" {
		t.Skip("set RUN_ANTHROPIC_REAL_E2E=1 to run this HTTP e2e")
	}
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY required")
	}
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_REAL_E2E_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5"
	}

	// Real provider call ----------------------------------------------------
	client := anthropic.NewClient(anthropicoption.WithAPIKey(apiKey))
	adapter := anthropicadapter.NewAnthropicAdapter(client, model, &e2eMockLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(16),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	gi := resp.Choices[0].GenerationInfo

	// Build a TokenUsageEvent the same way mcpagent does. ------------------
	additional := map[string]interface{}{}
	for k, v := range gi.Additional {
		additional[k] = v
	}
	prompt := derefInt(gi.PromptTokens, gi.InputTokens)
	completion := derefInt(gi.CompletionTokens, gi.OutputTokens)
	tokenEvent := &unifiedevents.TokenUsageEvent{
		ModelID:          model,
		Provider:         "anthropic",
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		GenerationInfo:   additional,
	}
	// Anthropic's adapter writes cost_usd_estimated (no provider-blessed
	// cost on the Messages API). The cost-routes extractor promotes it
	// into the ledger entry's TotalCostUSD with CostUSDSource=estimated.

	// Build the API with a ledger pointed at an httptest workspace -------
	wsServer := costledger.NewTestServer(t)
	defer wsServer.Close()
	api := &StreamingAPI{
		costLedger: costledger.NewLedger(wsServer.URL),
	}

	// Wire the costObserver and feed the event through it ------------------
	observer := newCostObserver(api.costLedger, "test-session", "test-user", "chat")
	if err := observer.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
		Type:      unifiedevents.TokenUsage,
		Timestamp: time.Now(),
		Component: "test",
		Data:      tokenEvent,
	}); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	// HTTP roundtrip through handleCostSummary -----------------------------
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
		t.Fatalf("Total.CallCount = 0; expected 1 entry")
	}
	if summary.Total.PromptTokens == 0 {
		t.Fatalf("Total.PromptTokens = 0; expected > 0 from real Anthropic call")
	}
	if summary.Total.CompletionTokens == 0 {
		t.Fatalf("Total.CompletionTokens = 0")
	}
	if summary.Total.TotalCostUSD <= 0 {
		t.Fatalf("Total.TotalCostUSD = %v; expected > 0", summary.Total.TotalCostUSD)
	}
	if _, ok := summary.ByModel[model]; !ok {
		keys := []string{}
		for k := range summary.ByModel {
			keys = append(keys, k)
		}
		t.Fatalf("summary.ByModel missing %q; keys=%v", model, keys)
	}
	t.Logf("✅ /api/cost/summary: prompt=%d completion=%d cost=$%.6f model=%q",
		summary.Total.PromptTokens, summary.Total.CompletionTokens, summary.Total.TotalCostUSD, model)
}

// TestCostSummaryHTTPCapturesRealClaudeCodeTmuxTurn is the tmux-transport
// HTTP cost e2e for the Claude Code CLI. Tests the full ledger pipeline
// for a coding-agent provider (rather than direct API), mirroring the
// cursor tmux test in cost_http_e2e_cursor_real_test.go.
//
// Why this exists: claude-code's tmux adapter sources tokens from a sidecar
// JSONL file the CLI writes at ~/.claude/projects/<dir>/<sid>.jsonl. The
// sidecar parser readClaudeTranscriptUsage has unit-test coverage with a
// synthetic fixture, but no end-to-end test verifies that:
//   - the real Claude CLI actually emits that file in the format we expect
//   - the parser hits the real file before the turn's grace window closes
//   - tokens + cost flow through costObserver to /api/cost/summary
//
// If any of those break, this test fails. Unlike cursor (subscription-based,
// pricing = 0), Anthropic is metered, so TotalCostUSD must be > 0.
//
// Gated on RUN_CLAUDE_CODE_EXPERIMENTAL_LIVE_E2E=1 + claude binary.
func TestCostSummaryHTTPCapturesRealClaudeCodeTmuxTurn(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_CODE_EXPERIMENTAL_LIVE_E2E") == "" {
		t.Skip("set RUN_CLAUDE_CODE_EXPERIMENTAL_LIVE_E2E=1 to run this cost HTTP e2e")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude binary not found: %v", err)
	}
	model := strings.TrimSpace(os.Getenv("CLAUDE_CODE_EXPERIMENTAL_MODEL"))
	if model == "" {
		model = "claude-haiku-5-5"
	}
	t.Cleanup(func() { _ = claudecodeadapter.CleanupClaudeCodeTmuxSessions(context.Background()) })

	adapter := claudecodeadapter.NewClaudeCodeInteractiveAdapter(model, &e2eMockLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly the word OK and nothing else."},
			}},
		},
	)
	if err != nil {
		t.Fatalf("claude-code tmux GenerateContent: %v", err)
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
		t.Fatalf("PromptTokens = 0 — readClaudeTranscriptUsage failed to find a usable entry in the sidecar JSONL. gi.Additional=%v", gi.Additional)
	}
	if completion == 0 {
		t.Fatalf("CompletionTokens = 0 — sidecar parser regression. gi.Additional=%v", gi.Additional)
	}

	// Verify the adapter populates CodingProviderIntermediateMessages
	// from the sidecar JSONL — even a single-turn "Reply OK" produces
	// at least one assistant text message from the CLI's transcript.
	// (mcpagent will splice these into conversation_history in the
	// non-direct-LLM flow; this test exercises only the adapter, but
	// the assertion proves the wiring works end-to-end.)
	intermediate, hasIntermediate := llmtypes.ExtractCodingProviderIntermediateMessages(gi)
	if !hasIntermediate || len(intermediate.Messages) == 0 {
		t.Fatalf("CodingProviderIntermediateMessages empty — adapter should reconstruct CLI turn trail from sidecar JSONL (responseSessionID handle=%+v)", gi.CodingProviderSessionHandle)
	}
	if intermediate.Transport != llmtypes.CodingProviderTransportTmux {
		t.Fatalf("intermediate.Transport = %q, want %q", intermediate.Transport, llmtypes.CodingProviderTransportTmux)
	}
	t.Logf("✅ adapter populated %d intermediate message(s) from sidecar", len(intermediate.Messages))

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
		Provider:         "claudecode",
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
	observer := newCostObserver(api.costLedger, "test-session-claude-code-tmux", "test-user", "chat")
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
		t.Fatal("Total.CallCount = 0; expected 1 claude-code tmux turn")
	}
	if summary.Total.PromptTokens == 0 {
		t.Fatalf("Total.PromptTokens = 0 — sidecar token-extraction broken end-to-end")
	}
	if summary.Total.CompletionTokens == 0 {
		t.Fatalf("Total.CompletionTokens = 0 — sidecar token-extraction broken end-to-end")
	}
	if summary.Total.TotalCostUSD <= 0 {
		t.Fatalf("Total.TotalCostUSD = %v — cost computation broken. Anthropic is metered; cost must be > 0. effectiveModel=%q gi.Additional=%v",
			summary.Total.TotalCostUSD, effectiveModel, gi.Additional)
	}
	if _, ok := summary.ByModel[effectiveModel]; !ok {
		keys := []string{}
		for k := range summary.ByModel {
			keys = append(keys, k)
		}
		t.Fatalf("summary.ByModel missing %q; keys=%v", effectiveModel, keys)
	}

	t.Logf("✅ /api/cost/summary claude-code tmux turn: prompt=%d completion=%d cost=$%.6f model=%q",
		summary.Total.PromptTokens, summary.Total.CompletionTokens, summary.Total.TotalCostUSD, effectiveModel)
}
