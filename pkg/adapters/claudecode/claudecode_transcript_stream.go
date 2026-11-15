package claudecode

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
)

// claudeInteractiveStreamTranscriptEnabled reports whether to tail the CLI's
// JSONL transcript for structured streaming. Opt-in (default OFF): the existing
// pane-snapshot stream is unaffected when this is disabled. Set per call via
// WithStreamTranscript — there is no environment-variable fallback.
func claudeInteractiveStreamTranscriptEnabled(opts *llmtypes.CallOptions) bool {
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if v, ok := opts.Metadata.Custom[MetadataKeyStreamTranscript].(bool); ok {
			return v
		}
	}
	return false
}

// claudeTranscriptEvent is one structured item recovered from the transcript
// during a turn: assistant text (Text set), the start of a tool call (ToolName
// set), or a tool's result (IsToolEnd set). Kept minimal — this is the mid-turn
// streaming signal, not the authoritative end-of-turn reconstruction
// (readClaudeTranscriptMessages).
type claudeTranscriptEvent struct {
	Text      string
	Reasoning string
	// IsThinking preserves Claude's explicit thinking activity even when the
	// provider intentionally redacts the thinking text to an empty string.
	// Without this bit an empty thinking block is indistinguishable from an
	// empty/default transcript event and the UI remains silent until the final
	// answer arrives.
	IsThinking bool
	ToolName   string
	ToolCallID string
	// ToolArgs is the call's arguments, carried onto the START chunk (the only
	// chunk with a field for them — ToolCallEnd has none). Without this the
	// mcpagent layer receives a tool call with an empty body, so a product can
	// render only the bare tool name and an agentic reviewer cannot tell a
	// bridge-routed call from raw native shell use.
	ToolArgs string
	// IsToolEnd distinguishes a tool_result row from a tool_use row; both
	// carry ToolCallID, so this is the only way to tell them apart once
	// mapped onto the shared event shape.
	IsToolEnd    bool
	ToolResult   string
	ToolDuration time.Duration
	// Compaction is set for a system/compact_boundary row.
	Compaction *llmtypes.ContextCompaction
}

func transcriptEventToChunk(sessionID string, e claudeTranscriptEvent) llmtypes.StreamChunk {
	meta := map[string]interface{}{
		"claude_code_session_id":    sessionID,
		"claude_code_stream_source": "transcript",
	}
	if e.Compaction != nil {
		chunk := llmtypes.ContextCompactionChunk(*e.Compaction)
		chunk.Metadata = meta
		return chunk
	}
	if e.IsToolEnd {
		return llmtypes.StreamChunk{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolCallID:   e.ToolCallID,
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
	if e.IsThinking {
		meta["thinking_active"] = true
		return llmtypes.StreamChunk{
			Type:     llmtypes.StreamChunkTypeReasoning,
			Content:  e.Reasoning,
			Metadata: meta,
		}
	}
	return llmtypes.StreamChunk{
		Type:     llmtypes.StreamChunkTypeContent,
		Content:  e.Text,
		Metadata: meta,
	}
}

// readClaudeTranscriptEventsFromFile is the file-level core, split out so tests
// can drive it against a growing fixture without a real ~/.claude tree.
func readClaudeTranscriptEventsFromFile(path string, offset int64, turnStart time.Time, pendingToolStarts map[string]time.Time) ([]claudeTranscriptEvent, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	return readClaudeTranscriptEventsFromOpenFile(f, offset, turnStart, pendingToolStarts)
}

func readClaudeTranscriptEventsFromOpenFile(f *os.File, offset int64, turnStart time.Time, pendingToolStarts map[string]time.Time, lineageState ...*nativereplies.ClaudeLineage) ([]claudeTranscriptEvent, int64, error) {
	lineage := &nativereplies.ClaudeLineage{}
	if len(lineageState) > 0 && lineageState[0] != nil {
		lineage = lineageState[0]
	}
	// Seek even when offset is zero. A previous poll may have read an
	// incomplete first line to EOF without advancing the logical offset; the
	// next poll must reread that partial line together with its appended tail.
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}
	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL < 0 {
		// No complete line appended yet — hold the offset and wait.
		return nil, offset, nil
	}
	consumed := data[:lastNL+1]
	nextOffset := offset + int64(len(consumed))

	var events []claudeTranscriptEvent
	for _, raw := range bytes.Split(consumed, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var e struct {
			Type            string                 `json:"type"`
			Subtype         string                 `json:"subtype"`
			ToolUseResult   json.RawMessage        `json:"toolUseResult"`
			UUID            string                 `json:"uuid"`
			Timestamp       string                 `json:"timestamp"`
			Message         json.RawMessage        `json:"message"`
			CompactMetadata *claudeCompactMetadata `json:"compactMetadata"`
		}
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		root := lineage.Observe(line)
		if e.Type == "system" && e.Subtype == "compact_boundary" {
			rowTime, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
			if !turnStart.IsZero() && !rowTime.IsZero() && rowTime.Before(turnStart) {
				continue
			}
			c := claudeCompactionFromBoundary(e.UUID, e.CompactMetadata, rowTime)
			events = append(events, claudeTranscriptEvent{Compaction: &c})
			continue
		}
		if e.Type != "assistant" && e.Type != "user" {
			continue
		}
		var rowTime time.Time
		if e.Timestamp != "" {
			rowTime, _ = time.Parse(time.RFC3339Nano, e.Timestamp)
		}
		if !turnStart.IsZero() && !rowTime.IsZero() && rowTime.Before(turnStart) {
			continue
		}
		if !nativereplies.ForegroundMayPublish(root) {
			continue
		}

		switch e.Type {
		case "assistant":
			var am struct {
				Content []claudeAssistantContentBlock `json:"content"`
			}
			if err := json.Unmarshal(e.Message, &am); err != nil {
				continue
			}
			// Thinking is deliberately not part of the replayed conversation
			// history, but it is a first-class structured progress stream.
			// Text/tool blocks still use the common reconstruction mapping.
			for _, block := range am.Content {
				if block.Type == "thinking" {
					events = append(events, claudeTranscriptEvent{
						Reasoning:  strings.TrimSpace(block.Thinking),
						IsThinking: true,
					})
				}
			}
			// Reuse the same block→parts mapping the end-of-turn reader uses,
			// so streaming and final reconstruction never diverge.
			for _, p := range assistantBlocksToParts(am.Content) {
				switch v := p.(type) {
				case llmtypes.TextContent:
					if v.Text != "" {
						events = append(events, claudeTranscriptEvent{Text: v.Text})
					}
				case llmtypes.ToolCall:
					name, args := "", ""
					if v.FunctionCall != nil {
						name = v.FunctionCall.Name
						args = v.FunctionCall.Arguments
					}
					events = append(events, claudeTranscriptEvent{ToolName: name, ToolCallID: v.ID, ToolArgs: args})
					// Recorded even when rowTime is zero (missing/unparseable
					// timestamp): the matching tool_result below treats a
					// zero start as "not measured" and reports 0 rather than
					// a fabricated duration.
					if pendingToolStarts != nil && v.ID != "" {
						pendingToolStarts[v.ID] = rowTime
					}
				}
			}

		case "user":
			// Claude feeds a tool's result back to the model as a LATER
			// "user"-role row (verified against the same CLI this adapter's
			// structured transport drives — see claudecode_structured_adapter.go).
			// This is the transcript's only record of that result and its
			// real duration; without it, a tmux-transport tool call has a
			// start and nothing else.
			var um struct {
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(e.Message, &um); err != nil {
				continue
			}
			var blocks []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				IsError   bool            `json:"is_error"`
				Content   json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(um.Content, &blocks); err != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type != "tool_result" || b.ToolUseID == "" {
					continue
				}
				var duration time.Duration
				if pendingToolStarts != nil {
					if start, ok := pendingToolStarts[b.ToolUseID]; ok {
						if !start.IsZero() && !rowTime.IsZero() && rowTime.After(start) {
							duration = rowTime.Sub(start)
						}
						delete(pendingToolStarts, b.ToolUseID)
					}
				}
				result := flattenToolResultContent(b.Content)
				// Claude's TaskList display is prose; retain its actual structured
				// task list from the journal for the retained tool-result consumer.
				var taskList struct {
					Tasks json.RawMessage `json:"tasks"`
				}
				if len(blocks) == 1 && json.Unmarshal(e.ToolUseResult, &taskList) == nil && len(taskList.Tasks) > 0 {
					result = string(e.ToolUseResult)
				}
				if b.IsError {
					result = "[ERROR] " + result
				}
				events = append(events, claudeTranscriptEvent{
					IsToolEnd:    true,
					ToolCallID:   b.ToolUseID,
					ToolResult:   result,
					ToolDuration: duration,
				})
			}
		}
	}
	return events, nextOffset, nil
}
