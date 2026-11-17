package server

import (
	"fmt"
	"log"
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
	agentTaskWhyMax   = 500
)

type agentTaskFields struct {
	Title    string
	Ref      string
	Files    []string
	Decision bool
	Why      string
}

// agentTaskArgs copies only the task-field arguments, for forwarding to another server.
func agentTaskArgs(args map[string]interface{}) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"title", "ref", "files", "decision", "why"} {
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
	props["ref"] = str("Optional outside reference for the task: a GitHub issue such as #2965, a ticket or a PR link.")
	props["files"] = map[string]interface{}{"type": "array", "maxItems": agentTaskFilesMax, "items": map[string]interface{}{"type": "string"}, "description": "Optional paths or links the task is about (up to 10). To send a file's bytes, attach it instead."}
	props["decision"] = map[string]interface{}{"type": "boolean", "description": "A decision you settled, sent in the task's thread with why. It is kept with the task and never changes its state."}
	props["why"] = str("With decision=true: the reason, in one or two sentences.")
	return props
}

func agentTaskOneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func agentTaskFieldsFromArgs(args map[string]interface{}) (agentTaskFields, error) {
	f := agentTaskFields{Title: agentTaskOneLine(stringToolArg(args, "title")), Ref: agentTaskOneLine(stringToolArg(args, "ref")), Why: strings.TrimSpace(stringToolArg(args, "why"))}
	f.Decision, _ = args["decision"].(bool)
	if len([]rune(f.Title)) > agentTaskTitleMax {
		return f, fmt.Errorf("title is longer than %d characters; keep it to one short line", agentTaskTitleMax)
	}
	if len([]rune(f.Ref)) > agentTaskRefMax {
		return f, fmt.Errorf("ref is longer than %d characters; use the issue, ticket or PR link", agentTaskRefMax)
	}
	if len([]rune(f.Why)) > agentTaskWhyMax {
		return f, fmt.Errorf("why is longer than %d characters", agentTaskWhyMax)
	}
	if f.Why != "" && !f.Decision {
		return f, fmt.Errorf("why goes with a decision; set decision=true")
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

// agentTaskTitle is the given title, else the message's first line cut to 120 characters.
func agentTaskTitle(f agentTaskFields, message string) string {
	if f.Title != "" {
		return f.Title
	}
	return firstLineClip(message, agentTaskTitleMax)
}

// agentTaskMessageText puts the task fields in front of the text, so every
// delivery route (live chat, inbox read, Mac app, another server) shows them.
func agentTaskMessageText(f agentTaskFields, message string) string {
	var head []string
	if f.Decision {
		head = append(head, "Decision.")
	}
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
	if f.Why != "" {
		text += "\n\nWhy: " + f.Why
	}
	if len(head) == 0 {
		return text
	}
	return strings.Join(head, "\n") + "\n\n" + text
}

func (m agentMessage) taskFields() agentTaskFields {
	return agentTaskFields{Title: m.Title, Ref: m.Ref, Files: m.Files, Decision: m.Decision, Why: m.Why}
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
	if m.Decision {
		item["decision"] = true
		if m.Why != "" {
			item["why"] = m.Why
		}
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
