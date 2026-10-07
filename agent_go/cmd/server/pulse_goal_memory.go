package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// Goal memory (PLAT-697 phase 3, docs/design/pulse_goal_owner.md "Memory: the
// goal over time"): how the goal is managed, beside soul.md (what the goal
// is). It lives in the workflow's existing memory area as its own file,
// memory/goal.md, next to the Builder's project MEMORY.md, so neither
// rewrites the other's entries.
//
//   - One line per entry: "- YYYY-MM-DD [source] text", source being an
//     owner answer, a dated result, or Pulse's own inference (marked).
//   - An owner answer is copied in by code when it is saved
//     (rememberOwnerAnswer); Pulse adds results, lessons and bets with
//     record_pulse_goal_memory and consolidates when it grows.
//   - Pulse and goal-check turns read it first; soul.md wins on conflict.
//   - The owner sees and edits it in the Pulse tab.

const goalMemoryRelPath = "memory/goal.md"

// goalMemoryMaxEntries keeps the memory short: past it Pulse must
// consolidate before adding.
const goalMemoryMaxEntries = 60

// goalMemoryMaxBytes bounds an owner edit.
const goalMemoryMaxBytes = 32 * 1024

var goalMemorySections = []string{
	"Owner preferences and answers",
	"Decisions and outcomes",
	"Lessons",
	"Open bets",
	"Waiting on the owner",
}

// goalMemorySources maps the tool's source values to the marker on the line.
var goalMemorySources = map[string]string{
	"owner_answer":    "owner answer",
	"result":          "result",
	"pulse_inference": "Pulse inference",
}

const goalMemoryHeader = `# Goal memory

How this goal is managed: owner preferences and answers, decisions and their outcomes, lessons, open bets, and what
waits on the owner. One line per entry: date, [source] (owner answer, result, or Pulse inference), text.
soul/soul.md says what the goal is and wins on any conflict. The Goal Lead keeps this short; the owner may edit it.`

var goalMemoryMu sync.Mutex

type goalMemoryDoc struct {
	preamble []string
	sections []goalMemorySection
}

type goalMemorySection struct {
	title string
	lines []string
}

func parseGoalMemory(content string) goalMemoryDoc {
	doc := goalMemoryDoc{}
	var current *goalMemorySection
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			doc.sections = append(doc.sections, goalMemorySection{title: strings.TrimSpace(strings.TrimPrefix(line, "## "))})
			current = &doc.sections[len(doc.sections)-1]
			continue
		}
		if current == nil {
			doc.preamble = append(doc.preamble, line)
			continue
		}
		if strings.TrimSpace(line) != "" {
			current.lines = append(current.lines, line)
		}
	}
	return doc
}

func (doc goalMemoryDoc) render() string {
	var b strings.Builder
	preamble := strings.TrimSpace(strings.Join(doc.preamble, "\n"))
	if preamble == "" {
		preamble = goalMemoryHeader
	}
	b.WriteString(preamble)
	b.WriteString("\n")
	for _, section := range doc.sections {
		b.WriteString("\n## " + section.title + "\n")
		for _, line := range section.lines {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func (doc goalMemoryDoc) entryCount() int {
	n := 0
	for _, section := range doc.sections {
		for _, line := range section.lines {
			if strings.HasPrefix(strings.TrimSpace(line), "- ") {
				n++
			}
		}
	}
	return n
}

func (doc *goalMemoryDoc) add(section, line string) {
	for i := range doc.sections {
		if strings.EqualFold(doc.sections[i].title, section) {
			doc.sections[i].lines = append(doc.sections[i].lines, line)
			return
		}
	}
	doc.sections = append(doc.sections, goalMemorySection{title: section, lines: []string{line}})
}

func newGoalMemoryDoc() goalMemoryDoc {
	doc := goalMemoryDoc{}
	for _, title := range goalMemorySections {
		doc.sections = append(doc.sections, goalMemorySection{title: title})
	}
	return doc
}

// openGoalMemoryRoot opens the workflow folder as an os.Root, so the file can
// never resolve outside it.
func openGoalMemoryRoot(workspacePath string) (*os.Root, string, error) {
	normalized, err := normalizeReportHumanInputWorkspacePath(workspacePath)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(normalized, "Workflow/") {
		return nil, "", fmt.Errorf("goal memory belongs to a workflow (Workflow/<name>), not %s", normalized)
	}
	base, err := os.OpenRoot(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return nil, "", err
	}
	defer base.Close()
	root, err := base.OpenRoot(normalized)
	if err != nil {
		return nil, "", err
	}
	return root, normalized, nil
}

// readGoalMemory returns the memory file's content ("" when there is none).
func readGoalMemory(workspacePath string) (string, error) {
	root, _, err := openGoalMemoryRoot(workspacePath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	data, err := root.ReadFile(goalMemoryRelPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

func writeGoalMemoryLocked(root *os.Root, normalized, content string) error {
	if err := root.MkdirAll(path.Dir(goalMemoryRelPath), 0o755); err != nil {
		return err
	}
	if err := root.WriteFile(goalMemoryRelPath, []byte(content), 0o644); err != nil {
		return err
	}
	noteWorkspaceMutation(normalized+"/"+goalMemoryRelPath, false)
	return nil
}

func goalMemoryLine(date time.Time, source, text string) string {
	text = strings.Join(strings.Fields(text), " ")
	return fmt.Sprintf("- %s [%s] %s", date.UTC().Format("2006-01-02"), source, text)
}

// appendGoalMemory adds one line to a section. Past goalMemoryMaxEntries it
// refuses unless force (an owner answer is never dropped; Pulse
// consolidates on its next turn).
func appendGoalMemory(workspacePath, section, line string, force bool) error {
	goalMemoryMu.Lock()
	defer goalMemoryMu.Unlock()
	root, normalized, err := openGoalMemoryRoot(workspacePath)
	if err != nil {
		return err
	}
	defer root.Close()
	doc := newGoalMemoryDoc()
	if data, err := root.ReadFile(goalMemoryRelPath); err == nil {
		doc = parseGoalMemory(string(data))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !force && doc.entryCount() >= goalMemoryMaxEntries {
		return fmt.Errorf("goal memory has %d entries (limit %d): consolidate it first with record_pulse_goal_memory(action=\"consolidate\")", doc.entryCount(), goalMemoryMaxEntries)
	}
	doc.add(section, line)
	return writeGoalMemoryLocked(root, normalized, doc.render())
}

// saveGoalMemoryContent replaces the file (the owner's edit in the Pulse tab).
func saveGoalMemoryContent(workspacePath, content string) error {
	if len(content) > goalMemoryMaxBytes {
		return fmt.Errorf("goal memory is limited to %d KB; consolidate it", goalMemoryMaxBytes/1024)
	}
	goalMemoryMu.Lock()
	defer goalMemoryMu.Unlock()
	root, normalized, err := openGoalMemoryRoot(workspacePath)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeGoalMemoryLocked(root, normalized, strings.TrimRight(content, "\n")+"\n")
}

// rememberOwnerAnswer is the code distillation of an owner's answer: a straight
// copy of the chosen option (and note), marked as the owner's, and whether it
// followed or overruled the Goal Lead's recommendation.
func rememberOwnerAnswer(workspacePath string, input *ReportHumanInput, rec *PulseRecommendation) error {
	if input == nil || input.Source == "user_suggestion" || !strings.HasPrefix(input.WorkspacePath, "Workflow/") {
		return nil
	}
	choice := reportHumanInputOptionTitle(input.Options, input.SelectedOptionID)
	if choice == "" {
		choice = input.SelectedOptionID
	}
	text := fmt.Sprintf("Asked %q, the owner chose %q", strings.TrimSpace(input.Question), choice)
	if choice == "" {
		text = fmt.Sprintf("Asked %q, the owner answered %q", strings.TrimSpace(input.Question), strings.TrimSpace(input.Note))
	} else if note := strings.TrimSpace(input.Note); note != "" {
		text += fmt.Sprintf(" because %q", note)
	}
	if rec != nil {
		switch rec.OwnerResponse {
		case "accepted":
			text += " (the Goal Lead's recommendation)"
		case "changed":
			text += fmt.Sprintf(" (overruled the Goal Lead, who recommended %q)", rec.recommendedLabel(input.Options))
		}
	}
	text += "."
	if len(text) > 600 {
		text = text[:597] + "..."
	}
	return appendGoalMemory(workspacePath, goalMemorySections[0], goalMemoryLine(time.Now(), "owner answer", text), true)
}

// recordPulseGoalMemoryFromToolArgs backs record_pulse_goal_memory.
func recordPulseGoalMemoryFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := stringToolArg(args, "workspace_path")
	if workspacePath == "" {
		return "", fmt.Errorf("record_pulse_goal_memory requires workspace_path")
	}
	action := strings.ToLower(stringToolArg(args, "action"))
	switch action {
	case "", "add":
		entry, err := goalMemoryEntryFromArgs(args)
		if err != nil {
			return "", err
		}
		if err := appendGoalMemory(workspacePath, entry.section, entry.line, false); err != nil {
			return "", err
		}
		return "Goal memory entry added.", nil
	case "consolidate":
		raw, ok := args["entries"].([]interface{})
		if !ok || len(raw) == 0 {
			return "", fmt.Errorf("consolidate requires entries: the whole memory, one short line per preference, decision, lesson or bet")
		}
		if len(raw) > goalMemoryMaxEntries {
			return "", fmt.Errorf("consolidate to at most %d entries", goalMemoryMaxEntries)
		}
		doc := newGoalMemoryDoc()
		for i, item := range raw {
			fields, ok := item.(map[string]interface{})
			if !ok {
				return "", fmt.Errorf("entries[%d] must be an object", i)
			}
			entry, err := goalMemoryEntryFromArgs(fields)
			if err != nil {
				return "", fmt.Errorf("entries[%d]: %w", i, err)
			}
			doc.add(entry.section, entry.line)
		}
		goalMemoryMu.Lock()
		defer goalMemoryMu.Unlock()
		root, normalized, err := openGoalMemoryRoot(workspacePath)
		if err != nil {
			return "", err
		}
		defer root.Close()
		if data, err := root.ReadFile(goalMemoryRelPath); err == nil {
			doc.preamble = parseGoalMemory(string(data)).preamble
		}
		if err := writeGoalMemoryLocked(root, normalized, doc.render()); err != nil {
			return "", err
		}
		return fmt.Sprintf("Goal memory consolidated to %d entries.", len(raw)), nil
	}
	return "", fmt.Errorf("action must be add or consolidate")
}

type goalMemoryEntry struct{ section, line string }

func goalMemoryEntryFromArgs(args map[string]interface{}) (goalMemoryEntry, error) {
	section := ""
	want := stringToolArg(args, "section")
	for _, title := range goalMemorySections {
		if strings.EqualFold(title, want) {
			section = title
		}
	}
	if section == "" {
		return goalMemoryEntry{}, fmt.Errorf("section must be one of: %s", strings.Join(goalMemorySections, ", "))
	}
	source, ok := goalMemorySources[stringToolArg(args, "source")]
	if !ok {
		return goalMemoryEntry{}, fmt.Errorf("source must be owner_answer (only what the owner said), result (a dated result) or pulse_inference (your own reading, marked as such)")
	}
	text := stringToolArg(args, "text")
	if text == "" || strings.Contains(text, "\n") || len(text) > 300 {
		return goalMemoryEntry{}, fmt.Errorf("text is one plain line of at most 300 characters")
	}
	date := time.Now().UTC()
	if raw := stringToolArg(args, "date"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return goalMemoryEntry{}, fmt.Errorf("date must be YYYY-MM-DD")
		}
		date = parsed
	}
	return goalMemoryEntry{section: section, line: goalMemoryLine(date, source, text)}, nil
}
