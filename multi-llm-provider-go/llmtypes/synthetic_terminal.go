package llmtypes

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// transportLabel returns the explicit transport ("api" / "structured_cli"
// / "tmux") when the caller set one, otherwise falls back to "non_tmux"
// for backwards compatibility with the old hard-coded value.
func (t *SyntheticTerminal) transportLabel() string {
	if t == nil || t.transport == "" {
		return "non_tmux"
	}
	return t.transport
}

// SyntheticTerminal accumulates a terminal-style log for transports
// that have no real tmux pane to snapshot (structured CLI + direct
// API providers). The log is emitted as StreamChunkTypeTerminal chunks
// with tmux-replace semantics: each chunk carries the FULL buffer so
// the frontend can render it with the existing terminal-pane code path.
//
// Why a separate type instead of doing this inline in each adapter:
// every adapter needs the same line cap, the same non-blocking emit,
// the same metadata shape. Five copies drift.
type SyntheticTerminal struct {
	mu        sync.Mutex
	lines     []string
	rows      []TerminalRow
	maxLines  int
	provider  string
	model     string
	transport string
	ch        chan<- StreamChunk
}

// TerminalRow is the role-aware terminal-pane representation emitted in
// StreamChunk.Metadata["rows"]. The text buffer remains available for debug
// and old consumers, but rows are the source of truth for UI rendering.
type TerminalRow struct {
	Kind         string `json:"kind"`
	Text         string `json:"text,omitempty"`
	Name         string `json:"name,omitempty"`
	Args         string `json:"args,omitempty"`
	Result       string `json:"result,omitempty"`
	ResultPrefix string `json:"result_prefix,omitempty"`
}

// NewSyntheticTerminal builds a log bound to a single StreamChan. If
// ch is nil, the returned terminal is a no-op (Append + Emit are
// safe to call but emit nothing). The caller passes provider/model
// for metadata enrichment.
func NewSyntheticTerminal(ch chan<- StreamChunk, provider, model string) *SyntheticTerminal {
	return NewSyntheticTerminalWithTransport(ch, provider, model, "")
}

// NewSyntheticTerminalWithTransport is like NewSyntheticTerminal but
// also records the actual transport class ("api" / "structured_cli" /
// "tmux"). The chip in the frontend reads this from the chunk
// metadata so a claude (tmux) call is labelled tmux·claude code
// rather than the wrong-by-default api·claude code.
func NewSyntheticTerminalWithTransport(ch chan<- StreamChunk, provider, model, transport string) *SyntheticTerminal {
	return &SyntheticTerminal{
		ch:        ch,
		provider:  provider,
		model:     model,
		transport: transport,
		maxLines:  200, // keep the pane bounded; older lines drop off the top
	}
}

// Enabled reports whether emits will reach a consumer.
func (t *SyntheticTerminal) Enabled() bool {
	return t != nil && t.ch != nil
}

// Header appends an opening banner ($ prompt + summary) and emits.
func (t *SyntheticTerminal) Header(cmdline string) {
	if !t.Enabled() {
		return
	}
	t.appendRow(TerminalRow{Kind: "banner", Text: cmdline})
	t.emit()
}

// Line appends a single formatted line and emits a snapshot.
func (t *SyntheticTerminal) Line(format string, args ...interface{}) {
	if !t.Enabled() {
		return
	}
	t.appendLine(fmt.Sprintf(format, args...))
	t.emit()
}

// Context records a workflow/step context line.
func (t *SyntheticTerminal) Context(text string) {
	if !t.Enabled() {
		return
	}
	t.appendRow(TerminalRow{Kind: "context", Text: strings.TrimPrefix(text, "↳ ")})
	t.emit()
}

// UserText appends a role-aware user message row.
func (t *SyntheticTerminal) UserText(text string) {
	if !t.Enabled() || strings.TrimSpace(text) == "" {
		return
	}
	t.appendRow(TerminalRow{Kind: "user", Text: strings.TrimSpace(text)})
	t.emit()
}

// AssistantHistoryText appends a complete assistant message from conversation
// history as one role-aware row.
func (t *SyntheticTerminal) AssistantHistoryText(text string) {
	if !t.Enabled() || strings.TrimSpace(text) == "" {
		return
	}
	t.appendRow(TerminalRow{Kind: "asst", Text: strings.TrimSpace(text)})
	t.emit()
}

// Row appends an already-classified terminal row.
func (t *SyntheticTerminal) Row(row TerminalRow) {
	if !t.Enabled() {
		return
	}
	t.appendRow(row)
	t.emit()
}

// ToolStart records a tool invocation. Args are truncated for display.
func (t *SyntheticTerminal) ToolStart(name, args string) {
	if !t.Enabled() {
		return
	}
	t.appendRow(TerminalRow{Kind: "tool", Name: name, Args: truncate(args, 120)})
	t.emit()
}

// ToolEnd records a tool result with duration. Output is truncated.
func (t *SyntheticTerminal) ToolEnd(name, result string, dur time.Duration) {
	if !t.Enabled() {
		return
	}
	resultSummary := truncate(strings.ReplaceAll(result, "\n", " "), 200)
	resultText := fmt.Sprintf("%s · %s", dur.Round(time.Millisecond), resultSummary)
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.rows) - 1; i >= 0; i-- {
		if t.rows[i].Kind == "tool" && t.rows[i].Name == name && t.rows[i].Result == "" {
			t.rows[i].Result = resultText
			t.rows[i].ResultPrefix = "✓"
			t.rebuildLinesLocked()
			t.trim()
			t.emitLocked()
			return
		}
	}
	t.rows = append(t.rows, TerminalRow{
		Kind:         "tool",
		Name:         name,
		Result:       resultText,
		ResultPrefix: "✓",
	})
	t.rebuildLinesLocked()
	t.trim()
	t.emitLocked()
}

// AssistantText appends streamed assistant text. Token-level deltas
// concatenate onto the trailing assistant pane line so the pane reads
// like a streaming reply. When the delta carries newlines, each
// newline-separated segment becomes its own pane line with the "  "
// continuation prefix so the frontend parser keeps classifying the
// whole block as the same assistant row — the row coalesces back
// together with newlines preserved, which is what ReactMarkdown needs
// to render structure (lists, headings, paragraph breaks, fenced code)
// instead of one collapsed run-on string.
func (t *SyntheticTerminal) AssistantText(delta string) {
	if !t.Enabled() || delta == "" {
		return
	}
	t.mu.Lock()
	if len(t.rows) > 0 && t.rows[len(t.rows)-1].Kind == "asst" {
		t.rows[len(t.rows)-1].Text += delta
	} else {
		t.rows = append(t.rows, TerminalRow{Kind: "asst", Text: delta})
	}
	t.rebuildLinesLocked()
	t.trim()
	t.mu.Unlock()
	t.emit()
}

// Done records a final summary line.
func (t *SyntheticTerminal) Done(durationMs int64, summary string) {
	if !t.Enabled() {
		return
	}
	dur := humanizeDuration(durationMs)
	if summary != "" {
		t.appendRow(TerminalRow{Kind: "done", Text: fmt.Sprintf("[done · %s · %s]", dur, summary)})
	} else {
		t.appendRow(TerminalRow{Kind: "done", Text: fmt.Sprintf("[done · %s]", dur)})
	}
	t.emit()
}

// humanizeDuration converts a millisecond count into a compact
// human-readable form so terminal users don't have to mentally
// convert "30691ms" into "30.7s". Scales:
//   - under 1s   → "234ms"
//   - under 60s  → "30.7s"
//   - under 1h   → "2m 14s"
//   - 1h+        → "1h 5m"
func humanizeDuration(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	sec := float64(ms) / 1000
	if sec < 60 {
		return fmt.Sprintf("%.1fs", sec)
	}
	secs := ms / 1000
	mins := secs / 60
	rem := secs % 60
	if mins < 60 {
		return fmt.Sprintf("%dm %ds", mins, rem)
	}
	hours := mins / 60
	remMin := mins % 60
	return fmt.Sprintf("%dh %dm", hours, remMin)
}

// Error records a terminal failure line.
func (t *SyntheticTerminal) Error(err error) {
	if !t.Enabled() {
		return
	}
	t.appendRow(TerminalRow{Kind: "error", Text: err.Error()})
	t.emit()
}

func (t *SyntheticTerminal) appendLine(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	t.rows = append(t.rows, TerminalRow{Kind: "plain", Text: line})
	t.trim()
}

func (t *SyntheticTerminal) appendRow(row TerminalRow) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rows = append(t.rows, row)
	t.rebuildLinesLocked()
	t.trim()
}

// trim drops the oldest lines if the buffer exceeds maxLines.
// Caller holds t.mu.
func (t *SyntheticTerminal) trim() {
	if len(t.lines) > t.maxLines {
		t.lines = t.lines[len(t.lines)-t.maxLines:]
	}
	if len(t.rows) > t.maxLines {
		t.rows = t.rows[len(t.rows)-t.maxLines:]
		t.rebuildLinesLocked()
	}
}

func (t *SyntheticTerminal) snapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

func (t *SyntheticTerminal) snapshotRows() []TerminalRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := make([]TerminalRow, len(t.rows))
	copy(rows, t.rows)
	return rows
}

func (t *SyntheticTerminal) rebuildLinesLocked() {
	t.lines = t.lines[:0]
	for _, row := range t.rows {
		t.lines = append(t.lines, terminalRowLines(row)...)
	}
}

func terminalRowLines(row TerminalRow) []string {
	switch row.Kind {
	case "banner":
		return []string{"$ " + row.Text}
	case "context":
		return []string{"↳ " + row.Text}
	case "user":
		return prefixedTextLines("> user: ", row.Text)
	case "asst":
		return prefixedTextLines("< asst: ", row.Text)
	case "tool":
		line := fmt.Sprintf("→ tool: %s(%s)", row.Name, row.Args)
		if row.Result == "" {
			return []string{line}
		}
		prefix := row.ResultPrefix
		if prefix == "" {
			prefix = "✓"
		}
		return []string{line, fmt.Sprintf("%s result %s: %s", prefix, row.Name, row.Result)}
	case "done":
		return []string{row.Text}
	case "error":
		return []string{"[error] " + row.Text}
	default:
		return []string{row.Text}
	}
}

func prefixedTextLines(prefix, text string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, prefix+line)
		} else {
			out = append(out, "  "+line)
		}
	}
	return out
}

// emit sends the current buffer as a Terminal chunk. Non-blocking:
// if the consumer is slow we drop this snapshot — the next emit
// will carry the freshest state anyway.
//
// Adapters own the close of opts.StreamChan and some close it before
// WithObservability's terminal Done fires (e.g. vertex defers close
// inside generateContentStreaming). A send to a closed channel
// panics, so we recover here — losing the final Done snapshot is
// preferable to crashing every vertex/structured-CLI call.
func (t *SyntheticTerminal) emit() {
	defer func() { _ = recover() }()
	snap := t.snapshot()
	rows := t.snapshotRows()
	select {
	case t.ch <- StreamChunk{
		Type:    StreamChunkTypeTerminal,
		Content: snap,
		Metadata: map[string]interface{}{
			// kind=terminal is the gate the orchestrator's
			// terminals store checks; without it our synthetic
			// snapshots are silently dropped before reaching the
			// /api/terminals endpoint the frontend pane reads.
			"kind":      "terminal",
			"source":    "synthetic",
			"provider":  t.provider,
			"model":     t.model,
			"transport": t.transportLabel(),
			"rows":      rows,
		},
	}:
	default:
	}
}

func (t *SyntheticTerminal) emitLocked() {
	defer func() { _ = recover() }()
	snap := strings.Join(t.lines, "\n")
	rows := make([]TerminalRow, len(t.rows))
	copy(rows, t.rows)
	select {
	case t.ch <- StreamChunk{
		Type:    StreamChunkTypeTerminal,
		Content: snap,
		Metadata: map[string]interface{}{
			"kind":      "terminal",
			"source":    "synthetic",
			"provider":  t.provider,
			"model":     t.model,
			"transport": t.transportLabel(),
			"rows":      rows,
		},
	}:
	default:
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
