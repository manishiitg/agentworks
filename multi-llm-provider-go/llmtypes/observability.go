package llmtypes

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ObservabilityConfig collects the inputs WithObservability needs to
// build both the inspector emitter and the synthetic terminal.
//
// Adapters fill this in once at the top of GenerateContent. The helper
// then handles every cross-cutting concern (inspector request/completion
// events, synthetic-terminal banner + done line, error reporting) so
// each new provider gets observability for free.
type ObservabilityConfig struct {
	Provider     string       // canonical provider name: "anthropic", "openai", "vertex", "azure", "claudecode", "codex-cli", etc.
	Model        string       // model ID actually being called (post-resolution)
	Opts         *CallOptions // the caller's CallOptions; carries InspectorSink + StreamChan
	MessageCount int          // len(messages) before any provider-specific conversion
	HeaderLine   string       // a single-line "command" string for the terminal banner (e.g. "openai.chat.completions model=gpt-5 msgs=3")
	// Optional: full message list. When set, WithObservability extracts
	// the last user/human message and emits a "> user: ..." line
	// into the synthetic terminal so the pane shows the prompt that
	// was sent, not just the response. Truncated to keep the pane
	// readable; full prompt remains in the inspector request event.
	Messages []MessageContent

	// Optional: extra request-phase metadata merged into the inspector
	// request event. Standard keys (message_count, max_tokens, tool_count,
	// json_mode, streaming) are populated automatically — only set this
	// for provider-specific extras (e.g. "transport": "structured_cli").
	RequestMetaExtra map[string]interface{}

	// Optional: provider-specific completion-meta enrichment, called
	// after the body returns successfully. Receives the response and
	// the already-populated base meta (duration + tokens + stop_reason).
	// Mutate / append to add provider-specific keys (cache stats, etc.).
	EnrichCompletionMeta func(*ContentResponse, map[string]interface{})

	// Optional: override the terminal Done summary string. Default uses
	// "{N} in · {M} out" from the response's Usage or GenerationInfo.
	SummaryFn func(*ContentResponse) string
}

// AdapterBody is the body of an adapter's GenerateContent. It receives
// a StreamSink that fans every emitted chunk to (a) the caller's
// StreamChan, (b) the synthetic terminal, and (c) the inspector. The
// adapter MUST publish chunks through sink.Emit(ctx, chunk) — that's
// the single point of routing for the unified observability surface.
//
// sink.Term and sink.Inspector are exposed for the rare provider-
// specific case (e.g. emitting a phase event the auto-router doesn't
// cover) but normal streaming code shouldn't reach for them.
type AdapterBody func(sink *StreamSink) (*ContentResponse, error)

// WithObservability is the standard wrapper every adapter's
// GenerateContent should use. It guarantees consistent
// inspector + synthetic-terminal behavior across all providers.
//
// Contract for adapter authors:
//
//	func (a *MyAdapter) GenerateContent(ctx, messages, options...) (*ContentResponse, error) {
//	    opts := parseOptions(options...)
//	    return llmtypes.WithObservability(ctx, llmtypes.ObservabilityConfig{
//	        Provider:     "myprovider",
//	        Model:        modelID,
//	        Opts:         opts,
//	        MessageCount: len(messages),
//	        HeaderLine:   fmt.Sprintf("myprovider.call model=%s msgs=%d", modelID, len(messages)),
//	    }, func(term *llmtypes.SyntheticTerminal, inspector *llmtypes.InspectorEmitter) (*llmtypes.ContentResponse, error) {
//	        // ... adapter body. Call term.AssistantText/ToolStart and
//	        // inspector.EmitEvent inside the stream loop.
//	        return resp, nil
//	    })
//	}
//
// New providers added under this contract get inspector + terminal
// for free. Don't reimplement these boundaries by hand — drift across
// adapters is the bug this helper exists to prevent.
func WithObservability(ctx context.Context, cfg ObservabilityConfig, body AdapterBody) (*ContentResponse, error) {
	_ = ctx // reserved for future use (cancellation-aware metrics, deadlines)
	if cfg.Opts == nil {
		// Defensive — every adapter parses options before calling us,
		// but a nil here would crash on InspectorSink dereference.
		return body(&StreamSink{
			Term:      NewSyntheticTerminal(nil, cfg.Provider, cfg.Model),
			Inspector: NewInspectorEmitter(nil, cfg.Provider, cfg.Model),
		})
	}

	inspector := NewInspectorEmitter(cfg.Opts.InspectorSink, cfg.Provider, cfg.Model)
	reqMeta := map[string]interface{}{
		"message_count": cfg.MessageCount,
		"max_tokens":    cfg.Opts.MaxTokens,
		"tool_count":    len(cfg.Opts.Tools),
		"json_mode":     cfg.Opts.JSONMode,
		"streaming":     cfg.Opts.StreamChan != nil,
	}
	for k, v := range cfg.RequestMetaExtra {
		reqMeta[k] = v
	}
	inspector.EmitRequest(reqMeta)

	// Pass the actual transport into the synthetic terminal so the
	// chunk metadata carries the right transport label. RequestMetaExtra
	// is the existing place adapters declare their transport
	// ("tmux" / "structured_cli" / "api"); honour whatever's there so
	// the frontend chip matches the Header line in the pane.
	transportLabel, _ := cfg.RequestMetaExtra["transport"].(string)
	// Tmux adapters already publish a live pane scrape — their real
	// terminal output IS the screen. Emitting a parallel synthetic
	// banner ("$ claude (tmux) ... > user: ... [done]") competes with
	// the pane scrape for the same terminal entry in the store and
	// produces a flip-flop where the chip toggles between tmux and
	// api, plus a duplicate [done · tokens · cost] trailer. Build a
	// disabled terminal (ch=nil) for tmux so every term.* call is a
	// no-op while still keeping inspector emission for cost/tokens.
	var termSink chan<- StreamChunk = cfg.Opts.StreamChan
	if transportLabel == "tmux" {
		termSink = nil
	}
	term := NewSyntheticTerminalWithTransport(termSink, cfg.Provider, cfg.Model, transportLabel)
	if cfg.HeaderLine != "" {
		term.Header(cfg.HeaderLine)
	}
	// Append a workflow-context line beneath the Header when the
	// caller's InspectorSink carries step identity. This is the
	// "top line inside the terminal" the orchestrator UX leans on
	// to surface step name + index + attempt + agent without
	// crowding the panel title.
	if line := formatWorkflowContextLine(InspectorSinkStepContext(cfg.Opts.InspectorSink)); line != "" {
		term.Context(line)
	}
	for _, row := range formatConversationRows(cfg.Messages) {
		term.Row(row)
	}

	sink := &StreamSink{
		Ch:        cfg.Opts.StreamChan,
		Term:      term,
		Inspector: inspector,
	}

	// Helper owns the final close of opts.StreamChan so term.Done's
	// snapshot (with cost + tokens) lands BEFORE the channel is shut.
	// Adapter bodies must NOT defer-close opts.StreamChan themselves
	// — if any still do, the recover here keeps the double-close
	// from panicking, but their Done snapshot will still drop.
	defer func() {
		if cfg.Opts.StreamChan == nil {
			return
		}
		defer func() { _ = recover() }()
		close(cfg.Opts.StreamChan)
	}()

	callStart := time.Now()
	resp, err := body(sink)
	durationMs := time.Since(callStart).Milliseconds()

	if err != nil {
		term.Error(err)
		if inspector.Enabled() {
			inspector.EmitError(err, map[string]interface{}{"duration_ms": durationMs})
		}
		return resp, err
	}

	summary := ""
	if cfg.SummaryFn != nil {
		summary = cfg.SummaryFn(resp)
	} else {
		summary = defaultSummary(resp)
	}
	term.Done(durationMs, summary)

	if inspector.Enabled() {
		meta := extractCompletionMeta(resp, durationMs)
		if cfg.EnrichCompletionMeta != nil {
			cfg.EnrichCompletionMeta(resp, meta)
		}
		inspector.EmitCompletion(meta)
	}
	return resp, nil
}

// defaultSummary builds the terminal Done line from whatever token info
// the response carries. Adapters can override via SummaryFn.
//
// Token counts come from resp.Usage or the first choice's GenerationInfo.
// USD cost — when the adapter populated it via attachCostEstimate —
// rides on GenerationInfo.Additional["cost_usd_estimated"] and is
// appended to the summary for the [done · ...] line.
func defaultSummary(resp *ContentResponse) string {
	if resp == nil {
		return ""
	}
	cost := extractCostUSD(resp)
	tokens := ""
	if resp.Usage != nil && (resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0) {
		tokens = fmt.Sprintf("%d in · %d out", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	} else if len(resp.Choices) > 0 && resp.Choices[0] != nil && resp.Choices[0].GenerationInfo != nil {
		gi := resp.Choices[0].GenerationInfo
		in, out := 0, 0
		if gi.PromptTokens != nil {
			in = *gi.PromptTokens
		} else if gi.InputTokens != nil {
			in = *gi.InputTokens
		}
		if gi.CompletionTokens != nil {
			out = *gi.CompletionTokens
		} else if gi.OutputTokens != nil {
			out = *gi.OutputTokens
		}
		if in > 0 || out > 0 {
			tokens = fmt.Sprintf("%d in · %d out", in, out)
		}
	}
	if tokens != "" && cost > 0 {
		return fmt.Sprintf("%s · $%s", tokens, formatUSD(cost))
	}
	if tokens != "" {
		return tokens
	}
	if cost > 0 {
		return fmt.Sprintf("$%s", formatUSD(cost))
	}
	return ""
}

// extractCostUSD reads cost_usd_estimated from the first choice's
// GenerationInfo.Additional bag. Adapters populate this via their
// attachCostEstimate helpers; if not set, returns 0.
func extractCostUSD(resp *ContentResponse) float64 {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return 0
	}
	gi := resp.Choices[0].GenerationInfo
	if gi == nil || gi.Additional == nil {
		return 0
	}
	if v, ok := gi.Additional["cost_usd_estimated"].(float64); ok {
		return v
	}
	return 0
}

// formatConversationRows renders the message history as role-aware terminal
// rows. The SyntheticTerminal still mirrors rows into the legacy text buffer,
// but user/assistant/tool identity is carried structurally in metadata.
//
// Truncation rules:
//   - User messages: shown in full (debug visibility — they're
//     usually the most informative line).
//   - Assistant text / tool results: truncated to maxLen so the
//     pane stays scannable. Full text remains in the inspector
//     request event.
//   - Tool args: truncated more aggressively (120 chars).
func formatConversationRows(messages []MessageContent) []TerminalRow {
	const maxLen = 400
	const maxProseLen = 16000
	out := make([]TerminalRow, 0, len(messages))
	collapse := func(s string) string {
		return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	}
	truncate := func(s string, max int) string {
		s = collapse(s)
		if len(s) <= max {
			return s
		}
		return s[:max] + "…"
	}
	emitProse := func(kind, text string, budget int) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return
		}
		if len(trimmed) > budget {
			trimmed = trimmed[:budget] + "…"
		}
		out = append(out, TerminalRow{Kind: kind, Text: trimmed})
	}
	for _, msg := range messages {
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case TextContent:
				text := strings.TrimSpace(p.Text)
				if text == "" {
					continue
				}
				switch msg.Role {
				case ChatMessageTypeHuman:
					// User messages: full text, multi-line preserved.
					// The step's instructions live here; collapsing
					// to one line hides the structure.
					emitProse("user", text, maxProseLen)
				case ChatMessageTypeAI:
					// Assistant text emitted multi-line so the
					// synthetic terminal can render the model's
					// markdown (lists, headings, code fences,
					// paragraph breaks) instead of one collapsed
					// run-on string.
					emitProse("asst", text, maxProseLen)
				case ChatMessageTypeSystem:
					// Skip system prompts — too long, not useful
					// inline. They're available in the inspector
					// request event.
				default:
					out = append(out, TerminalRow{Kind: "plain", Text: truncate(text, maxLen)})
				}
			case ToolCall:
				name := ""
				args := ""
				if p.FunctionCall != nil {
					name = p.FunctionCall.Name
					args = p.FunctionCall.Arguments
				}
				out = append(out, TerminalRow{Kind: "tool", Name: name, Args: truncate(args, 120)})
			case ToolCallResponse:
				prefix := "✓"
				if p.IsError {
					prefix = "✗"
				}
				name := p.Name
				if name == "" {
					name = p.ToolCallID
				}
				out = append(out, TerminalRow{
					Kind:         "tool",
					Name:         name,
					Result:       truncate(p.Content, 300),
					ResultPrefix: prefix,
				})
				if len(p.Images) > 0 {
					out = append(out, TerminalRow{Kind: "plain", Text: fmt.Sprintf("+ %d image(s) attached to result", len(p.Images))})
				}
			case ImageContent:
				ref := p.Data
				if p.SourceType == "base64" {
					ref = fmt.Sprintf("base64 %d bytes", len(p.Data))
				}
				out = append(out, TerminalRow{Kind: "attachment", Text: fmt.Sprintf("[image %s %s %s]", p.SourceType, p.MediaType, truncate(ref, 80))})
			case DocumentContent:
				ref := p.Data
				if p.SourceType == "base64" {
					ref = fmt.Sprintf("base64 %d bytes", len(p.Data))
				}
				out = append(out, TerminalRow{Kind: "attachment", Text: fmt.Sprintf("[document %s %s %s]", p.SourceType, p.MediaType, truncate(ref, 80))})
			}
			// Reasoning / thinking blocks (Claude extended thinking,
			// Gemini thinking) don't ride as their own ContentPart in
			// llmtypes today — providers surface them either as
			// StreamChunkTypeContent during the response (routed to
			// term.AssistantText by StreamSink) or as token-count
			// metadata (ReasoningTokens / ThoughtsTokens on Usage).
			// If/when a dedicated ReasoningContent part is added,
			// extend this switch with a "(thinking) ..." line.
		}
	}
	return out
}

// formatWorkflowContextLine builds the "↳ step X (N/M) · attempt A ·
// agent Y · parent Z · triggered by W" line that sits beneath the
// synthetic terminal Header. Returns empty when no workflow context
// is available (e.g. direct API call outside a workflow), so the
// pane stays clean for those cases.
func formatWorkflowContextLine(sc StepContext) string {
	if sc.StepName == "" && sc.StepID == "" && sc.AgentName == "" && sc.WorkflowName == "" {
		return ""
	}
	parts := []string{}
	name := sc.StepName
	if name == "" {
		name = sc.StepID
	}
	if name != "" {
		if sc.StepIndex > 0 && sc.StepTotal > 0 {
			parts = append(parts, fmt.Sprintf("step %s (%d/%d)", name, sc.StepIndex, sc.StepTotal))
		} else {
			parts = append(parts, "step "+name)
		}
	}
	if sc.Attempt > 1 {
		parts = append(parts, fmt.Sprintf("attempt %d", sc.Attempt))
	}
	if sc.AgentName != "" {
		parts = append(parts, "agent "+sc.AgentName)
	}
	if sc.ParentStepID != "" {
		parts = append(parts, "parent "+sc.ParentStepID)
	}
	if sc.CallPurpose != "" {
		parts = append(parts, sc.CallPurpose)
	}
	if sc.WorkflowName != "" {
		parts = append(parts, "workflow "+sc.WorkflowName)
	}
	if len(parts) == 0 {
		return ""
	}
	return "↳ " + strings.Join(parts, " · ")
}

// formatUSD scales decimals to the magnitude so cheap haiku calls
// don't render as "$0.0000" while expensive runs stay readable.
//   - $1+      → 2 decimals (e.g. "$1.23")
//   - $0.01–$1 → 4 decimals (e.g. "$0.0420")
//   - below 1¢ → 6 decimals (e.g. "$0.000021"); zero cost falls back to "0"
func formatUSD(cost float64) string {
	switch {
	case cost >= 1:
		return fmt.Sprintf("%.2f", cost)
	case cost >= 0.01:
		return fmt.Sprintf("%.4f", cost)
	case cost > 0:
		return fmt.Sprintf("%.6f", cost)
	default:
		return "0"
	}
}

// extractCompletionMeta gathers the standard inspector completion
// fields from a unified ContentResponse. It reads both resp.Usage and
// resp.Choices[0].GenerationInfo, so the same code works whether the
// adapter populates one or both.
func extractCompletionMeta(resp *ContentResponse, durationMs int64) map[string]interface{} {
	meta := map[string]interface{}{"duration_ms": durationMs}
	if resp == nil {
		return meta
	}
	if len(resp.Choices) > 0 && resp.Choices[0] != nil {
		// Always populate stop_reason (may be empty string) so the
		// inspector contract sees a present-but-possibly-empty value
		// instead of a missing key.
		meta["stop_reason"] = resp.Choices[0].StopReason
		if gi := resp.Choices[0].GenerationInfo; gi != nil {
			if gi.PromptTokens != nil {
				meta["prompt_tokens"] = *gi.PromptTokens
			} else if gi.InputTokens != nil {
				meta["prompt_tokens"] = *gi.InputTokens
			}
			if gi.CompletionTokens != nil {
				meta["completion_tokens"] = *gi.CompletionTokens
			} else if gi.OutputTokens != nil {
				meta["completion_tokens"] = *gi.OutputTokens
			}
			if gi.TotalTokens != nil {
				meta["total_tokens"] = *gi.TotalTokens
			}
			if gi.ReasoningTokens != nil {
				meta["reasoning_tokens"] = *gi.ReasoningTokens
			}
			if gi.ThoughtsTokens != nil {
				meta["thoughts_tokens"] = *gi.ThoughtsTokens
			}
			if gi.Additional != nil {
				if cost, ok := gi.Additional["cost_usd_estimated"].(float64); ok && cost > 0 {
					meta["cost_usd_estimated"] = cost
				}
				if cm, ok := gi.Additional["cost_model_id"].(string); ok && cm != "" {
					meta["cost_model_id"] = cm
				}
			}
		}
	}
	if resp.Usage != nil {
		if _, hasIn := meta["prompt_tokens"]; !hasIn && resp.Usage.InputTokens > 0 {
			meta["prompt_tokens"] = resp.Usage.InputTokens
		}
		if _, hasOut := meta["completion_tokens"]; !hasOut && resp.Usage.OutputTokens > 0 {
			meta["completion_tokens"] = resp.Usage.OutputTokens
		}
		if _, hasTot := meta["total_tokens"]; !hasTot && resp.Usage.TotalTokens > 0 {
			meta["total_tokens"] = resp.Usage.TotalTokens
		}
	}
	return meta
}
