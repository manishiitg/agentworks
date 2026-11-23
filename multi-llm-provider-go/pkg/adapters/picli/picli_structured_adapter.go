package picli

import (
	"bytes"

	"encoding/json"

	"os"

	"strings"

	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// piJSONEvent is one native Pi RPC event (Pi 1.4.0). Real shape: session -> agent_start
// -> turn_start -> message_start/message_update/message_end (assistant text
// deltas AND tool calls both flow through message_update.assistantMessageEvent)
// -> tool_execution_start/update/end (the actual tool run, separate from the
// model's toolCall content block) -> turn_end -> ... -> agent_end -> agent_settled.
type piJSONEvent struct {
	Type                string            `json:"type"`
	AssistantMessageEvt *piAssistantEvent `json:"assistantMessageEvent,omitempty"`
	ToolCallID          string            `json:"toolCallId,omitempty"`
	ToolName            string            `json:"toolName,omitempty"`
	Args                json.RawMessage   `json:"args,omitempty"`
	Result              json.RawMessage   `json:"result,omitempty"`
	IsError             bool              `json:"isError,omitempty"`
	Message             *piJSONMessage    `json:"message,omitempty"`
	// compaction_start / compaction_end (see piCompactionTracker).
	Reason  string `json:"reason,omitempty"`
	Aborted bool   `json:"aborted,omitempty"`
}

type piStructuredToolCall struct {
	Name string
	Args string
}

func compactPiStructuredJSON(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		return direct
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) == nil {
		return compact.String()
	}
	return strings.TrimSpace(string(raw))
}

func piStructuredToolStartChunk(event piJSONEvent) (llmtypes.StreamChunk, piStructuredToolCall) {
	call := piStructuredToolCall{
		Name: strings.TrimSpace(event.ToolName),
		Args: compactPiStructuredJSON(event.Args),
	}
	return llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeToolCallStart,
		Content:    call.Name,
		ToolName:   call.Name,
		ToolCallID: event.ToolCallID,
		ToolArgs:   call.Args,
	}, call
}

func piStructuredToolEndChunk(event piJSONEvent, call piStructuredToolCall, duration time.Duration) llmtypes.StreamChunk {
	name := strings.TrimSpace(event.ToolName)
	if name == "" {
		name = call.Name
	}
	return llmtypes.StreamChunk{
		Type:         llmtypes.StreamChunkTypeToolCallEnd,
		Content:      event.ToolCallID,
		ToolName:     name,
		ToolCallID:   event.ToolCallID,
		ToolArgs:     call.Args,
		ToolResult:   compactPiStructuredJSON(event.Result),
		ToolDuration: duration,
	}
}

type piAssistantEvent struct {
	Type    string `json:"type"` // "text_start" | "text_delta" | "text_end" | "toolcall_start" | "toolcall_delta" | "toolcall_end"
	Delta   string `json:"delta,omitempty"`
	Content string `json:"content,omitempty"`
}

// piApplyAssistantEvent updates the accumulated turn-text buffer for one
// assistantMessageEvent and returns any delta text to stream immediately.
//
// pi delimits each logical assistant text segment with its own
// text_start/text_end pair, verified live against pi 0.80.10 — including a
// captured turn with TWO separate text_start/text_end blocks (narration
// before a tool call, then narration after it, both inside one
// turn_start/turn_end pair). Before this, only text_delta was handled and the
// buffer was reset solely at turn_end, so two genuinely separate assistant
// messages within one turn would be concatenated with no separator at all —
// e.g. "...about to read b.txt.The file b.txt contained...". A fresh
// text_start now inserts a paragraph break when the buffer already holds a
// prior segment; the very first segment of a turn (empty buffer) gets no
// spurious separator.
func piApplyAssistantEvent(buf *strings.Builder, evt *piAssistantEvent) (streamDelta string) {
	if evt == nil {
		return ""
	}
	switch evt.Type {
	case "text_start":
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
	case "text_delta":
		if evt.Delta != "" {
			buf.WriteString(evt.Delta)
			return evt.Delta
		}
	}
	return ""
}

type piJSONMessage struct {
	Role  string       `json:"role,omitempty"`
	Usage *piJSONUsage `json:"usage,omitempty"`
}

type piJSONUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning"`
	Total      int `json:"totalTokens"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

func applyPiJSONUsage(message *piJSONMessage, totalUsage *llmtypes.Usage, cacheWriteTokens *int) {
	if message == nil || message.Usage == nil || totalUsage == nil || cacheWriteTokens == nil {
		return
	}
	u := message.Usage
	totalUsage.InputTokens = u.Input
	totalUsage.OutputTokens = u.Output
	totalUsage.TotalTokens = u.Total
	if totalUsage.TotalTokens == 0 {
		totalUsage.TotalTokens = u.Input + u.Output
	}
	if u.Reasoning > 0 {
		reasoning := u.Reasoning
		totalUsage.ReasoningTokens = &reasoning
	}
	if u.CacheRead > 0 {
		cacheRead := u.CacheRead
		totalUsage.CacheTokens = &cacheRead
	}
	*cacheWriteTokens = u.CacheWrite
}

// resolveStructuredProviderModel picks the provider/model a structured turn
// will actually run, honoring the caller's provider override exactly as the
// interactive path does.
//
// Extracted so a test can assert THIS decision rather than re-deriving it from
// the same helpers the adapter uses -- a test that calls
// resolvePiProviderModel itself passes whether or not the adapter bothers to
// consult the override, which is how the bug survived review in the first
// place.
func (p *PiCLIAdapter) resolveStructuredProviderModel(opts *llmtypes.CallOptions) (string, string) {

	return resolvePiProviderModel(p.GetModelID(), piProviderFromOptions(opts))
}

// piStallPollInterval is how often the wait loop checks; it is NOT how often it
// logs. See piStallReporter.
const piStallPollInterval = 30 * time.Second

// piStallSilenceThreshold is how long pi's stream must be completely silent
// before an unfinished run is worth reporting. Duration alone means nothing --
// a step legitimately making many tool calls can run long -- only silence
// does. (An earlier version of this comment claimed "this workflow permits
// 90m tool executions"; that was never verified and was wrong -- see
// piMaxTurnDuration below for what actually bounds a turn, and why nothing
// did until it was added.)
const piStallSilenceThreshold = 5 * time.Minute

// piWedgeSilenceThreshold is how long pi's stream may be COMPLETELY silent,
// with no tool call in flight, before the turn is treated as wedged and killed.
//
// This is the fast path out of the failure PLAT-153 documents, and it exists
// because piMaxTurnDuration alone is too blunt to be the only recovery: a
// wedged turn burned the full 45m ceiling before anything reclaimed it, and a
// workflow with several such steps pays that repeatedly.
//
// It is safe to be this aggressive ONLY because of the outstanding-tool guard.
// A tool call may legitimately run up to
// codingtimeout.LongRunningMCPToolTimeout (90m default), during which pi emits
// nothing at all -- killing on silence alone would murder healthy long tool
// calls. With zero tools in flight, the only legitimate reason for silence is
// waiting on the model to start responding, and once it starts, pi streams
// chunks continuously.
//
// 10 minutes: comfortably above every no-tool-in-flight gap observed in this
// deployment (the slowest full model turns measured were 2m19s and 1m9s,
// streaming throughout), and far below the 26+ minutes of dead silence the
// real wedge produced. Configurable via PI_STRUCTURED_WEDGE_SILENCE because,
// like the ceiling, this is reasoned from a day of observation rather than a
// long baseline.
func piWedgeSilenceThreshold() time.Duration {
	if v := strings.TrimSpace(os.Getenv("PI_STRUCTURED_WEDGE_SILENCE")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

// piMaxTurnDuration is the hard ceiling on one structured pi CLI turn.
//
// Before this, nothing bounded it. TOOL_EXECUTION_TIMEOUT
// (agent_go/cmd/server/agent_tuning.go) only wraps each individual tool call
// pi makes through the bridge -- and in this deployment was not even set,
// leaving the actual per-tool default at 5 minutes, not the 90 minutes
// earlier assumed without checking. Neither that setting nor anything else
// placed a deadline on the ctx passed into generateContentStructured itself;
// a pi process that got internally wedged -- not blocked on any one tool
// call, just stopped making progress -- could run until the server itself
// restarted.
//
// This is pi's own documented failure mode, not speculation: pi-coding-agent
// upstream (github.com/earendil-works/pi#8004) reports two real sessions
// frozen 5.5 and 8.7 hours by exactly this shape (a spawned/nested process
// that completed its actual work but never exited, keeping Node's event loop
// alive with nothing for `pi --print` to wait on), and states plainly there is
// "no general tool-call timeout" and "no abort handle" once a call is running.
// Confirmed live 2026-08-19: a form-26as step's pi process sat with a
// completely idle Node event loop (main thread parked in uv__io_poll/kevent,
// every worker blocked on uv_cond_wait, no thread in any syscall) for over an
// hour, its last actual tool call having finished in 28ms with nothing after.
//
// 45 minutes: comfortably above every turn duration observed in this
// deployment today (healthy multi-tool-call turns finished in single-digit
// minutes), while still turning an unbounded, hours-long wedge into a bounded
// one. Configurable via PI_STRUCTURED_MAX_TURN_DURATION because 45m is a
// reasoned default, not a measured one -- no turn-duration history exists yet
// to derive it from.
func piMaxTurnDuration() time.Duration {
	if v := strings.TrimSpace(os.Getenv("PI_STRUCTURED_MAX_TURN_DURATION")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 45 * time.Minute
}

// piMaxStallReportInterval caps the doubling backoff, so a genuinely stuck run
// keeps a heartbeat without flooding.
const piMaxStallReportInterval = 16 * time.Minute

// piStallReporter decides WHEN an outstanding cmd.Wait() is worth a log line.
//
// Extracted from the wait loop so this decision is testable: the first version
// of this logging reported purely on elapsed run time and produced ~34
// error-level lines for a perfectly healthy 17-minute step (measured live
// 2026-08-19), which is the same alert-fatigue failure that made the
// TOOL_ERROR_SUSPECT channel unreadable at 929 lines. A predicate that is only
// exercised through a real subprocess is a predicate nobody checks.
type piStallReporter struct {
	lastReportedAt time.Time
	nextInterval   time.Duration
}

// shouldReport reports whether to emit a stall line now.
//
//   - terminal (pi said it finished but has not exited) is the deadlock: it
//     never resolves on its own, so report it on the very first check.
//   - otherwise, stay silent until pi's stream has been quiet for
//     piStallSilenceThreshold. Events still flowing means it is working.
//   - after any report, back off by doubling so a stuck run leaves a readable
//     trail rather than hundreds of identical lines.
func (r *piStallReporter) shouldReport(terminal bool, silence time.Duration, now time.Time) bool {
	if !terminal && silence < piStallSilenceThreshold {
		return false
	}
	if !r.lastReportedAt.IsZero() && now.Sub(r.lastReportedAt) < r.nextInterval {
		return false
	}
	r.lastReportedAt = now
	if r.nextInterval == 0 {
		r.nextInterval = piStallPollInterval
	} else if r.nextInterval < piMaxStallReportInterval {
		r.nextInterval *= 2
	}
	return true
}

func extractTextFromPiMessage(m llmtypes.MessageContent) string {
	var parts []string
	for _, part := range m.Parts {
		if tc, ok := part.(llmtypes.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func intPtrIfNonZeroPi(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// piOverrideEnv returns base with any existing entry for a key present in
// overrides removed, then overrides appended. Filtering first rather than
// relying on "last assignment wins" keeps this correct regardless of
// platform-specific duplicate-env-var behavior in exec.
func piOverrideEnv(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	blocked := make(map[string]bool, len(overrides))
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		blocked[key] = true
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if blocked[key] {
			continue
		}
		out = append(out, entry)
	}
	return append(out, overrides...)
}
