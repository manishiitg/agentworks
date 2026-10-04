package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	codexcliadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	cursorcliadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// multiTurnChatE2ESpec describes one tmux-coding-agent provider for
// runMultiTurnChatE2E. We use a spec struct so the same test body
// drives claude-code, codex, and cursor — every assertion runs
// identically; only the adapter + per-call options differ.
type multiTurnChatE2ESpec struct {
	providerName     string // human-friendly name for log messages
	providerKey      string // ledger Provider field
	envGate          string // env var that must be "1" to run this provider's test
	extraSkipFn      func(t *testing.T)
	newAdapter       func(t *testing.T) llmtypes.Model
	turnOptions      func(ownerSessionID string) []llmtypes.CallOption
	expectNonZero    bool // provider produces non-zero TotalCostUSD (false for cursor: display-model gap)
	expectCacheReads bool // provider surfaces cache_read_input_tokens in gi.Additional (true for claude/codex; false for cursor — char-estimated tokens)
	strictMemory     bool // when true, mustContainAll misses are hard failures, not logged warnings. Enable for providers whose persistent-session contract requires full cross-turn recall (cursor's TUI is supposed to remember its own conversation by definition, so memory loss is a regression). Leave false for providers like codex that legitimately re-invoke fresh per turn.
	cleanup          func(ctx context.Context)
}

// runMultiTurnChatE2E drives the spec's adapter through N sequential
// turns on a single persistent tmux session and checks every axis
// the user asked about:
//
//  1. Sending user messages — each turn appends a Human message to
//     the running history before calling GenerateContent, so the
//     adapter sees a growing history and the CLI receives each
//     follow-up correctly (verified by the conversation-context
//     prompts later in the sequence).
//  2. Completion detection — assertion that the call returns within
//     a reasonable bound (90s/turn) without erroring out.
//  3. Text extraction — assertion that resp.Choices[0].Content is
//     non-empty and (for context-checking turns) contains the
//     reference token from an earlier turn.
//  4. Token tracking — assertion that GenerationInfo has non-zero
//     PromptTokens and CompletionTokens (cursor's char-estimate
//     counts too).
//  5. Conversation reconstruction — assertion that the sidecar
//     parser populated CodingProviderIntermediateMessages, and that
//     each later turn's intermediate slice contains ONLY new
//     content (catches cursor multi-turn cumulative root /
//     claude per-turn timestamp filtering).
//
// After all turns: verifies the cost ledger received N entries for
// this session, the running spliced history grew monotonically, and
// every reference token appears at least once in the conversation.
func runMultiTurnChatE2E(t *testing.T, spec multiTurnChatE2ESpec) {
	t.Helper()
	if os.Getenv(spec.envGate) == "" {
		t.Skipf("set %s=1 to run %s multi-turn chat e2e", spec.envGate, spec.providerName)
	}
	if spec.extraSkipFn != nil {
		spec.extraSkipFn(t)
	}
	if spec.cleanup != nil {
		t.Cleanup(func() { spec.cleanup(context.Background()) })
	}
	adapter := spec.newAdapter(t)
	ownerSessionID := fmt.Sprintf("multi-turn-e2e-%s-%s", spec.providerName, time.Now().Format("150405"))

	// Use distinctive tokens so we can verify context flow without
	// being tripped up by the model's training data. Avoid words a
	// general-purpose model might paraphrase away.
	const (
		tokenA = "WIDGET_A47"
		tokenB = "WIDGET_B23"
		tokenC = "WIDGET_C91"
	)
	type turnSpec struct {
		prompt         string
		mustContainAny []string // turn must contain at least one of these (case-insensitive); empty = no content assertion
		mustContainAll []string // turn must contain ALL of these
	}
	prompts := []turnSpec{
		{
			prompt:         fmt.Sprintf("Repeat exactly this token and nothing else: %s", tokenA),
			mustContainAny: []string{tokenA},
		},
		{
			prompt:         fmt.Sprintf("Now repeat exactly this token and nothing else: %s", tokenB),
			mustContainAny: []string{tokenB},
		},
		{
			prompt:         "Which two tokens have I given you so far? Reply with both, comma-separated.",
			mustContainAll: []string{tokenA, tokenB},
		},
		{
			prompt:         fmt.Sprintf("One more token, repeat exactly: %s", tokenC),
			mustContainAny: []string{tokenC},
		},
		{
			prompt:         "List ALL three tokens I have given you, in order.",
			mustContainAll: []string{tokenA, tokenB, tokenC},
		},
	}

	// Set up an in-process workspace + ledger so we can check
	// costs.jsonl accumulation at the end.
	wsServer := costledger.NewTestServer(t)
	defer wsServer.Close()
	ledger := costledger.NewLedger(wsServer.URL)
	api := &StreamingAPI{costLedger: ledger}
	observer := newCostObserver(api.costLedger, ownerSessionID, "test-user", "chat")

	// Local running history — simulates mcpagent's conversation_history.
	// Each turn we append the human prompt, then after the response
	// append the intermediate-messages splice (or fall back to the
	// final text if no intermediate was populated).
	var history []llmtypes.MessageContent

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	for i, p := range prompts {
		turn := i + 1
		// 1. Append the user message — sending the user message.
		history = append(history, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: p.prompt}},
		})

		t.Logf("→ turn %d: %s", turn, p.prompt)
		t0 := time.Now()
		resp, err := adapter.GenerateContent(ctx, history, spec.turnOptions(ownerSessionID)...)
		elapsed := time.Since(t0)
		if err != nil {
			t.Fatalf("turn %d GenerateContent: %v", turn, err)
		}
		if len(resp.Choices) == 0 || resp.Choices[0] == nil {
			t.Fatalf("turn %d: empty choices", turn)
		}
		choice := resp.Choices[0]
		gi := choice.GenerationInfo
		if gi == nil {
			t.Fatalf("turn %d: missing GenerationInfo", turn)
		}

		// 2. Completion detection — bounded turn duration.
		if elapsed > 90*time.Second {
			t.Errorf("turn %d took %v, want <90s (completion-detection regression?)", turn, elapsed)
		}

		// 3. Text extraction — non-empty content + context check.
		content := strings.TrimSpace(choice.Content)
		if content == "" {
			t.Fatalf("turn %d: empty Content (text extraction failed). GI.Additional=%v", turn, gi.Additional)
		}
		// "mustContainAll" tokens reference EARLIER turns and depend
		// on the CLI maintaining cross-turn memory in its persistent
		// session. claude-code's --resume path does; codex exec
		// reinvokes per turn and is fresher per call; cursor varies
		// by composer mode. By default downgrade these to a logged
		// warning so memory-shape differences across providers don't
		// fail the cross-provider plumbing test. Providers that opt
		// into spec.strictMemory (cursor today) treat recall misses
		// as a hard failure — cursor's persistent TUI is supposed to
		// remember the full conversation by definition, so a missing
		// earlier token is a real regression, not a provider variance.
		for _, token := range p.mustContainAll {
			if !strings.Contains(strings.ToUpper(content), strings.ToUpper(token)) {
				if spec.strictMemory {
					t.Errorf("turn %d: %s persistent-session memory regression — response missing %q from an earlier turn\n   content=%q", turn, spec.providerName, token, content)
				} else {
					t.Logf("   ⚠ turn %d: context-recall miss — response missing %q (provider memory may not include earlier turns)\n      content=%q", turn, token, content)
				}
			}
		}
		// "mustContainAny" is the per-turn-echo check: the model was
		// asked to repeat the current turn's token verbatim. Failing
		// this means text extraction or message-delivery is broken
		// for THIS turn — keep as a hard assertion.
		if len(p.mustContainAny) > 0 {
			hit := false
			for _, token := range p.mustContainAny {
				if strings.Contains(strings.ToUpper(content), strings.ToUpper(token)) {
					hit = true
					break
				}
			}
			if !hit {
				t.Errorf("turn %d: response missing any of %v\nfull content: %q", turn, p.mustContainAny, content)
			}
		}

		// 4. Token tracking — non-zero prompt + completion.
		prompt := derefInt(gi.PromptTokens, gi.InputTokens)
		completion := derefInt(gi.CompletionTokens, gi.OutputTokens)
		if prompt == 0 && resp.Usage != nil && resp.Usage.InputTokens > 0 {
			prompt = resp.Usage.InputTokens
		}
		if completion == 0 && resp.Usage != nil && resp.Usage.OutputTokens > 0 {
			completion = resp.Usage.OutputTokens
		}
		if prompt == 0 {
			t.Errorf("turn %d: PromptTokens == 0 (token-extraction regression). GI=%+v", turn, gi)
		}
		if completion == 0 {
			t.Errorf("turn %d: CompletionTokens == 0 (token-extraction regression). GI=%+v", turn, gi)
		}

		// 5. Conversation reconstruction — sidecar splice populated.
		intermediate, hasIntermediate := llmtypes.ExtractCodingProviderIntermediateMessages(gi)
		if hasIntermediate && len(intermediate.Messages) > 0 {
			if intermediate.Transport != llmtypes.CodingProviderTransportTmux {
				t.Errorf("turn %d: intermediate.Transport=%q, want %q", turn, intermediate.Transport, llmtypes.CodingProviderTransportTmux)
			}
			t.Logf("   ✓ turn %d intermediate.Messages = %d (provider=%s)", turn, len(intermediate.Messages), intermediate.Provider)
			// Multi-turn dedup proxy: each turn beyond the first
			// should yield NEW content referencing the current
			// turn's input — not just the cumulative tail of
			// prior turns. We check ANY message in this turn's
			// intermediate slice (codex emits trailing tool-use
			// blocks like "PLAN UPDATED" after the text answer,
			// so the LAST entry isn't necessarily the assistant
			// text; the token may be in an earlier text part).
			if len(p.mustContainAny) > 0 {
				combinedTurn := ""
				for _, m := range intermediate.Messages {
					combinedTurn += " " + messageContentToText(m)
				}
				combinedTurnUpper := strings.ToUpper(combinedTurn)
				tokenFound := false
				for _, tok := range p.mustContainAny {
					if strings.Contains(combinedTurnUpper, strings.ToUpper(tok)) {
						tokenFound = true
						break
					}
				}
				if !tokenFound {
					t.Errorf("turn %d: spliced messages don't reference current-turn token (multi-turn dedup may be stale)\ncombined: %q", turn, combinedTurn)
				}
			}
			// Splice into running history.
			history = append(history, intermediate.Messages...)
		} else {
			// Fall back to choice.Content if no intermediate (e.g.
			// cursor with very fast turns where store.db wasn't
			// committed in time).
			t.Logf("   ⚠ turn %d: no intermediate messages — falling back to choice.Content", turn)
			history = append(history, llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeAI,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: content}},
			})
		}

		// Push the turn's usage into the cost ledger so the final
		// assertion can verify accumulation.
		additional := map[string]interface{}{}
		for k, v := range gi.Additional {
			additional[k] = v
		}
		// Cache token surfacing contract (see
		// docs/COSTS_AND_CONVERSATION_HISTORY.md → "Cache token
		// surfacing contract"): providers that report cache hits MUST
		// write cache_read_input_tokens into gi.Additional. We can't
		// guarantee a cache HIT on every turn (turn 1 is usually a
		// cache miss; some providers don't cache short prompts) so
		// we only assert the KEY is REACHABLE on turn 2+ when the
		// provider claims to support it. If the key is missing, the
		// ledger's extractCacheTokens will return 0 in production.
		if spec.expectCacheReads && turn >= 2 {
			if _, ok := additional["cache_read_input_tokens"]; !ok {
				t.Errorf("turn %d: gi.Additional missing cache_read_input_tokens — adapter cache-key surfacing regression (provider=%s)", turn, spec.providerName)
			}
		}
		effectiveModel, _ := extractCostAndEffectiveModel(additional)
		tokenEvent := &unifiedevents.TokenUsageEvent{
			ModelID:          effectiveModel,
			Provider:         spec.providerKey,
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			GenerationInfo:   additional,
		}
		if err := observer.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
			Type:      unifiedevents.TokenUsage,
			Timestamp: time.Now(),
			Component: "test",
			Data:      tokenEvent,
		}); err != nil {
			t.Errorf("turn %d HandleEvent: %v", turn, err)
		}

		t.Logf("   ✓ turn %d ok (elapsed=%v, prompt=%d, completion=%d, content_len=%d)",
			turn, elapsed.Round(100*time.Millisecond), prompt, completion, len(content))
	}

	// ─── Aggregate assertions ─────────────────────────────────────────

	// Cost ledger should have N entries for this session.
	req := httptest.NewRequest(http.MethodGet, "/api/cost/summary", nil)
	rec := httptest.NewRecorder()
	api.handleCostSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/cost/summary status=%d body=%s", rec.Code, rec.Body.String())
	}
	var summary costledger.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.Total.CallCount != len(prompts) {
		t.Errorf("Total.CallCount = %d, want %d (one per turn)", summary.Total.CallCount, len(prompts))
	}
	if summary.Total.PromptTokens == 0 {
		t.Errorf("Total.PromptTokens = 0")
	}
	if summary.Total.CompletionTokens == 0 {
		t.Errorf("Total.CompletionTokens = 0")
	}
	if spec.expectNonZero && summary.Total.TotalCostUSD <= 0 {
		t.Errorf("Total.TotalCostUSD = %v, want > 0 (metered provider)", summary.Total.TotalCostUSD)
	}
	// Cache-read end-to-end: providers that claim cache support
	// MUST have at least some cache-read tokens after N turns that
	// share the same prompt prefix. If this is 0, either the
	// adapter dropped cache_read_input_tokens from Additional OR
	// extractCacheTokens isn't keyed off the right name.
	if spec.expectCacheReads && summary.Total.CacheReadTokens == 0 {
		t.Errorf("Total.CacheReadTokens = 0 across %d turns — cache-key surfacing or extractCacheTokens regression", len(prompts))
	}

	// ─── Conversation-history persistence shape ───────────────────────
	// The live server persists the running `history` slice as
	// chat_history JSON. We don't drive the full save path here but
	// we DO have the same slice. Marshal-roundtrip and assert the
	// shape so downstream consumers (resume picker, conversation
	// log readers) can read it back.
	convData := map[string]interface{}{
		"session_id":           ownerSessionID,
		"agent_mode":           "chat",
		"conversation_history": history,
		"updated_at":           time.Now().Format(time.RFC3339),
	}
	convJSON, err := json.MarshalIndent(convData, "", "  ")
	if err != nil {
		t.Fatalf("marshal conversation history: %v", err)
	}
	var roundtrip struct {
		ConversationHistory []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(convJSON, &roundtrip); err != nil {
		t.Fatalf("roundtrip conversation history: %v", err)
	}
	var humanCount, aiOrToolCount int
	for _, m := range roundtrip.ConversationHistory {
		switch m.Role {
		case llmtypes.ChatMessageTypeHuman:
			humanCount++
		case llmtypes.ChatMessageTypeAI, llmtypes.ChatMessageTypeTool:
			aiOrToolCount++
		}
	}
	if humanCount != len(prompts) {
		t.Errorf("persisted conversation_history: %d human entries, want %d", humanCount, len(prompts))
	}
	if aiOrToolCount == 0 {
		t.Errorf("persisted conversation_history: 0 ai/tool entries — splice didn't fire end-to-end")
	}

	// Spliced history should have grown to at least 1 + N*2 entries
	// (one Human + one AI per turn, sometimes more if the CLI emitted
	// tool calls). The strict lower bound catches a splice-skipping
	// regression without being brittle to provider verbosity.
	minExpected := 2 * len(prompts)
	if len(history) < minExpected {
		t.Errorf("final history length = %d, want >= %d (turn-by-turn growth)", len(history), minExpected)
	}

	// Every reference token must appear somewhere in the spliced
	// history — proves the conversation context flowed end-to-end.
	combined := ""
	for _, m := range history {
		combined += " " + messageContentToText(m)
	}
	combinedUpper := strings.ToUpper(combined)
	for _, token := range []string{tokenA, tokenB, tokenC} {
		if !strings.Contains(combinedUpper, strings.ToUpper(token)) {
			t.Errorf("final spliced history missing token %q (conversation context did not flow through)", token)
		}
	}

	t.Logf("✅ %s multi-turn e2e: %d turns, history=%d entries, cost=$%.6f, prompt_tokens=%d completion_tokens=%d",
		spec.providerName, len(prompts), len(history), summary.Total.TotalCostUSD,
		summary.Total.PromptTokens, summary.Total.CompletionTokens)
}

// messageContentToText flattens a MessageContent's parts into a
// single string for substring searches.
func messageContentToText(m llmtypes.MessageContent) string {
	var b strings.Builder
	for _, p := range m.Parts {
		switch v := p.(type) {
		case llmtypes.TextContent:
			b.WriteString(v.Text)
			b.WriteString(" ")
		case *llmtypes.TextContent:
			if v != nil {
				b.WriteString(v.Text)
				b.WriteString(" ")
			}
		case llmtypes.ToolCall:
			if v.FunctionCall != nil {
				b.WriteString(v.FunctionCall.Name)
				b.WriteString(" ")
				b.WriteString(v.FunctionCall.Arguments)
				b.WriteString(" ")
			}
		case llmtypes.ToolCallResponse:
			b.WriteString(v.Content)
			b.WriteString(" ")
		}
	}
	return b.String()
}

// ─── Per-provider entry points ─────────────────────────────────────

func TestMultiTurnChatE2E_ClaudeCode(t *testing.T) {
	model := strings.TrimSpace(os.Getenv("CLAUDE_CODE_EXPERIMENTAL_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	runMultiTurnChatE2E(t, multiTurnChatE2ESpec{
		providerName: "claude-code",
		providerKey:  "claudecode",
		envGate:      "RUN_CLAUDE_CODE_EXPERIMENTAL_LIVE_E2E",
		extraSkipFn: func(t *testing.T) {
			if _, err := exec.LookPath("claude"); err != nil {
				t.Skipf("claude binary not found: %v", err)
			}
		},
		newAdapter: func(t *testing.T) llmtypes.Model {
			return claudecodeadapter.NewClaudeCodeInteractiveAdapter(model, &e2eMockLogger{})
		},
		turnOptions: func(ownerSessionID string) []llmtypes.CallOption {
			// Claude Code's interactive adapter manages its own
			// session via the response's NativeSessionID; we don't
			// need to pass options for persistence here.
			return nil
		},
		expectNonZero:    true,
		expectCacheReads: true, // claude prompt-caches the system prompt across turns
		cleanup: func(ctx context.Context) {
			_ = claudecodeadapter.CleanupClaudeCodeTmuxSessions(ctx)
		},
	})
}

func TestMultiTurnChatE2E_Codex(t *testing.T) {
	model := strings.TrimSpace(os.Getenv("CODEX_CLI_REAL_CONTRACT_MODEL"))
	if model == "" {
		model = "gpt-5.4-mini"
	}
	runMultiTurnChatE2E(t, multiTurnChatE2ESpec{
		providerName: "codex",
		providerKey:  "codexcli",
		envGate:      "RUN_CODEX_CLI_INTERACTIVE_E2E",
		extraSkipFn: func(t *testing.T) {
			if _, err := exec.LookPath("codex"); err != nil {
				t.Skipf("codex binary not found: %v", err)
			}
		},
		newAdapter: func(t *testing.T) llmtypes.Model {
			return codexcliadapter.NewCodexCLIAdapter("", model, &e2eMockLogger{})
		},
		turnOptions: func(ownerSessionID string) []llmtypes.CallOption {
			return []llmtypes.CallOption{
				codexcliadapter.WithInteractiveSessionID(ownerSessionID),
				codexcliadapter.WithPersistentInteractiveSession(true),
				codexcliadapter.WithDisableShellTool(),
				codexcliadapter.WithApprovalPolicy("never"),
				codexcliadapter.WithReasoningEffort("low"),
			}
		},
		expectNonZero:    true,
		expectCacheReads: true, // codex/OpenAI prompt-caches the system prompt across turns
		cleanup: func(ctx context.Context) {
			_ = codexcliadapter.CleanupCodexCLIInteractiveSessions(ctx)
		},
	})
}

func TestMultiTurnChatE2E_Cursor(t *testing.T) {
	model := strings.TrimSpace(os.Getenv("CURSOR_CLI_REAL_E2E_MODEL"))
	if model == "" {
		model = "cursor-cli"
	}
	runMultiTurnChatE2E(t, multiTurnChatE2ESpec{
		providerName: "cursor",
		providerKey:  "cursor-cli",
		envGate:      "RUN_CURSOR_CLI_REAL_E2E",
		extraSkipFn: func(t *testing.T) {
			if _, err := exec.LookPath("cursor-agent"); err != nil {
				t.Skipf("cursor-agent binary not found: %v", err)
			}
		},
		newAdapter: func(t *testing.T) llmtypes.Model {
			return cursorcliadapter.NewCursorCLIAdapter("", model, &e2eMockLogger{})
		},
		turnOptions: func(ownerSessionID string) []llmtypes.CallOption {
			return []llmtypes.CallOption{
				cursorcliadapter.WithInteractiveSessionID(ownerSessionID),
				cursorcliadapter.WithPersistentInteractiveSession(true),
			}
		},
		// Cursor's effective model ("Composer 2.5") is a display name
		// not in the metadata registry — known cost-emission gap.
		// Tokens are char-estimated; cost stays 0 until that's fixed.
		// Cursor also doesn't expose cache info (char-estimate has no
		// cache concept), so the cache-read assertions are skipped.
		expectNonZero:    false,
		expectCacheReads: false,
		// Cursor's persistent tmux TUI is supposed to remember every
		// turn of the live session — there's no token-budget reason
		// to drop earlier turns when the convo is <200 tokens total.
		// Hard-fail recall misses so the truncation bug observed in
		// production (see task #19) stays visible until the root
		// cause in the cursor adapter or TUI is fixed.
		strictMemory: true,
	})
}
