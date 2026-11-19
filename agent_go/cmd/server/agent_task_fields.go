package server

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// #2965: a request is the task. Its title, outside reference and files travel
// on the message itself, so the Team views read the thread and nothing else.
const (
	agentTaskTitleMax = 120
	agentTaskRefMax   = 200
	agentTaskFilesMax = 10
	agentTaskFileMax  = 500
)

type agentTaskFields struct {
	Title string
	Ref   string
	Files []string
}

// agentTaskArgs copies only the task-field arguments, for forwarding to another server.
func agentTaskArgs(args map[string]interface{}) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"title", "ref", "files", "buttons"} {
		if v, ok := args[k]; ok {
			out[k] = v
		}
	}
	return out
}

// withAgentTaskSchema adds the task fields to a send tool's properties.
func withAgentTaskSchema(props map[string]interface{}) map[string]interface{} {
	str := func(d string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": d}
	}
	props["title"] = str("Ask or queue: the task in one short line (up to 120 characters). Defaults to the message's first line.")
	props["ref"] = str("What the task belongs to: a GitHub issue such as #2965, a ticket or PR link, or the message_id of the task this is a sub-task of. Defaults to the first #number in the message.")
	props["files"] = map[string]interface{}{"type": "array", "maxItems": agentTaskFilesMax, "items": map[string]interface{}{"type": "string"}, "description": "Optional web links the task is about, such as a ticket, PR or doc (up to 10); they show on the task. A local path is not shown to others: attach the file itself (one per message); everyone on this Team task sees it, so attach nothing private."}
	props["buttons"] = map[string]interface{}{"type": "array", "maxItems": 3, "items": map[string]interface{}{"type": "string", "maxLength": 24}, "description": "With target person:<name>: one to three short buttons the person clicks in the Team panel, e.g. Approve, Not now, Ask my agent."}
	return props
}

func agentTaskOneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func agentTaskFieldsFromArgs(args map[string]interface{}) (agentTaskFields, error) {
	f := agentTaskFields{Title: agentTaskOneLine(stringToolArg(args, "title")), Ref: agentTaskOneLine(stringToolArg(args, "ref"))}
	if len([]rune(f.Title)) > agentTaskTitleMax {
		return f, fmt.Errorf("title is longer than %d characters; keep it to one short line", agentTaskTitleMax)
	}
	if len([]rune(f.Ref)) > agentTaskRefMax {
		return f, fmt.Errorf("ref is longer than %d characters; use the issue, ticket or PR link", agentTaskRefMax)
	}
	for _, file := range stringSliceFromToolArg(args["files"]) {
		if file = agentTaskOneLine(file); file == "" {
			continue
		}
		if len([]rune(file)) > agentTaskFileMax {
			return f, fmt.Errorf("a files entry is longer than %d characters", agentTaskFileMax)
		}
		f.Files = append(f.Files, file)
	}
	if len(f.Files) > agentTaskFilesMax {
		return f, fmt.Errorf("files lists at most %d paths or links; attach one file with file_name", agentTaskFilesMax)
	}
	return f, nil
}

// agentTaskTitle is the given title, else the message's first sentence without a
// leading "agent-name:" and cut to 80 characters, so the Team views read as tasks.
func agentTaskTitle(f agentTaskFields, message string) string {
	if f.Title != "" {
		return agentTaskTitleWithoutRef(f.Title, agentTaskRef(f, message))
	}
	line := firstLineClip(message, agentTaskTitleMax)
	if name, rest, ok := strings.Cut(line, ": "); ok && name != "" && len(name) <= 40 && !strings.ContainsAny(name, " \t") && strings.TrimSpace(rest) != "" {
		line = strings.TrimSpace(rest)
	}
	if end := agentTaskSentenceEnd.FindStringIndex(line); end != nil && end[0] >= 12 {
		line = line[:end[0]+1]
	}
	return firstLineClip(agentTaskTitleWithoutRef(line, agentTaskRef(f, message)), agentTaskShortTitle)
}

// agentTaskTitleWithoutRef drops a leading issue number the task already shows as its ref
// ("#3018 Fix the tab" under #3018 reads "Fix the tab").
func agentTaskTitleWithoutRef(title, ref string) string {
	if ref == "" || !strings.HasPrefix(ref, "#") {
		return title
	}
	rest, ok := strings.CutPrefix(title, ref)
	if !ok || rest != "" && !strings.ContainsRune(" :-–—", []rune(rest)[0]) {
		return title
	}
	if rest = strings.TrimLeft(rest, " :-–—"); rest == "" {
		return title
	}
	return rest
}

const agentTaskShortTitle = 80

var (
	agentTaskSentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)
	agentTaskIssueRef    = regexp.MustCompile(`(^|[\s(])(#[0-9]{1,7})\b`)
	agentTaskProofLink   = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/(?:pull/[0-9]+|commit/[0-9a-f]{7,40})|\bPR ?#?[0-9]{1,7}\b`)
	agentTaskCommit      = regexp.MustCompile(`(?:^|[\s(])([0-9a-f]{10,40})(?:[\s).,;]|$)`)
)

// agentTaskRef is the given ref, else the first issue number (#1234) in the text.
// A ref that is another request's message ID (msg-...) makes this a sub-task of it.
func agentTaskRef(f agentTaskFields, message string) string {
	if f.Ref != "" {
		return f.Ref
	}
	if m := agentTaskIssueRef.FindStringSubmatch(message); m != nil {
		return m[2]
	}
	return ""
}

// agentTaskProof is the pull request or commit a finished task's reply names, if any.
func agentTaskProof(text string) string {
	if link := agentTaskProofLink.FindString(text); link != "" {
		return link
	}
	for _, m := range agentTaskCommit.FindAllStringSubmatch(text, -1) {
		if strings.ContainsAny(m[1], "abcdef") && strings.ContainsAny(m[1], "0123456789") {
			return m[1]
		}
	}
	return ""
}

// agentTaskMessageText puts the task fields in front of the text, so every
// delivery route (live chat, inbox read, Mac app, another server) shows them.
func agentTaskMessageText(f agentTaskFields, message string) string {
	var head []string
	if f.Title != "" && f.Title != firstLineClip(message, agentTaskTitleMax) {
		head = append(head, "Task: "+f.Title)
	}
	if f.Ref != "" {
		head = append(head, "Ref: "+f.Ref)
	}
	if len(f.Files) > 0 {
		head = append(head, "Files: "+strings.Join(f.Files, ", "))
	}
	text := message
	if len(head) == 0 {
		return text
	}
	return strings.Join(head, "\n") + "\n\n" + text
}

func (m agentMessage) taskFields() agentTaskFields {
	return agentTaskFields{Title: m.Title, Ref: m.Ref, Files: m.Files}
}

// agentMessageTaskItem adds the stored task fields to a read result.
func agentMessageTaskItem(item map[string]interface{}, m agentMessage) map[string]interface{} {
	if m.Title != "" {
		item["title"] = m.Title
	}
	if m.Ref != "" {
		item["ref"] = m.Ref
	}
	if len(m.Files) > 0 {
		item["files"] = m.Files
	}
	return item
}

// agentTaskOpenRequest is the newest request from the other side that a reply from side still answers.
func agentTaskOpenRequest(c agentConversation, side int, now time.Time) (agentMessage, bool) {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		request := c.Messages[i]
		if request.From == side || request.MessageType != "request" || request.Kind != "" {
			continue
		}
		switch state, _ := agentTaskState(c, request, now); state {
		case "done", "blocked", "needs_you":
			continue
		}
		return request, true
	}
	return agentMessage{}, false
}

// logAgentTask writes one line per request state change: ids, title, ref and status, never the text.
func logAgentTask(c agentConversation, request agentMessage, from, to string, reply agentMessage) {
	if from == to {
		return
	}
	log.Printf("[AGENT_TASK] inbox=%s request=%s reply=%s state=%s->%s status=%s title=%q ref=%q", c.ID, request.ID, reply.ID, firstNonEmptyTrimmed(from, "new"), to, reply.ReplyStatus, request.Title, request.Ref)
}
