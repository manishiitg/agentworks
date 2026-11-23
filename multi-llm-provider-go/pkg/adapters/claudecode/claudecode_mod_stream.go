package claudecode

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// claudeModStreamItem is one piece of a model request the mod streamed: text,
// thinking, a tool call's start, its input as written, or the request's stop.
type claudeModStreamItem struct {
	Kind       string          `json:"kind"`
	Index      int             `json:"index"`
	Text       string          `json:"text,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	JSON       string          `json:"json,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Usage      *claudeModUsage `json:"usage,omitempty"`
}

// claudeModUsage is one model request's token counts, as the API spells them.
type claudeModUsage struct {
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheReadTokens     int    `json:"cache_read_input_tokens"`
	CacheCreationTokens int    `json:"cache_creation_input_tokens"`
	Model               string `json:"model"`
}

type claudeModPendingTool struct {
	id, name string
	args     strings.Builder
}

// claudeModStreamer turns the mod's events of one turn into stream chunks, in
// the order they happened, each event once.
type claudeModStreamer struct {
	sessionName string
	sinceMS     int64
	seen        map[string]bool
	tools       map[string]*claudeModPendingTool // step:index -> tool being written
	toolStarts  map[string]time.Time             // tool call id -> when it was complete
	textBlock   string                           // step:index of the text block being streamed
}

func newClaudeModStreamer(sessionName string, turnStart time.Time) *claudeModStreamer {
	return &claudeModStreamer{
		sessionName: sessionName,
		sinceMS:     turnStart.UnixMilli(),
		seen:        map[string]bool{},
		tools:       map[string]*claudeModPendingTool{},
		toolStarts:  map[string]time.Time{},
	}
}

func (s *claudeModStreamer) poll() []llmtypes.StreamChunk {
	var chunks []llmtypes.StreamChunk
	for _, event := range readClaudeModEvents(s.sessionName) {
		if event.At < s.sinceMS || s.seen[event.name] {
			continue
		}
		s.seen[event.name] = true
		chunks = append(chunks, s.chunks(event)...)
	}
	return chunks
}

func (s *claudeModStreamer) chunks(event claudeModEvent) []llmtypes.StreamChunk {
	meta := func() map[string]interface{} {
		// "transcript": clean structured output, not a screen capture.
		return map[string]interface{}{"claude_code_session_id": event.SessionID, "claude_code_stream_source": "transcript", "claude_code_stream_origin": "claude_mod"}
	}
	var out []llmtypes.StreamChunk
	switch event.Type {
	case "stream":
		for _, item := range event.Items {
			key := event.Step + ":" + itoa(item.Index)
			switch item.Kind {
			case "text":
				if item.Text != "" {
					text := item.Text
					// A new text block (after a tool call, or a new request) starts a paragraph.
					if s.textBlock != "" && s.textBlock != key {
						text = "\n\n" + text
					}
					s.textBlock = key
					m := meta()
					m[llmtypes.ContentDeltaMetadataKey] = true
					out = append(out, llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: text, Metadata: m})
				}
			case "thinking":
				m := meta()
				m["thinking_active"] = true
				m[llmtypes.ContentDeltaMetadataKey] = true
				out = append(out, llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: item.Text, Metadata: m})
			case "tool":
				s.tools[key] = &claudeModPendingTool{id: item.ID, name: item.Name}
			case "input":
				if tool := s.tools[key]; tool != nil && item.JSON != "" {
					tool.args.WriteString(item.JSON)
					out = append(out, llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallDelta, ToolName: tool.name, ToolCallID: tool.id, ToolArgs: tool.args.String(), Metadata: meta()})
				}
			case "stop":
				// The request is complete: its tool calls now run, with their whole input.
				var keys []string
				for k := range s.tools {
					if strings.HasPrefix(k, event.Step+":") {
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				for _, k := range keys {
					tool := s.tools[k]
					out = append(out, llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: tool.name, ToolCallID: tool.id, ToolArgs: tool.args.String(), Metadata: meta()})
					s.toolStarts[tool.id] = time.UnixMilli(event.At)
					delete(s.tools, k)
				}
			}
		}
	case "row":
		if event.Door != "tool-result" {
			break
		}
		var message struct {
			Content []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			} `json:"content"`
		}
		if json.Unmarshal(event.Message, &message) != nil {
			break
		}
		for _, block := range message.Content {
			if block.Type != "tool_result" {
				continue
			}
			chunk := llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolCallID: block.ToolUseID, ToolResult: flattenToolResultContent(block.Content), Metadata: meta()}
			if started, ok := s.toolStarts[block.ToolUseID]; ok {
				chunk.ToolDuration = time.UnixMilli(event.At).Sub(started)
			}
			out = append(out, chunk)
		}
	case "compaction":
		now := time.UnixMilli(event.At).UTC()
		end := claudeCompactionFromBoundary("claude-compact-"+itoa(int(event.At)), &claudeCompactMetadata{Trigger: event.Trigger, PreTokens: int(event.TokensBefore), PostTokens: int(event.TokensAfter)}, now)
		chunk := llmtypes.ContextCompactionChunk(end)
		chunk.Metadata = meta()
		out = append(out, chunk)
	}
	return out
}

// startClaudeModStream sends the turn's streamed output to streamChan until
// stopped. Stopping waits for one last read, so nothing the mod wrote before
// the turn ended is lost, and never sends after it returns.
func startClaudeModStream(parent context.Context, sessionName string, turnStart time.Time, streamChan chan<- llmtypes.StreamChunk) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	streamer := newClaudeModStreamer(sessionName, turnStart)
	send := func(chunks []llmtypes.StreamChunk, wait <-chan struct{}) {
		for _, chunk := range chunks {
			select {
			case streamChan <- chunk:
			case <-wait:
				return
			}
		}
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			send(streamer.poll(), ctx.Done())
			select {
			case <-ctx.Done():
				// The last read gets a short window of its own.
				final, finalCancel := context.WithTimeout(context.Background(), 2*time.Second)
				send(streamer.poll(), final.Done())
				finalCancel()
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// claudeModTurnRecord is what the mod recorded for one turn: the history rows
// in the transcript's own shape, the prompts it answered, and the model
// requests' token counts.
type claudeModTurnRecord struct {
	messages []llmtypes.MessageContent
	turnText string
	roots    []string
	usage    claudeModUsage
	requests int
}

func claudeModRecordTurn(sessionName string, turnStart time.Time) claudeModTurnRecord {
	var record claudeModTurnRecord
	var lines []string
	var texts []string
	sinceMS := turnStart.UnixMilli()
	for _, event := range readClaudeModEvents(sessionName) {
		if event.At < sinceMS {
			continue
		}
		switch event.Type {
		case "row":
			var message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(event.Message, &message) != nil || message.Role == "" {
				continue
			}
			if message.Role == "user" && event.Door != "tool-result" && event.UUID != "" {
				record.roots = append(record.roots, event.UUID)
			}
			if message.Role == "assistant" {
				texts = append(texts, claudeModRowText(message.Content)...)
			}
			// Rows of one model request share its id, as the transcript's do.
			line, _ := json.Marshal(map[string]interface{}{
				"type":      message.Role,
				"timestamp": time.UnixMilli(event.At).UTC().Format(time.RFC3339Nano),
				"uuid":      event.UUID,
				"message":   map[string]interface{}{"id": event.Step, "role": message.Role, "content": message.Content},
			})
			lines = append(lines, string(line))
		case "stream":
			for _, item := range event.Items {
				if item.Kind != "stop" || item.Usage == nil {
					continue
				}
				record.requests++
				record.usage.InputTokens += item.Usage.InputTokens
				record.usage.OutputTokens += item.Usage.OutputTokens
				record.usage.CacheReadTokens += item.Usage.CacheReadTokens
				record.usage.CacheCreationTokens += item.Usage.CacheCreationTokens
				if item.Usage.Model != "" {
					record.usage.Model = item.Usage.Model
				}
			}
		}
	}
	record.messages = parseClaudeTranscriptMessages(strings.NewReader(strings.Join(lines, "\n")), time.Time{}, false)
	record.turnText = strings.TrimSpace(strings.Join(texts, "\n\n"))
	return record
}

func claudeModRowText(content json.RawMessage) []string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			texts = append(texts, strings.TrimSpace(block.Text))
		}
	}
	return texts
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
