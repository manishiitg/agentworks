package claudecode

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// ContextBreakdown is what fills a Claude Code chat's context, in tokens, read
// from its saved transcript. Claude Code reports only the total each reply
// read; the parts are sized from what the transcript holds since the chat last
// started or compacted. Setup (Claude Code's own system prompt and tool
// definitions, plus any appended system prompt) is measured: it is what the
// first reply of that stretch read from cache, or that reply's total less the
// messages before it. The rest of the total is shared out by text size.
type ContextBreakdown struct {
	// Total is what the last reply read: new input plus cache reads and writes.
	Total int
	Setup int
	// Skills is the skill listing Claude Code adds to messages.
	Skills int
	// InstructionFiles are CLAUDE.md / AGENTS.md files Claude Code loaded.
	InstructionFiles int
	// Marked is the text between the caller's markers in user messages (for
	// example instructions a host repeats in each message), seen MarkedCount times.
	Marked      int
	MarkedCount int
	// Summary is what a compaction left of the earlier conversation.
	Summary int
	// AppNotes are Claude Code's other reminders (environment, task and agent lists).
	AppNotes int
	// Conversation is the person's messages, the replies and tool results.
	Conversation int
}

// ContextMarkers delimit a block in user messages that the caller wants sized
// on its own; End may be empty (the block runs to the end of the message).
type ContextMarkers struct{ Start, End string }

// ContextBreakdownFromTranscript reads a chat's transcript and sizes what fills
// its context. ok is false when there is no transcript or no reply with usage.
func ContextBreakdownFromTranscript(sessionID, workingDir string, markers ContextMarkers, accountHome ...string) (ContextBreakdown, bool) {
	path, err := resolveClaudeTranscriptPath(sessionID, workingDir, false, accountHome...)
	if err != nil || path == "" {
		return ContextBreakdown{}, false
	}
	f, err := claudeTranscriptOpen(path)
	if err != nil {
		return ContextBreakdown{}, false
	}
	defer f.Close()
	return contextBreakdown(f, markers)
}

const breakdownCharsPerToken = 4

type breakdownUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u breakdownUsage) total() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

type breakdownRow struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsSidechain      bool            `json:"isSidechain"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Message          json.RawMessage `json:"message"`
	Attachment       struct {
		Type string `json:"type"`
	} `json:"attachment"`
	Rendered []struct {
		Content string `json:"content"`
	} `json:"rendered"`
}

// breakdownChars counts one stretch's text by part, in characters.
type breakdownChars struct {
	skills, files, marked, summary, notes, conversation int
	markedCount                                         int
}

func contextBreakdown(r io.Reader, markers ContextMarkers) (ContextBreakdown, bool) {
	var chars breakdownChars
	var first, last *breakdownUsage
	var beforeFirst int
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var row breakdownRow
			if json.Unmarshal(line, &row) == nil && !row.IsSidechain {
				switch {
				case row.Type == "system" && row.Subtype == "compact_boundary":
					// A compaction starts a new stretch: earlier text is no longer in the context.
					chars, first, last, beforeFirst = breakdownChars{}, nil, nil, 0
				case row.Type == "user" && row.IsCompactSummary:
					chars, first, last, beforeFirst = breakdownChars{}, nil, nil, 0
					chars.summary += len(messageText(row.Message))
				case row.Type == "user":
					chars.addUser(messageText(row.Message), markers)
				case row.Type == "attachment":
					chars.addAttachment(row)
				case row.Type == "assistant":
					var message struct {
						Content json.RawMessage `json:"content"`
						Usage   *breakdownUsage `json:"usage"`
					}
					if json.Unmarshal(row.Message, &message) == nil {
						chars.conversation += len(message.Content)
						if message.Usage != nil && message.Usage.total() > 0 {
							if first == nil {
								first = message.Usage
								beforeFirst = chars.total()
							}
							last = message.Usage
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	if last == nil {
		return ContextBreakdown{}, false
	}
	out := ContextBreakdown{Total: last.total(), MarkedCount: chars.markedCount}
	out.Setup = first.CacheReadInputTokens
	if out.Setup <= 0 {
		out.Setup = first.total() - beforeFirst/breakdownCharsPerToken
	}
	out.Setup = max(0, min(out.Setup, out.Total))
	rest := out.Total - out.Setup
	sum := chars.total()
	if sum <= 0 {
		out.Conversation = rest
		return out, true
	}
	share := func(n int) int { return int(int64(rest) * int64(n) / int64(sum)) }
	out.Skills = share(chars.skills)
	out.InstructionFiles = share(chars.files)
	out.Marked = share(chars.marked)
	out.Summary = share(chars.summary)
	out.AppNotes = share(chars.notes)
	out.Conversation = rest - out.Skills - out.InstructionFiles - out.Marked - out.Summary - out.AppNotes
	return out, true
}

func (c *breakdownChars) total() int {
	return c.skills + c.files + c.marked + c.summary + c.notes + c.conversation
}

func (c *breakdownChars) addUser(text string, markers ContextMarkers) {
	if markers.Start != "" {
		for {
			start := strings.Index(text, markers.Start)
			if start < 0 {
				break
			}
			end := len(text)
			if markers.End != "" {
				if i := strings.Index(text[start:], markers.End); i >= 0 {
					end = start + i
				}
			}
			c.marked += end - start
			c.markedCount++
			text = text[:start] + text[end:]
		}
	}
	c.conversation += len(text)
}

func (c *breakdownChars) addAttachment(row breakdownRow) {
	n := 0
	for _, part := range row.Rendered {
		n += len(part.Content)
	}
	switch row.Attachment.Type {
	case "skill_listing":
		c.skills += n
	case "instructions", "nested_memory":
		c.files += n
	default:
		c.notes += n
	}
}

// messageText is a user message's text and tool results, as one string.
func messageText(raw json.RawMessage) string {
	var message struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(message.Content, &blocks) != nil {
		return string(message.Content)
	}
	var b strings.Builder
	for _, block := range blocks {
		switch {
		case block.Text != "":
			b.WriteString(block.Text)
		case len(block.Content) > 0:
			var s string
			if json.Unmarshal(block.Content, &s) == nil {
				b.WriteString(s)
			} else {
				b.Write(block.Content)
			}
		}
	}
	return b.String()
}
