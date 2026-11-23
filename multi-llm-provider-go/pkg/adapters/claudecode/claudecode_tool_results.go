package claudecode

import (
	"bufio"
	"encoding/json"
	"strings"
	"time"
)

// TranscriptToolResult is one completed tool call as Claude Code recorded it.
type TranscriptToolResult struct {
	ToolCallID string
	ToolName   string
	Result     string
	StartedAt  time.Time
	EndedAt    time.Time
}

// Duration is the tool's real runtime, from the call to its result.
func (r TranscriptToolResult) Duration() time.Duration {
	if r.StartedAt.IsZero() || r.EndedAt.IsZero() || r.EndedAt.Before(r.StartedAt) {
		return 0
	}
	return r.EndedAt.Sub(r.StartedAt)
}

// ToolResultsFromTranscript reads completed tool calls out of a Claude Code
// session's own transcript, keyed by the tool-call id the model assigned.
//
// PLAT-141. The platform derives its tool_call_start / tool_call_end events
// from the live stream, and for some calls the end is never produced — measured
// on tectonicusadaytrading, a call whose transcript shows tool_use at
// 15:52:26.430Z and tool_result at 15:52:26.471Z, 41 milliseconds apart, for
// which no end event exists under any session. The UI was left showing a
// finished command as unresolved, and the compensating settle displayed a
// fabricated 45.4-second duration because the only interval it could measure
// was how long it had waited.
//
// The transcript is the CLI's own record and is complete: the same session
// shows 215 tool_use and 215 tool_result. Reading the result from there gives
// both the real output and the real duration, which is why this exists rather
// than a larger grace window — the missing events are not late, they are never
// emitted, and no amount of waiting produces one.
//
// Best-effort by design: an unreadable or absent transcript returns nothing
// rather than an error, because the caller's fallback is simply to show what it
// already has.
func ToolResultsFromTranscript(sessionID, workingDir string) map[string]TranscriptToolResult {
	if !isClaudeTranscriptSessionID(sessionID) {
		return nil
	}
	f, _, err := openClaudeTranscript(sessionID, workingDir)
	if err != nil {
		return nil
	}
	defer f.Close()

	type contentBlock struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	type row struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Content []contentBlock `json:"content"`
		} `json:"message"`
	}

	results := map[string]TranscriptToolResult{}
	scanner := bufio.NewScanner(f)
	// Tool results carry whole command outputs; the default line limit is far
	// too small for them.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var r row
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
		for _, block := range r.Message.Content {
			switch block.Type {
			case "tool_use":
				if block.ID == "" {
					continue
				}
				entry := results[block.ID]
				entry.ToolCallID = block.ID
				entry.ToolName = block.Name
				entry.StartedAt = at
				results[block.ID] = entry
			case "tool_result":
				if block.ToolUseID == "" {
					continue
				}
				entry := results[block.ToolUseID]
				entry.ToolCallID = block.ToolUseID
				entry.EndedAt = at
				entry.Result = transcriptToolResultText(block.Content)
				results[block.ToolUseID] = entry
			}
		}
	}
	// A tool_use with no matching tool_result is a call that genuinely has no
	// answer yet; returning it would let a caller present an empty output as the
	// tool's own.
	for id, entry := range results {
		if entry.EndedAt.IsZero() {
			delete(results, id)
		}
	}
	return results
}

// transcriptToolResultText flattens a tool_result's content, which Claude Code
// writes either as a plain string or as a list of typed blocks.
func transcriptToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return strings.TrimSpace(asString)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if text := strings.TrimSpace(b.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
