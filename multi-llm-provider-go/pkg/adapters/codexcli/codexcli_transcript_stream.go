package codexcli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// codexInteractiveStreamTranscriptEnabled reports whether to tail Codex's
// rollout JSONL for structured streaming. Opt-in (default OFF), set per call
// via WithStreamTranscript — there is no environment-variable fallback.
func codexInteractiveStreamTranscriptEnabled(opts *llmtypes.CallOptions) bool {
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if v, ok := opts.Metadata.Custom[MetadataKeyStreamTranscript].(bool); ok {
			return v
		}
	}
	return false
}

// codexTranscriptEvent is one structured item recovered from the rollout during
// a turn: assistant text (Text set), the start of a tool call (ToolName set),
// or a tool's completion (IsToolEnd set — from mcp_tool_call_end or
// item_completed for MCP bridge calls, or function_call_output /
// custom_tool_call_output for Codex's native tools).
type codexTranscriptEvent struct {
	Text       string
	ToolName   string
	ToolCallID string
	// ToolArgs is recorded for MCP calls as invocation.arguments.  The rollout
	// commonly persists only the mcp_tool_call_end row, so retaining it here
	// lets the synthesized start event carry the actual call input to consumers.
	ToolArgs string
	Key      string // dedup key for content (row timestamp); tools dedup by ToolCallID
	// IsToolEnd distinguishes a completion row from a start row; both carry
	// ToolCallID, so this is the only way to tell them apart once mapped onto
	// the shared event shape. Codex 0.153+ item_completed rows include MCP
	// result text; older mcp_tool_call_end rows do not.
	IsToolEnd    bool
	ToolResult   string
	ToolDuration time.Duration
	// Compaction is set for Codex's own ContextCompaction item (end only:
	// Codex writes no start record).
	Compaction *llmtypes.ContextCompaction
	// Usage is set for a token_count record: the prompt size of the latest
	// model call, the context window and the plan windows.
	Usage *codexLiveUsage
}

// codexLiveUsage is one token_count record, reduced to what a live meter needs.
type codexLiveUsage struct {
	Model        string
	InputTokens  int // prompt side, cached included: the context fill
	CachedTokens int
	OutputTokens int
	// TotalTokens matters for one record: right after a compaction Codex
	// writes a token_count with zero input and total = the size of the
	// compacted context (verified on real 0.160 rollouts).
	TotalTokens   int
	ContextWindow int
	RateLimits    *codexRateLimits
}

// contextTokens is the context fill this record reports.
func (u *codexLiveUsage) contextTokens() int {
	if u.InputTokens > 0 {
		return u.InputTokens
	}
	return u.TotalTokens
}

func (u *codexLiveUsage) statusLine() *llmtypes.StatusLine {
	if u == nil || u.contextTokens() <= 0 {
		return nil
	}
	uncached := u.InputTokens - u.CachedTokens
	if uncached < 0 {
		uncached = u.InputTokens
	}
	status := &llmtypes.StatusLine{
		Provider:             "codex-cli",
		Model:                u.Model,
		InputTokens:          uncached,
		OutputTokens:         u.OutputTokens,
		CacheReadInputTokens: u.CachedTokens,
		TotalInputTokens:     u.InputTokens,
		TotalOutputTokens:    u.OutputTokens,
	}
	status.SetContextUsage(u.contextTokens(), u.ContextWindow)
	status.SetRateLimitWindows(codexStructuredRateLimitWindows(u.RateLimits))
	status.SetStatusExtras(codexStatusExtras(u.RateLimits, u.contextTokens(), u.ContextWindow, "", time.Now()))
	return status
}

// codexTranscriptStreamState tails the current turn's rollout JSONL and emits
// content/tool-call-start StreamChunks as new rows are written. It resolves the
// rollout path once (mirroring codexTurnCompletionTracker) and then advances by
// byte offset. Used INSIDE waitForCodexInteractiveResponse — which returns
// before the adapter closes the StreamChan — so there is no send-on-closed-chan
// race that a detached goroutine would risk.
type codexTranscriptStreamState struct {
	turnStart  time.Time
	workingDir string
	// resolveRollout, when set, returns THIS session's rollout. Lock-free (see
	// codexRolloutResolverForSession) because poll runs while the adapter holds
	// session.mu. nil falls back to the unsafe directory lookup, which can tail
	// another conversation's transcript when two sessions share a directory.
	resolveRollout func(time.Time) string
	path           string
	offset         int64
	seenTool       map[string]bool // defensive dedup of tool starts by call_id
	seenContent    map[string]bool // dedup assistant text (codex writes it as BOTH agent_message and response_item message)
	// pendingToolStarts carries each tool call's start timestamp across polls
	// (begin and end rows, or a call and its output row, can land in
	// different polls) so the matching end can compute a real duration
	// instead of reporting zero.
	pendingToolStarts map[string]time.Time
	// sideOnly drops assistant text and tool rows: the structured transport
	// already streams those from stdout and reads only compaction and usage
	// from the rollout.
	sideOnly bool
	// liveUsage, when set, turns token_count rows into throttled status_line
	// chunks. Nil for the tmux transport, which already publishes the same
	// rollout usage with its terminal snapshots (streamCodexStatusLine).
	liveUsage *llmtypes.LiveUsageThrottle
	// lastContextTokens is the newest prompt size seen, used as a
	// compaction's tokens-before.
	lastContextTokens int
	// compactedTokens is the post-compaction context size from the zero-input
	// token_count Codex writes between `compacted` and the item record.
	compactedTokens int
	model           string
	seenCompaction  map[string]bool
}

func newCodexTranscriptStreamState(turnStart time.Time, workingDir string, resolveRollout func(time.Time) string) *codexTranscriptStreamState {
	return &codexTranscriptStreamState{
		turnStart:         turnStart,
		workingDir:        workingDir,
		resolveRollout:    resolveRollout,
		seenTool:          map[string]bool{},
		seenContent:       map[string]bool{},
		pendingToolStarts: map[string]time.Time{},
		seenCompaction:    map[string]bool{},
	}
}

// poll reads any newly-appended rollout rows and emits their events on
// streamChan. Best-effort: swallows IO errors and returns on ctx cancellation.
func (s *codexTranscriptStreamState) poll(ctx context.Context, streamChan chan<- llmtypes.StreamChunk) {
	if streamChan == nil {
		return
	}
	for _, chunk := range s.readChunks() {
		select {
		case streamChan <- chunk:
		case <-ctx.Done():
			return
		}
	}
}

// Both normal streaming and retained follow-ups consume the same decoder and
// dedup rules. Reading progress never invokes the final-answer classifier.
func (s *codexTranscriptStreamState) readChunks() []llmtypes.StreamChunk {
	return s.readChunksAt(time.Now(), false)
}

// readChunksAt is readChunks with the clock and the usage-throttle flush made
// explicit: flush releases the newest usage snapshot regardless of the
// interval (the last read of a turn).
func (s *codexTranscriptStreamState) readChunksAt(now time.Time, flush bool) []llmtypes.StreamChunk {
	chunks := s.readRolloutChunks()
	if s.liveUsage != nil {
		if status := s.liveUsage.Take(now, flush); status != nil {
			chunks = append(chunks, llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeStatusLine, StatusLine: status})
		}
	}
	return chunks
}

func (s *codexTranscriptStreamState) readRolloutChunks() []llmtypes.StreamChunk {
	if s.path == "" {
		if s.resolveRollout != nil {
			s.path = s.resolveRollout(s.turnStart)
		} else {
			s.path = findCodexRolloutByWorkingDirUnsafe(s.turnStart, s.workingDir)
		}
		if s.path == "" {
			return nil // rollout not created yet — try again next tick
		}
	}
	events, next, err := readCodexTranscriptEventsFromFile(s.path, s.offset, s.turnStart, s.pendingToolStarts)
	if err != nil {
		return nil
	}
	s.offset = next
	var chunks []llmtypes.StreamChunk
	for _, e := range events {
		if e.Usage != nil {
			if e.Usage.Model == "" {
				e.Usage.Model = s.model
			} else {
				s.model = e.Usage.Model
			}
			if e.Usage.InputTokens > 0 {
				s.lastContextTokens = e.Usage.InputTokens
				s.compactedTokens = 0
			} else if e.Usage.TotalTokens > 0 {
				s.compactedTokens = e.Usage.TotalTokens
			}
			if s.liveUsage != nil && e.Usage.contextTokens() > 0 {
				s.liveUsage.Offer(e.Usage.statusLine())
			}
			continue
		}
		if e.Compaction != nil {
			if e.Compaction.ID != "" {
				if s.seenCompaction[e.Compaction.ID] {
					continue
				}
				s.seenCompaction[e.Compaction.ID] = true
			}
			compaction := *e.Compaction
			if compaction.TokensBefore == 0 {
				compaction.TokensBefore = s.lastContextTokens
			}
			if compaction.TokensAfter == 0 && s.compactedTokens > 0 {
				compaction.TokensAfter = s.compactedTokens
				s.lastContextTokens = s.compactedTokens
				s.compactedTokens = 0
			}
			chunks = append(chunks, llmtypes.ContextCompactionChunk(compaction))
			continue
		}
		if s.sideOnly {
			continue
		}
		// Defensive: emit a given tool call's start only once, keyed by
		// call_id, in case a row is ever re-observed.
		if e.ToolName != "" && e.ToolCallID != "" {
			if s.seenTool[e.ToolCallID] {
				continue
			}
			s.seenTool[e.ToolCallID] = true
		}
		// Codex writes each assistant message twice — as an event_msg
		// agent_message AND a response_item message with the same text (sometimes
		// at slightly different timestamps). The double-write is systematic, so
		// emit each distinct assistant line once, keyed by text.
		if e.ToolName == "" && e.Text != "" {
			if s.seenContent[e.Text] {
				continue
			}
			s.seenContent[e.Text] = true
		}
		chunks = append(chunks, codexTranscriptEventToChunk(e))
	}
	return chunks
}

func codexTranscriptEventToChunk(e codexTranscriptEvent) llmtypes.StreamChunk {
	meta := map[string]interface{}{"codex_cli_stream_source": "transcript"}
	if e.IsToolEnd {
		return llmtypes.StreamChunk{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolCallID:   e.ToolCallID,
			ToolArgs:     e.ToolArgs,
			ToolResult:   e.ToolResult,
			ToolDuration: e.ToolDuration,
			Metadata:     meta,
		}
	}
	if e.ToolName != "" {
		return llmtypes.StreamChunk{
			Type:       llmtypes.StreamChunkTypeToolCallStart,
			ToolName:   e.ToolName,
			ToolCallID: e.ToolCallID,
			ToolArgs:   e.ToolArgs,
			Metadata:   meta,
		}
	}
	return llmtypes.StreamChunk{
		Type:     llmtypes.StreamChunkTypeContent,
		Content:  e.Text,
		Metadata: meta,
	}
}

// readCodexTranscriptEventsFromFile reads new rollout rows starting at byte
// `offset`, returning the structured events plus the offset to resume from. Only
// consumes up to the last complete (newline-terminated) line, holding back a
// partial trailing line. Rows older than turnStart are skipped (but consumed).
// Parses the same rollout schema as readCodexTranscriptMessagesFile:
// response_item → message(assistant output_text) / function_call /
// custom_tool_call / function_call_output / custom_tool_call_output, plus
// event_msg → mcp_tool_call_begin/_end or item_completed for MCP bridge calls.
// pendingToolStarts
// carries each call's start timestamp across calls (a call and its
// completion row can land in different polls) so a matching completion can
// compute a real duration instead of reporting zero; pass the same map back
// in on every call.
func readCodexTranscriptEventsFromFile(path string, offset int64, turnStart time.Time, pendingToolStarts map[string]time.Time) ([]codexTranscriptEvent, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, err
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}
	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL < 0 {
		return nil, offset, nil
	}
	consumed := data[:lastNL+1]
	nextOffset := offset + int64(len(consumed))

	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type invocation struct {
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	type itemDuration struct {
		Seconds int64 `json:"secs"`
		Nanos   int64 `json:"nanos"`
	}
	type completedItem struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Result    json.RawMessage `json:"result"`
		Duration  itemDuration    `json:"duration"`
		// Native web search: {"type":"Extension","kind":"web.search"} (0.160)
		// or {"type":"WebSearch"} (older, no results). See codexcli_web_search.go.
		Kind    string                 `json:"kind"`
		Query   string                 `json:"query"`
		Action  json.RawMessage        `json:"action"`
		Results []codexWebSearchResult `json:"results"`
	}
	type rolloutPayload struct {
		Type string `json:"type"`
		// response_item: message
		Role    string         `json:"role"`
		Content []contentBlock `json:"content"`
		// response_item: function_call / custom_tool_call
		Name   string `json:"name"`
		CallID string `json:"call_id"`
		// Arguments for the call. function_call carries "arguments"; the newer
		// custom_tool_call shape carries the invocation body as "input" instead
		// (for codex's code-mode `exec`, that body is the actual script — which
		// is where a bridge call like
		// `tools.mcp__api_bridge__execute_shell_command({...})` appears). Reading
		// only "arguments" left every custom_tool_call with empty ToolArgs, so
		// the UI had no detail to render for the call.
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
		// response_item: function_call_output / custom_tool_call_output
		//
		// See coding-agent-loop docs/design/
		// product_api_transport_for_coding_agents.md ("Measured matrix") for the
		// live evidence behind this shape.
		//
		// Deliberately json.RawMessage, NOT string: function_call_output sends a
		// bare string, but custom_tool_call_output sends an ARRAY of content
		// blocks ([{"type":"input_text","text":"..."}]). Declaring this `string`
		// made json.Unmarshal fail for the WHOLE row, so the row was skipped and
		// no ToolCallEnd was ever emitted — every codex code-mode tool call
		// stayed open and its UI chip spun forever.
		Output json.RawMessage `json:"output"`
		// event_msg: agent_message
		Message string `json:"message"`
		// event_msg: mcp_tool_call_begin / _end
		Invocation *invocation `json:"invocation"`
		// Codex 0.153+ records the semantic MCP call as an item_completed
		// event. The surrounding response_item is only the generic JavaScript
		// `exec` transport, so this item is the authoritative tool identity.
		Item *completedItem `json:"item"`
		// item_completed timing (Codex 0.15x+; older builds omit started_at_ms).
		StartedAtMs   int64 `json:"started_at_ms"`
		CompletedAtMs int64 `json:"completed_at_ms"`
		// turn_context: the effective model.
		Model string `json:"model"`
		// event_msg: token_count, written after every model call.
		Info *struct {
			LastTokenUsage struct {
				InputTokens       int `json:"input_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
				OutputTokens      int `json:"output_tokens"`
				TotalTokens       int `json:"total_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int `json:"model_context_window"`
		} `json:"info"`
		RateLimits *codexRateLimits `json:"rate_limits"`
	}
	type ev struct {
		Type      string         `json:"type"` // "event_msg" | "response_item"
		Timestamp string         `json:"timestamp"`
		Payload   rolloutPayload `json:"payload"`
	}

	var events []codexTranscriptEvent
	for _, raw := range bytes.Split(consumed, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var e ev
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		var rowTime time.Time
		if e.Timestamp != "" {
			rowTime, _ = time.Parse(time.RFC3339Nano, e.Timestamp)
		}
		// Usage rows are read even before the turn: the context fill is the
		// conversation's current state, and the newest one is a compaction's
		// tokens-before.
		if e.Type == "turn_context" && e.Payload.Model != "" {
			events = append(events, codexTranscriptEvent{Usage: &codexLiveUsage{Model: e.Payload.Model}})
			continue
		}
		if e.Type == "event_msg" && e.Payload.Type == "token_count" {
			if info := e.Payload.Info; info != nil {
				events = append(events, codexTranscriptEvent{Usage: &codexLiveUsage{
					InputTokens:   info.LastTokenUsage.InputTokens,
					CachedTokens:  info.LastTokenUsage.CachedInputTokens,
					OutputTokens:  info.LastTokenUsage.OutputTokens,
					TotalTokens:   info.LastTokenUsage.TotalTokens,
					ContextWindow: info.ModelContextWindow,
					RateLimits:    e.Payload.RateLimits,
				}})
			}
			continue
		}
		if !turnStart.IsZero() && !rowTime.IsZero() && rowTime.Before(turnStart) {
			continue
		}
		switch e.Type {
		case "event_msg":
			// Current Codex (0.144+) records assistant prose as agent_message and
			// MCP calls as mcp_tool_call_begin/_end (not as response_item rows).
			// Some 0.145 code-mode calls only persist the _end row; that row still
			// carries the full invocation, so synthesize the missing start instead
			// of losing the actual MCP tool name from the stream.
			switch e.Payload.Type {
			case "agent_message":
				if e.Payload.Message != "" {
					events = append(events, codexTranscriptEvent{Text: e.Payload.Message, Key: e.Timestamp})
				}
			case "mcp_tool_call_begin":
				if e.Payload.Invocation != nil && e.Payload.Invocation.Tool != "" {
					events = append(events, codexTranscriptEvent{
						ToolName:   e.Payload.Invocation.Tool,
						ToolCallID: e.Payload.CallID,
						ToolArgs:   compactCodexJSON(e.Payload.Invocation.Arguments),
					})
					// Recorded even when rowTime is zero (missing/unparseable
					// timestamp): the matching _end below treats a zero start
					// as "not measured" and reports 0 rather than a
					// fabricated duration.
					if pendingToolStarts != nil && e.Payload.CallID != "" {
						pendingToolStarts[e.Payload.CallID] = rowTime
					}
				}
			case "mcp_tool_call_end":
				if e.Payload.CallID != "" {
					if _, began := pendingToolStarts[e.Payload.CallID]; !began && e.Payload.Invocation != nil && e.Payload.Invocation.Tool != "" {
						events = append(events, codexTranscriptEvent{
							ToolName:   e.Payload.Invocation.Tool,
							ToolCallID: e.Payload.CallID,
							ToolArgs:   compactCodexJSON(e.Payload.Invocation.Arguments),
						})
					}
					events = append(events, codexTranscriptEvent{
						IsToolEnd:  true,
						ToolCallID: e.Payload.CallID,
						// No ToolResult: codex's own event stream never
						// includes an MCP call's result text (see the
						// codexTranscriptEvent doc comment).
						ToolDuration: consumeCodexPendingStart(pendingToolStarts, e.Payload.CallID, rowTime),
					})
				}
			case "item_completed":
				if item := e.Payload.Item; item != nil && item.Type == "ContextCompaction" {
					events = append(events, codexTranscriptEvent{Compaction: codexCompactionFromItem(item.ID, e.Payload.StartedAtMs, e.Payload.CompletedAtMs, rowTime)})
					continue
				}
				if item := e.Payload.Item; item != nil && item.ID != "" && (item.Type == "WebSearch" || (item.Type == "Extension" && item.Kind == "web.search")) {
					// One completed row per search and no start row: emit the
					// pair so the chat's search card gets the query and sources.
					var duration time.Duration
					if e.Payload.StartedAtMs > 0 && e.Payload.CompletedAtMs >= e.Payload.StartedAtMs {
						duration = time.Duration(e.Payload.CompletedAtMs-e.Payload.StartedAtMs) * time.Millisecond
					}
					args := codexWebSearchArgs(item.Query, item.Action)
					events = append(events,
						codexTranscriptEvent{ToolName: "web_search", ToolCallID: item.ID, ToolArgs: args},
						codexTranscriptEvent{
							IsToolEnd:    true,
							ToolCallID:   item.ID,
							ToolArgs:     args,
							ToolResult:   codexWebSearchResultText(item.Query, item.Action, item.Results),
							ToolDuration: duration,
						},
					)
					continue
				}
				if item := e.Payload.Item; item != nil && item.Type == "McpToolCall" && item.ID != "" && item.Tool != "" {
					// This shape has no separate start row. Emit an adjacent
					// start/end pair so downstream consumers see the real MCP
					// tool rather than only Codex's generic outer `exec` call.
					events = append(events,
						codexTranscriptEvent{
							ToolName:   item.Tool,
							ToolCallID: item.ID,
							ToolArgs:   compactCodexJSON(item.Arguments),
						},
						codexTranscriptEvent{
							IsToolEnd:    true,
							ToolCallID:   item.ID,
							ToolResult:   codexMCPItemResultText(item.Result),
							ToolDuration: time.Duration(item.Duration.Seconds)*time.Second + time.Duration(item.Duration.Nanos),
						},
					)
				}
			}
		case "response_item":
			// Also handle the response_item form (older/other Codex builds, and
			// codex's own shell tool custom_tool_call name:"exec").
			switch e.Payload.Type {
			case "message":
				if e.Payload.Role != "assistant" {
					continue
				}
				for _, b := range e.Payload.Content {
					if b.Type != "output_text" && b.Type != "text" {
						continue
					}
					if b.Text == "" {
						continue
					}
					events = append(events, codexTranscriptEvent{Text: b.Text, Key: e.Timestamp})
				}
			case "function_call", "custom_tool_call":
				// Carry the call body through as ToolArgs so the UI has something
				// to show for the call. function_call uses "arguments";
				// custom_tool_call uses "input" — for codex's code-mode `exec`
				// that body is the script, which is also the only place a nested
				// bridge call (tools.mcp__api_bridge__*) is visible.
				args := codexRolloutText(e.Payload.Arguments)
				if args == "" {
					args = codexRolloutText(e.Payload.Input)
				}
				events = append(events, codexTranscriptEvent{ToolName: e.Payload.Name, ToolCallID: e.Payload.CallID, ToolArgs: args})
				if pendingToolStarts != nil && e.Payload.CallID != "" {
					pendingToolStarts[e.Payload.CallID] = rowTime
				}
			case "function_call_output", "custom_tool_call_output":
				if e.Payload.CallID != "" {
					events = append(events, codexTranscriptEvent{
						IsToolEnd:    true,
						ToolCallID:   e.Payload.CallID,
						ToolResult:   codexRolloutText(e.Payload.Output),
						ToolDuration: consumeCodexPendingStart(pendingToolStarts, e.Payload.CallID, rowTime),
					})
				}
			}
		}
	}
	return events, nextOffset, nil
}

func codexMCPItemResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return codexRolloutText(raw)
	}
	var texts []string
	for _, block := range result.Content {
		if strings.TrimSpace(block.Text) != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// consumeCodexPendingStart looks up and removes callID's recorded start time,
// returning the elapsed duration to endTime, or 0 if the start is missing,
// zero, or the end row's own timestamp couldn't be parsed — "not measured",
// never a fabricated value.
func consumeCodexPendingStart(pendingToolStarts map[string]time.Time, callID string, endTime time.Time) time.Duration {
	if pendingToolStarts == nil {
		return 0
	}
	start, ok := pendingToolStarts[callID]
	if !ok {
		return 0
	}
	delete(pendingToolStarts, callID)
	if start.IsZero() || endTime.IsZero() || !endTime.After(start) {
		return 0
	}
	return endTime.Sub(start)
}

// codexRolloutText renders a rollout field that codex sends in more than one
// shape into display text.
//
// Codex uses a bare JSON string for function_call_output, and an array of
// content blocks ([{"type":"input_text","text":"..."}]) for the newer
// custom_tool_call_output. Both must decode, and neither may abort the row: an
// unparsed completion row means no ToolCallEnd, which is indistinguishable in
// the UI from a tool that never finished.
func codexRolloutText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	// An unrecognised shape is still better surfaced verbatim than dropped.
	return string(raw)
}

// codexCompactionFromItem maps Codex's ContextCompaction item_completed record
// to the shared end event. Codex writes no start record and no token counts;
// tokens-before is filled by the caller from the preceding token_count.
func codexCompactionFromItem(id string, startedAtMs, completedAtMs int64, rowTime time.Time) *llmtypes.ContextCompaction {
	c := &llmtypes.ContextCompaction{
		Provider: "codex-cli",
		Phase:    llmtypes.ContextCompactionPhaseEnd,
		ID:       id,
		Outcome:  llmtypes.ContextCompactionOutcomeSuccess,
	}
	if completedAtMs > 0 {
		c.EndedAt = time.UnixMilli(completedAtMs).UTC()
	} else if !rowTime.IsZero() {
		c.EndedAt = rowTime.UTC()
	}
	if startedAtMs > 0 {
		c.StartedAt = time.UnixMilli(startedAtMs).UTC()
		if !c.EndedAt.IsZero() && c.EndedAt.After(c.StartedAt) {
			c.DurationMs = c.EndedAt.Sub(c.StartedAt).Milliseconds()
		}
	}
	return c
}
