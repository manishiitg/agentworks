package claudecode

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
)

// ContextItem is one piece of what fills a Claude Code chat's context, with its text, so people can see what a part
// of the breakdown is made of (each tool, each skill, each instruction file, each kind of reminder).
type ContextItem struct {
	Label string
	Text  string
	// Chars is the item's size in the context, counting every copy (a reminder added ten times counts ten times).
	Chars int
	// Count is how many times it was added, when more than once matters.
	Count int
}

// Part keys match ContextBreakdown's parts: app_setup (Claude Code's system prompt and tools), skills, files,
// resent, summary and notes.
const (
	ContextPartSetup   = "app_setup"
	ContextPartSkills  = "skills"
	ContextPartFiles   = "files"
	ContextPartResent  = "resent"
	ContextPartSummary = "summary"
	ContextPartNotes   = "notes"
)

// maxContextItemText keeps one item's text readable in a side pane; the size still counts the whole text.
const maxContextItemText = 40000

// ContextTextsFromTranscript reads a chat's transcript and returns the items behind each part, since the chat last
// started or compacted. Claude Code saves its system prompt and tool definitions only in newer versions; without
// them the setup part has no items. ok is false when there is no transcript.
func ContextTextsFromTranscript(sessionID, workingDir string, accountHome ...string) (map[string][]ContextItem, bool) {
	path, err := resolveClaudeTranscriptPath(sessionID, workingDir, false, accountHome...)
	if err != nil || path == "" {
		return nil, false
	}
	f, err := claudeTranscriptOpen(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	return contextTexts(f, workingDir), true
}

type contextTextRow struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsSidechain      bool            `json:"isSidechain"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Message          json.RawMessage `json:"message"`
	Attachment       struct {
		Type         string            `json:"type"`
		Path         string            `json:"path"`
		DisplayPath  string            `json:"displayPath"`
		Content      json.RawMessage   `json:"content"`
		Files        []json.RawMessage `json:"files"`
		SystemPrompt json.RawMessage   `json:"systemPrompt"`
		Tools        json.RawMessage   `json:"tools"`
	} `json:"attachment"`
	Rendered []struct {
		Content string `json:"content"`
	} `json:"rendered"`
}

// contextTextCollector gathers items by part; keyed items add up their copies.
type contextTextCollector struct {
	workingDir string
	order      map[string][]string
	items      map[string]map[string]*ContextItem
	setup      []ContextItem
}

func newContextTextCollector(workingDir string) *contextTextCollector {
	return &contextTextCollector{workingDir: workingDir, order: map[string][]string{}, items: map[string]map[string]*ContextItem{}}
}

func (c *contextTextCollector) reset() {
	// A compaction drops the earlier messages; the system prompt and tools stay.
	c.order, c.items = map[string][]string{}, map[string]map[string]*ContextItem{}
}

// add records one copy of an item; a later copy's text replaces the earlier one (it is the current version).
func (c *contextTextCollector) add(part, label, text string, chars int) {
	if strings.TrimSpace(text) == "" && chars == 0 {
		return
	}
	if c.items[part] == nil {
		c.items[part] = map[string]*ContextItem{}
	}
	item := c.items[part][label]
	if item == nil {
		item = &ContextItem{Label: label}
		c.items[part][label] = item
		c.order[part] = append(c.order[part], label)
	}
	item.Text, item.Chars, item.Count = text, item.Chars+chars, item.Count+1
}

func contextTexts(r io.Reader, workingDir string) map[string][]ContextItem {
	c := newContextTextCollector(workingDir)
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			c.line(line)
		}
		if err != nil {
			break
		}
	}
	out := map[string][]ContextItem{}
	if len(c.setup) > 0 {
		out[ContextPartSetup] = c.setup
	}
	for part, labels := range c.order {
		items := make([]ContextItem, 0, len(labels))
		for _, label := range labels {
			item := *c.items[part][label]
			item.Text = clipContextText(item.Text)
			items = append(items, item)
		}
		if part != ContextPartSummary && part != ContextPartResent {
			sort.SliceStable(items, func(i, j int) bool { return items[i].Chars > items[j].Chars })
		}
		out[part] = items
	}
	return out
}

func clipContextText(text string) string {
	if len(text) <= maxContextItemText {
		return text
	}
	cut := maxContextItemText
	for cut > 0 && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n…"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func (c *contextTextCollector) line(line []byte) {
	var row contextTextRow
	if json.Unmarshal(line, &row) != nil || row.IsSidechain {
		return
	}
	switch {
	case row.Type == "system" && row.Subtype == "compact_boundary":
		c.reset()
	case row.Type == "user" && row.IsCompactSummary:
		c.reset()
		text := messageText(row.Message)
		c.add(ContextPartSummary, "Summary", text, len(text))
	case row.Type == "user":
		for i, block := range resentBlocks(messageText(row.Message)) {
			c.add(ContextPartResent, "Update "+strconv.Itoa(i+1), block, len(block))
		}
	case row.Type == "attachment":
		c.attachment(row)
	}
}

// resentBlocks returns the instruction copies AgentWorks added to one message (see breakdownChars.addUser).
func resentBlocks(text string) []string {
	var blocks []string
	for {
		start := strings.Index(text, resentInstructionsStart)
		if changed := strings.Index(text, resentInstructionsChangedStart); changed >= 0 && (start < 0 || changed < start) {
			start = changed
		}
		if start < 0 {
			return blocks
		}
		end := len(text)
		if i := strings.Index(text[start:], resentInstructionsEnd); i >= 0 {
			end = start + i
		}
		blocks = append(blocks, text[start:end])
		text = text[:start] + text[end:]
	}
}

func (c *contextTextCollector) attachment(row contextTextRow) {
	a := row.Attachment
	rendered := ""
	for _, part := range row.Rendered {
		rendered += part.Content
	}
	switch a.Type {
	case "prompt_snapshot":
		c.setup = promptSnapshotItems(a.SystemPrompt, a.Tools)
	case "skill_listing":
		c.skills(jsonText(a.Content), len(rendered))
	case "instructions":
		files := 0
		for _, raw := range a.Files {
			var file struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if json.Unmarshal(raw, &file) == nil && file.Content != "" {
				c.add(ContextPartFiles, c.displayPath(file.Path), file.Content, len(file.Content))
				files += len(file.Content)
			}
		}
		if files == 0 {
			c.add(ContextPartFiles, "Instruction files", rendered, len(rendered))
		}
	case "nested_memory":
		label := c.displayPath(firstNonEmpty(a.Path, a.DisplayPath))
		c.add(ContextPartFiles, label, firstNonEmpty(jsonText(a.Content), rendered), len(rendered))
	default:
		if rendered != "" {
			c.add(ContextPartNotes, reminderLabel(a.Type), rendered, len(rendered))
		}
	}
}

// skills splits a skill listing into one item per skill ("- name: description" lines).
func (c *contextTextCollector) skills(content string, renderedChars int) {
	lines := strings.Split(content, "\n")
	found := 0
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		name, _, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		text := line
		for i+1 < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "- ") {
			i++
			text += "\n" + lines[i]
		}
		c.add(ContextPartSkills, strings.TrimSpace(name), strings.TrimSpace(text), len(text))
		found += len(text)
	}
	if found == 0 && renderedChars > 0 {
		c.add(ContextPartSkills, "Skills list", content, renderedChars)
	}
}

func (c *contextTextCollector) displayPath(path string) string {
	path = strings.TrimSpace(path)
	if dir := strings.TrimRight(strings.TrimSpace(c.workingDir), "/"); dir != "" && strings.HasPrefix(path, dir+"/") {
		return strings.TrimPrefix(path, dir+"/")
	}
	return path
}

// promptSnapshotItems lists Claude Code's system prompt and each tool it was given.
func promptSnapshotItems(systemPrompt, tools json.RawMessage) []ContextItem {
	var items []ContextItem
	var blocks []json.RawMessage
	if json.Unmarshal(systemPrompt, &blocks) == nil {
		var parts []string
		for _, block := range blocks {
			if text := jsonText(block); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		if text := strings.Join(parts, "\n\n"); text != "" {
			items = append(items, ContextItem{Label: "System prompt", Text: clipContextText(text), Chars: len(text), Count: 1})
		}
	} else if text := jsonText(systemPrompt); text != "" {
		items = append(items, ContextItem{Label: "System prompt", Text: clipContextText(text), Chars: len(text), Count: 1})
	}
	var list []map[string]json.RawMessage
	if json.Unmarshal(tools, &list) == nil {
		var toolItems []ContextItem
		for _, tool := range list {
			name := jsonText(tool["name"])
			if name == "" {
				continue
			}
			size := 0
			for _, raw := range tool {
				size += len(raw)
			}
			text := jsonText(tool["description"])
			for _, key := range []string{"schema", "input_schema", "inputSchema"} {
				if raw := tool[key]; len(raw) > 0 {
					text += "\n\nInputs:\n" + string(raw)
					break
				}
			}
			toolItems = append(toolItems, ContextItem{Label: "Tool: " + name, Text: clipContextText(text), Chars: size, Count: 1})
		}
		sort.SliceStable(toolItems, func(i, j int) bool { return toolItems[i].Chars > toolItems[j].Chars })
		items = append(items, toolItems...)
	}
	return items
}

// jsonText is a JSON string's value, or a text block's "text"; empty for anything else.
func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var block struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &block) == nil {
		return block.Text
	}
	return ""
}

var reminderLabels = map[string]string{
	"environment":            "Environment (folders, platform)",
	"date":                   "Today's date",
	"model":                  "Model name",
	"task_reminder":          "Task list reminders",
	"silent_turn_reminder":   "Progress update nudges",
	"agent_listing_delta":    "Helper agent list",
	"deferred_tools_delta":   "Newly available tools",
	"session_context":        "Account context",
	"auto_mode":              "Permission mode notes",
	"diagnostics":            "Code problems found",
	"edited_text_file":       "Files changed on disk",
	"file":                   "Files read",
	"compact_file_reference": "Files read before the summary",
	"remote_session_change":  "Commit and pull request notes",
	"queued_command":         "Messages sent during a reply",
}

// reminderLabel names a kind of Claude Code reminder in plain words.
func reminderLabel(kind string) string {
	if label := reminderLabels[kind]; label != "" {
		return label
	}
	words := strings.ReplaceAll(strings.TrimSpace(kind), "_", " ")
	if words == "" {
		return "Other reminders"
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
