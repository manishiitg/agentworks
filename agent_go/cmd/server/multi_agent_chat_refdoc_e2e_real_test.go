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

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// TestMultiAgentChatPromptSteersToReferenceDocs is a real-LLM e2e test for
// the multi-agent chat prompt refactor. It verifies that the new
// secret cheat-sheet+pointer pattern actually steers the LLM to
// call read_skill(skills=[{"name":"builder-reference","path":"references/....md"}]) before performing rare-path actions,
// instead of inventing the file format / tool semantics from memory.
//
// What it does:
//  1. Builds the same system prompt the multi-agent chat session would see
//     (GetMultiAgentDelegationInstructionsWithUser).
//  2. Defines read_skill with mcpagent's intrinsic attached-skill schema.
//  3. Sends a "store a secret" user message via the Anthropic API with
//     claude-haiku.
//  4. Asserts the model's first response contains a tool_use block for
//     read_skill with the expected bundled path. The system prompt pointer
//     ("call read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/secret-management.md\"}]) first") only
//     produces correct behavior if the LLM actually parses and acts on it.
//
// A sibling "schedule a daily task" case was removed 2026-08-27: multi-agent
// chat's own global schedule runtime was deleted (see
// TestGetMultiAgentDelegationInstructionsLazyLoadsSecret's "removed"
// assertions in delegation_tools_test.go), and there is no production code
// anywhere that registers a multi-agent-chat-scoped schedule tool. This case
// had drifted stale behind this test's cost gate without anyone noticing.
// Workflow-level scheduling (create_schedule/update_schedule/
// create_calendar_schedule) is a separate, still-live capability documented
// under references/workflow-tools.md, not this one.
//
// Gating:
//   - RUN_MULTIAGENT_REFDOC_E2E=1 to run (off by default — costs API tokens).
//   - ANTHROPIC_API_KEY required.
//   - ANTHROPIC_REFDOC_MODEL optional override (defaults to claude-haiku-4-5,
//     the cheapest model that's still smart enough to follow tool guidance).
//
// Cost: roughly $0.001 per case (one case, ~10k input tokens on
// claude-haiku-4-5).
func TestMultiAgentChatPromptSteersToReferenceDocs(t *testing.T) {
	if os.Getenv("RUN_MULTIAGENT_REFDOC_E2E") == "" {
		t.Skip("set RUN_MULTIAGENT_REFDOC_E2E=1 to run this real-LLM e2e (costs API tokens)")
	}
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY required")
	}
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_REFDOC_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5"
	}

	systemPrompt := virtualtools.GetMultiAgentDelegationInstructionsWithUser("Chats", "default")

	// read_skill is intrinsic to mcpagent whenever a skill is attached. Keep
	// this direct-API probe aligned with that stable transport-neutral schema.
	tools := []anthropic.ToolUnionParam{
		{
			OfTool: &anthropic.ToolParam{
				Name:        "read_skill",
				Description: anthropic.String("Read an attached skill or one of its bundled supporting files."),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: map[string]any{
						"skill_name": map[string]any{
							"type":        "string",
							"enum":        []string{"builder-reference"},
							"description": "Exact attached skill name.",
						},
						"path": map[string]any{
							"type": "string",
							"enum": []string{"references/secret-management.md"},
						},
					},
					Required: []string{"skill_name", "path"},
				},
			},
		},
	}

	type caseSpec struct {
		name       string
		userMsg    string
		expectKind string
	}
	cases := []caseSpec{
		{
			name:       "secret_storage_request_triggers_secret_doc_load",
			userMsg:    "Please store my Slack API token. The value is sk-test-1234-fake-not-real. Save it as SLACK_TOKEN.",
			expectKind: "secret-management",
		},
	}

	client := anthropic.NewClient(anthropicoption.WithAPIKey(apiKey))

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
				Model:     anthropic.Model(model),
				MaxTokens: 1024,
				System: []anthropic.TextBlockParam{
					{Text: systemPrompt},
				},
				Messages: []anthropic.MessageParam{
					anthropic.NewUserMessage(anthropic.NewTextBlock(tc.userMsg)),
				},
				Tools: tools,
			})
			if err != nil {
				t.Fatalf("anthropic Messages.New: %v", err)
			}

			t.Logf("response stop_reason=%q, content blocks=%d", msg.StopReason, len(msg.Content))

			foundRefDocCall := false
			var calledPath string
			for _, block := range msg.Content {
				switch v := block.AsAny().(type) {
				case anthropic.ToolUseBlock:
					if v.Name == "read_skill" {
						var input struct {
							SkillName string `json:"skill_name"`
							Path      string `json:"path"`
						}
						if err := json.Unmarshal(v.Input, &input); err != nil {
							t.Errorf("decode tool input: %v (raw=%s)", err, string(v.Input))
							continue
						}
						calledPath = input.Path
						if input.SkillName == "builder-reference" && input.Path == "references/"+tc.expectKind+".md" {
							foundRefDocCall = true
						}
					}
				case anthropic.TextBlock:
					// For debugging — show what the model said before/instead of the tool call.
					if v.Text != "" {
						t.Logf("model text: %s", truncateRefdocLog(v.Text, 240))
					}
				}
			}

			if !foundRefDocCall {
				t.Errorf("expected read_skill skills item for builder-reference/references/%s.md before performing action; calledPath=%q stop_reason=%q",
					tc.expectKind, calledPath, msg.StopReason)
				t.Logf("Full response blocks: %s", dumpBlocks(msg.Content))
			} else {
				t.Logf("✅ agent called read_skill with path references/%s.md before action", tc.expectKind)
			}
		})
	}
}

// truncateRefdocLog shortens a long string for test log lines so the failure
// message stays readable.
func truncateRefdocLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// dumpBlocks renders message content blocks compactly for debugging.
func dumpBlocks(blocks []anthropic.ContentBlockUnion) string {
	var sb strings.Builder
	for i, b := range blocks {
		switch v := b.AsAny().(type) {
		case anthropic.ToolUseBlock:
			sb.WriteString(fmt.Sprintf("[%d] tool_use name=%q input=%s\n", i, v.Name, string(v.Input)))
		case anthropic.TextBlock:
			sb.WriteString(fmt.Sprintf("[%d] text=%q\n", i, truncateRefdocLog(v.Text, 200)))
		default:
			sb.WriteString(fmt.Sprintf("[%d] %T\n", i, v))
		}
	}
	return sb.String()
}

// TestMultiAgentChatPromptSteersToReferenceDocs_ClaudeCode is the local-CLI
// variant of the refdoc steering test. It uses the Claude Code CLI adapter
// (which routes through the user's local `claude` binary and consumes the
// user's Claude subscription — no ANTHROPIC_API_KEY needed).
//
// Unlike the Anthropic SDK variant, Claude Code CLI does its tool calling
// via MCP servers configured externally; the adapter does NOT accept
// inline tool definitions. So this test can't verify a literal tool_use
// block was emitted. Instead it verifies the **model's stated intent** —
// does the text response mention calling `read_skill(skills=[{"name":"builder-reference","path":"references/....md"}])`
// with the expected kind? That proves the inline cheat-sheet pointer is
// strong enough to steer the LLM's plan, which is what we care about.
//
// Gating:
//   - RUN_MULTIAGENT_REFDOC_CC_E2E=1 to run.
//   - `claude` CLI binary must be on PATH.
//   - CLAUDE_CODE_REFDOC_MODEL override (default: claude-haiku-5-5).
//
// Cost: uses the user's local Claude subscription, not API credits.
func TestMultiAgentChatPromptSteersToReferenceDocs_ClaudeCode(t *testing.T) {
	if os.Getenv("RUN_MULTIAGENT_REFDOC_CC_E2E") == "" {
		t.Skip("set RUN_MULTIAGENT_REFDOC_CC_E2E=1 to run this claude-code CLI e2e")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude binary not found on PATH: %v", err)
	}
	model := strings.TrimSpace(os.Getenv("CLAUDE_CODE_REFDOC_MODEL"))
	if model == "" {
		model = "claude-haiku-5-5"
	}

	systemPrompt := virtualtools.GetMultiAgentDelegationInstructionsWithUser("Chats", "default")
	adapter := claudecodeadapter.NewClaudeCodeInteractiveAdapter(model, &e2eMockLogger{})
	t.Cleanup(func() {
		_ = claudecodeadapter.CleanupClaudeCodeTmuxSessions(context.Background())
	})

	type caseSpec struct {
		name        string
		userMsg     string
		expectKind  string
		mustMention []string // additional phrases that should appear in the response
	}
	cases := []caseSpec{
		{
			name:        "secret_storage_describes_secret_doc_load",
			userMsg:     "I want to save a Slack API token as SLACK_TOKEN. Walk me through exactly what tools you would call, in order, to store it correctly. Be specific about tool names and arguments.",
			expectKind:  "secret-management",
			mustMention: []string{"read_skill"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()

			resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
				{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}}},
				{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: tc.userMsg}}},
			})
			if err != nil {
				t.Fatalf("claude-code GenerateContent: %v", err)
			}
			if len(resp.Choices) == 0 {
				t.Fatal("no choices in response")
			}
			text := resp.Choices[0].Content
			t.Logf("model response (%d chars):\n%s", len(text), truncateRefdocLog(text, 1200))

			// Stated-intent check: does the response mention calling
			// read_skill with the expected kind? Allow common
			// formatting variations (kind=..., kind: ..., kind:"...").
			lower := strings.ToLower(text)
			kindMentioned := strings.Contains(lower, strings.ToLower(tc.expectKind))
			refDocMentioned := strings.Contains(lower, "read_skill")
			if !refDocMentioned {
				t.Errorf("expected response to mention read_skill; got text=%s", truncateRefdocLog(text, 600))
			}
			if !kindMentioned {
				t.Errorf("expected response to mention kind %q; got text=%s", tc.expectKind, truncateRefdocLog(text, 600))
			}
			for _, phrase := range tc.mustMention {
				if !strings.Contains(lower, strings.ToLower(phrase)) {
					t.Errorf("expected response to mention %q; got text=%s", phrase, truncateRefdocLog(text, 600))
				}
			}
			if refDocMentioned && kindMentioned {
				t.Logf("✅ model stated intent to call read_skill with path references/%s.md before action", tc.expectKind)
			}
		})
	}
}

// TestMultiAgentChatFullConversation_ClaudeCode is a single multi-turn
// conversation that exercises every capability the multi-agent chat prompt
// is supposed to surface: schedule management, secret management,
// employees + workflow assignments context, and workflow
// context inspection. The whole flow runs in ONE conversation (the adapter
// threads claude-code's native session via NativeSessionID), so each turn
// gets to depend on prior context if the LLM is steered correctly.
//
// What this proves end-to-end:
//   - The full assembled system prompt (delegation rules + synthetic
//     employees/workflow context) holds together
//     and steers the LLM on every relevant axis.
//   - read_skill pointers actually steer the model on schedule and
//     secret asks (same as the per-capability tests above, but verified in
//     a real session not just a one-shot).
//   - Auto-injected employees context lets the model resolve "who handles
//     X workflow?" by name without re-asking the user.
//   - Auto-injected workflow context lets the model describe a workflow's
//     purpose / structure without inventing details.
//
// Gating:
//   - RUN_MULTIAGENT_REFDOC_CC_E2E=1 (shared with the per-capability test).
//   - `claude` binary on PATH.
//
// Cost: uses Claude subscription, not API credits.
func TestMultiAgentChatFullConversation_ClaudeCode(t *testing.T) {
	if os.Getenv("RUN_MULTIAGENT_REFDOC_CC_E2E") == "" {
		t.Skip("set RUN_MULTIAGENT_REFDOC_CC_E2E=1 to run this claude-code multi-turn e2e")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude binary not found on PATH: %v", err)
	}
	model := strings.TrimSpace(os.Getenv("CLAUDE_CODE_REFDOC_MODEL"))
	if model == "" {
		model = "claude-haiku-5-5"
	}

	// Build the full system prompt the chat session would normally see.
	// Delegation prompt: real (cheat sheets + pointers).
	// Employees + workflow context: synthetic — production reads files
	// at request time, but for an isolated e2e test we inject fixed
	// content with distinctive names/tokens we can grep for.
	delegationPrompt := virtualtools.GetMultiAgentDelegationInstructionsWithUser("Chats", "default")
	const employeeName = "Priya"
	const employeeWorkflow = "Workflow/bot-whatsapp-customer-support"
	const workflowDescription = "an automated customer-support bot that triages incoming WhatsApp messages, classifies intent, and routes to the right handler step (refund, escalation, FAQ-bot, human-agent)"
	employeesSection := `
## Current Employees & Workflow Assignments

This workspace has the following employees with their assigned workflows. If the user's message names any employee below, treat that employee's assigned workflows as the primary source of truth and inspect the relevant workflow folder to ground your answer.

- **` + employeeName + `** (` + "`emp-001`" + `)
  - ` + "`" + employeeWorkflow + "`" + `
- **Arjun** (` + "`emp-002`" + `)
  - ` + "`Workflow/data-ingestion-pipeline`" + `
`
	workflowContextSection := `
## Workflow Context (Read-Only)

The following workflow(s) have been selected as reference context for this conversation. You have read-only access — read files, list directories, but cannot modify.

### Workflow: bot-whatsapp-customer-support
**Workspace Path:** ` + "`" + employeeWorkflow + "/`" + `

**Workflow Manifest (workflow.json):**
This workflow is ` + workflowDescription + `. It runs continuously and updates ` + "`db/whatsapp_tickets.json`" + ` with each handled message. Owner: ` + employeeName + `.

**Key Steps:**
- step-ingest-whatsapp: pulls new WhatsApp messages from the inbox
- step-classify-intent: LLM classifies each message into one of (refund, escalation, faq, human-handoff)
- step-route-handler: dispatches each message to the appropriate sub-agent
`
	systemPrompt := delegationPrompt + employeesSection + workflowContextSection

	adapter := claudecodeadapter.NewClaudeCodeInteractiveAdapter(model, &e2eMockLogger{})
	t.Cleanup(func() {
		_ = claudecodeadapter.CleanupClaudeCodeTmuxSessions(context.Background())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	// In-process workspace API + cost ledger so we can verify cost
	// + conversation persistence end-to-end. Mirrors what the live
	// server does after every chat turn: token_usage event → cost
	// observer → costs.jsonl entry; full history → chat_history JSON.
	wsServer := costledger.NewTestServer(t)
	defer wsServer.Close()
	api := &StreamingAPI{costLedger: costledger.NewLedger(wsServer.URL)}
	const testSessionID = "multi-agent-chat-full-conv-e2e"
	observer := newCostObserver(api.costLedger, testSessionID, "default", "multi-agent")

	// Running conversation history. The system prompt is set on the first
	// message; subsequent turns inherit it via the claude-code session.
	history := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}}},
	}

	type turn struct {
		name           string
		userMsg        string
		mustMentionAny [][]string // groups: response must contain at least one phrase from EACH group
		mustNotMention []string   // response must NOT contain these (red flags)
	}
	turns := []turn{
		{
			name:    "1_secret_storage",
			userMsg: "Hi. I want to save a Slack API token as SLACK_TOKEN. What tool do you call first, before doing anything else?",
			mustMentionAny: [][]string{
				{"read_skill"},
				{"secret-management"},
			},
		},
		{
			name:    "2_employees_lookup",
			userMsg: "Who handles the bot-whatsapp-customer-support workflow? Use the employee context you already have — don't ask me, just answer from what's loaded.",
			mustMentionAny: [][]string{
				// Either name or workflow path. Should NOT invent another name.
				{strings.ToLower(employeeName)},
			},
			mustNotMention: []string{
				"I don't know", "I don't have", "cannot find", "no information",
			},
		},
		{
			name:    "3_workflow_context_inspect",
			userMsg: "Briefly describe what the bot-whatsapp-customer-support workflow does, based on the workflow context loaded in this session. Don't make anything up — only use what's already in the system prompt.",
			mustMentionAny: [][]string{
				// Should reference the distinctive content from the synthetic workflow context.
				{"whatsapp", "customer-support", "triage", "refund", "escalation", "faq"},
			},
		},
	}

	successCount := 0
	for i, turn := range turns {
		turnNum := i + 1
		t.Run(turn.name, func(t *testing.T) {
			history = append(history, llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: turn.userMsg}},
			})

			t.Logf("→ turn %d: %s", turnNum, truncateRefdocLog(turn.userMsg, 200))
			t0 := time.Now()
			resp, err := adapter.GenerateContent(ctx, history)
			elapsed := time.Since(t0)
			if err != nil {
				t.Fatalf("turn %d GenerateContent: %v", turnNum, err)
			}
			if len(resp.Choices) == 0 {
				t.Fatalf("turn %d: no choices", turnNum)
			}
			text := resp.Choices[0].Content
			t.Logf("← turn %d (%v): %s", turnNum, elapsed, truncateRefdocLog(text, 800))

			// ─── Cost ledger per-turn assertions ─────────────────────
			// Drive the costObserver the same way the real server does:
			// build a TokenUsageEvent from the response's GenerationInfo
			// and feed it through. Then verify the resulting ledger
			// entry has the fields we care about populated.
			gi := resp.Choices[0].GenerationInfo
			if gi == nil {
				t.Fatalf("turn %d: GenerationInfo nil — cost ledger plumbing can't fire", turnNum)
			}
			promptTok := derefInt(gi.PromptTokens, gi.InputTokens)
			completionTok := derefInt(gi.CompletionTokens, gi.OutputTokens)
			if promptTok == 0 && resp.Usage != nil && resp.Usage.InputTokens > 0 {
				promptTok = resp.Usage.InputTokens
			}
			if completionTok == 0 && resp.Usage != nil && resp.Usage.OutputTokens > 0 {
				completionTok = resp.Usage.OutputTokens
			}
			if promptTok == 0 {
				t.Errorf("turn %d: PromptTokens unpopulated on gi — adapter regression", turnNum)
			}
			if completionTok == 0 {
				t.Errorf("turn %d: CompletionTokens unpopulated on gi — adapter regression", turnNum)
			}
			additional := map[string]interface{}{}
			for k, v := range gi.Additional {
				additional[k] = v
			}
			effectiveModel, _ := extractCostAndEffectiveModel(additional)
			if effectiveModel == "" {
				effectiveModel = model
			}
			// Cache token surfacing contract (per docs/COSTS_AND_CONVERSATION_HISTORY.md):
			// claude-code MUST populate cache_read_input_tokens in
			// gi.Additional, not just the typed CachedContentTokens
			// field. Turn 1 might be a cache miss; turns 2+ that
			// reuse the system prompt should hit the cache and
			// have the key populated.
			if turnNum > 1 {
				if _, ok := additional["cache_read_input_tokens"]; !ok {
					t.Errorf("turn %d: gi.Additional missing cache_read_input_tokens — claude adapter cache-key surfacing regression", turnNum)
				}
			}
			tokenEvent := &unifiedevents.TokenUsageEvent{
				ModelID:          effectiveModel,
				Provider:         "claudecode",
				PromptTokens:     promptTok,
				CompletionTokens: completionTok,
				TotalTokens:      promptTok + completionTok,
				GenerationInfo:   additional,
			}
			if err := observer.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
				Type:      unifiedevents.TokenUsage,
				Timestamp: time.Now(),
				Component: "test",
				Data:      tokenEvent,
			}); err != nil {
				t.Errorf("turn %d HandleEvent: %v", turnNum, err)
			}

			// Append assistant response to history so subsequent turns
			// see prior context.
			history = append(history, llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeAI,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
			})

			lower := strings.ToLower(text)
			passed := true
			for _, group := range turn.mustMentionAny {
				hit := false
				for _, phrase := range group {
					if strings.Contains(lower, strings.ToLower(phrase)) {
						hit = true
						break
					}
				}
				if !hit {
					t.Errorf("turn %d (%s): response missing any of %v\n  response: %s", turnNum, turn.name, group, truncateRefdocLog(text, 500))
					passed = false
				}
			}
			for _, bad := range turn.mustNotMention {
				if strings.Contains(lower, strings.ToLower(bad)) {
					t.Errorf("turn %d (%s): response contains forbidden phrase %q\n  response: %s", turnNum, turn.name, bad, truncateRefdocLog(text, 500))
					passed = false
				}
			}
			if passed {
				successCount++
				t.Logf("✅ turn %d passed", turnNum)
			}
		})
	}

	t.Logf("Final: %d/%d turns passed", successCount, len(turns))

	// ─── Aggregate cost-ledger assertions ─────────────────────────────
	// The cost-summary HTTP endpoint is what the dashboard reads. If
	// /api/cost/summary doesn't see N entries here, the chat in
	// production won't either. This is the assertion the per-turn
	// loop above feeds.
	req := httptest.NewRequest(http.MethodGet, "/api/cost/summary", nil)
	rec := httptest.NewRecorder()
	api.handleCostSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/cost/summary status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var summary costledger.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode cost summary: %v\nbody=%s", err, rec.Body.String())
	}
	if summary.Total.CallCount != len(turns) {
		t.Errorf("cost summary CallCount = %d, want %d (one entry per turn)", summary.Total.CallCount, len(turns))
	}
	if summary.Total.PromptTokens == 0 {
		t.Errorf("cost summary PromptTokens = 0 — accumulateTokenUsage or extractUsageMetrics regression")
	}
	if summary.Total.CompletionTokens == 0 {
		t.Errorf("cost summary CompletionTokens = 0")
	}
	// Cache reads should have flowed through for turns 2+ that share
	// the long system prompt. If this is 0 across all 6 turns,
	// either the adapter dropped cache_read_input_tokens from
	// Additional OR the ledger pipeline isn't reading it. Either
	// is the gap the live chat hit.
	if summary.Total.CacheReadTokens == 0 {
		t.Errorf("cost summary CacheReadTokens = 0 — cache-key surfacing or extractCacheTokens regression (every turn reuses the same long system prompt, so cache hits should be inevitable)")
	}
	// Anthropic is metered; total cost must be positive.
	if summary.Total.TotalCostUSD <= 0 {
		t.Errorf("cost summary TotalCostUSD = %v — cost-pricing regression (no effective_model match in metadata?)", summary.Total.TotalCostUSD)
	}
	if _, ok := summary.ByModel[model]; !ok {
		keys := make([]string, 0, len(summary.ByModel))
		for k := range summary.ByModel {
			keys = append(keys, k)
		}
		t.Errorf("summary.ByModel missing %q — effective_model wiring broken; got buckets %v", model, keys)
	}
	t.Logf("✅ cost summary: %d entries, prompt=%d completion=%d cache_read=%d cost=$%.6f",
		summary.Total.CallCount, summary.Total.PromptTokens, summary.Total.CompletionTokens,
		summary.Total.CacheReadTokens, summary.Total.TotalCostUSD)

	// ─── Conversation-history persistence shape check ──────────────────
	// The live server saves to:
	//   workspace-docs/_users/<user>/chat_history/<YYYY-MM-DD>/session-<sid>-conversation.json
	// We don't drive the full save path here (that requires the
	// streaming pipeline), but we DO have the same `history` slice
	// the server would persist. Encode it the same way and assert
	// the shape so a downstream consumer (the frontend resume picker,
	// /api/chat_history endpoints) can read it back.
	convData := map[string]interface{}{
		"session_id":           testSessionID,
		"agent_mode":           "multi-agent",
		"conversation_history": history,
		"updated_at":           time.Now().Format(time.RFC3339),
	}
	convJSON, err := json.MarshalIndent(convData, "", "  ")
	if err != nil {
		t.Fatalf("marshal conversation history: %v", err)
	}
	// Sanity: at minimum the persisted file must include every turn's
	// human message and every assistant response. Decode and inspect.
	var roundtrip struct {
		SessionID           string                    `json:"session_id"`
		ConversationHistory []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(convJSON, &roundtrip); err != nil {
		t.Fatalf("roundtrip conversation history: %v", err)
	}
	humanCount, aiCount := 0, 0
	for _, m := range roundtrip.ConversationHistory {
		switch m.Role {
		case llmtypes.ChatMessageTypeHuman:
			humanCount++
		case llmtypes.ChatMessageTypeAI:
			aiCount++
		}
	}
	if humanCount != len(turns) {
		t.Errorf("persisted conversation_history: %d human entries, want %d (one per turn)", humanCount, len(turns))
	}
	if aiCount != len(turns) {
		t.Errorf("persisted conversation_history: %d ai entries, want %d (one per turn)", aiCount, len(turns))
	}
	t.Logf("✅ conversation_history persistence shape: %d entries (%d human + %d ai + 1 system)",
		len(roundtrip.ConversationHistory), humanCount, aiCount)
}
