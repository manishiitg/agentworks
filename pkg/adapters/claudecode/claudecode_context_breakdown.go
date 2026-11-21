package claudecode

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"
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
	// Resent is instructions sent again in messages because they changed while
	// the chat was resumed (each copy stays in the context), sent ResentCount times.
	Resent      int
	ResentCount int
	// Summary is what a compaction left of the earlier conversation.
	Summary int
	// AppNotes are Claude Code's other reminders (environment, task and agent lists).
	AppNotes int
	// Conversation is the person's messages, the replies and tool results.
	Conversation int
}

// ContextBreakdownFromTranscript reads a chat's transcript and sizes what fills
// its context. ok is false when there is no transcript or no reply with usage.
// Transcripts grow to tens of megabytes, so each file's tally is kept and only
// lines added since the last call are read.
func ContextBreakdownFromTranscript(sessionID, workingDir string, accountHome ...string) (ContextBreakdown, bool) {
	path, err := resolveClaudeTranscriptPath(sessionID, workingDir, false, accountHome...)
	if err != nil || path == "" {
		return ContextBreakdown{}, false
	}
	f, err := claudeTranscriptOpen(path)
	if err != nil {
		return ContextBreakdown{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ContextBreakdown{}, false
	}
	breakdownTallies.Lock()
	defer breakdownTallies.Unlock()
	tally := breakdownTallies.byPath[path]
	if tally == nil || info.Size() < tally.offset {
		if tally == nil && len(breakdownTallies.byPath) >= maxBreakdownTallies {
			for evict := range breakdownTallies.byPath {
				delete(breakdownTallies.byPath, evict)
				break
			}
		}
		tally = &breakdownTally{}
		breakdownTallies.byPath[path] = tally
	}
	if _, err := f.Seek(tally.offset, io.SeekStart); err != nil {
		return ContextBreakdown{}, false
	}
	tally.read(f)
	return tally.result()
}

var breakdownTallies = struct {
	sync.Mutex
	byPath map[string]*breakdownTally
}{byPath: make(map[string]*breakdownTally)}

const maxBreakdownTallies = 256

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
	skills, files, resent, summary, notes, conversation int
	resentCount                                         int
}

// breakdownTally is one transcript's running count since its last compaction.
type breakdownTally struct {
	offset      int64
	chars       breakdownChars
	first, last *breakdownUsage
	beforeFirst int
}

func contextBreakdown(r io.Reader) (ContextBreakdown, bool) {
	var tally breakdownTally
	tally.read(r)
	return tally.result()
}

// read counts complete lines from r; a partly written last line is left for the next read.
func (t *breakdownTally) read(r io.Reader) {
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF && len(line) > 0 && json.Valid(line) {
				t.add(line)
				t.offset += int64(len(line))
			}
			return
		}
		t.offset += int64(len(line))
		t.add(line)
	}
}

func (t *breakdownTally) add(line []byte) {
	var row breakdownRow
	if json.Unmarshal(line, &row) != nil || row.IsSidechain {
		return
	}
	switch {
	case row.Type == "system" && row.Subtype == "compact_boundary":
		// A compaction starts a new stretch: earlier text is no longer in the context.
		t.chars, t.first, t.last, t.beforeFirst = breakdownChars{}, nil, nil, 0
	case row.Type == "user" && row.IsCompactSummary:
		t.chars, t.first, t.last, t.beforeFirst = breakdownChars{}, nil, nil, 0
		t.chars.summary += len(messageText(row.Message))
	case row.Type == "user":
		t.chars.addUser(messageText(row.Message))
	case row.Type == "attachment":
		t.chars.addAttachment(row)
	case row.Type == "assistant":
		var message struct {
			Content json.RawMessage `json:"content"`
			Usage   *breakdownUsage `json:"usage"`
		}
		if json.Unmarshal(row.Message, &message) != nil {
			return
		}
		t.chars.conversation += len(message.Content)
		if message.Usage != nil && message.Usage.total() > 0 {
			if t.first == nil {
				t.first = message.Usage
				t.beforeFirst = t.chars.total()
			}
			t.last = message.Usage
		}
	}
}

func (t *breakdownTally) result() (ContextBreakdown, bool) {
	chars, first, last, beforeFirst := t.chars, t.first, t.last, t.beforeFirst
	if last == nil {
		return ContextBreakdown{}, false
	}
	out := ContextBreakdown{Total: last.total(), ResentCount: chars.resentCount}
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
	out.Resent = share(chars.resent)
	out.Summary = share(chars.summary)
	out.AppNotes = share(chars.notes)
	out.Conversation = rest - out.Skills - out.InstructionFiles - out.Resent - out.Summary - out.AppNotes
	return out, true
}

func (c *breakdownChars) total() int {
	return c.skills + c.files + c.resent + c.summary + c.notes + c.conversation
}

func (c *breakdownChars) addUser(text string) {
	for {
		start := strings.Index(text, resentInstructionsStart)
		if start < 0 {
			break
		}
		end := len(text)
		if i := strings.Index(text[start:], resentInstructionsEnd); i >= 0 {
			end = start + i
		}
		c.resent += end - start
		c.resentCount++
		text = text[:start] + text[end:]
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
