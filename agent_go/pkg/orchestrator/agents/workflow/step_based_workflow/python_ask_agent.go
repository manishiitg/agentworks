package step_based_workflow

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/workspace/runlog"
)

type PythonAskEvidence struct {
	ID             string `json:"id"`
	Crew           string `json:"crew"`
	Status         string `json:"status"`
	InboxID        string `json:"inbox_id,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	ReplyMessageID string `json:"reply_message_id,omitempty"`
	Detail         string `json:"detail,omitempty"`
	LogFile        string `json:"log_file,omitempty"`
	AnswerFile     string `json:"answer_file,omitempty"`
}

func (hcpo *StepBasedWorkflowOrchestrator) runPythonAskAgent(ctx context.Context, parent PlanStepInterface, arguments map[string]interface{}, id string, log *runlog.Log, persist func(pythonHelperReply) error) (reply pythonHelperReply) {
	receipt := map[string]interface{}{"status": "refused"}
	reply.Value = receipt
	crew, _ := arguments["crew"].(string)

	message, _ := arguments["message"].(string)
	wait := float64(25)
	if value, ok := arguments["wait_seconds"]; ok {
		var valid bool
		wait, valid = value.(float64)
		if !valid || math.IsNaN(wait) || math.IsInf(wait, 0) || wait < 0 {
			reply.Error = "wait_seconds must be a finite non-negative number."
			return reply
		}
	}
	wait = min(wait, 25)
	if strings.TrimSpace(crew) == "" || strings.TrimSpace(message) == "" {
		reply.Error = "An ask needs a Crew and a non-empty message."
		return reply
	}
	_, executors, err := hcpo.pythonFunctionTools(ctx, parent.GetID())
	if err != nil {
		reply.Error = err.Error()
		return reply
	}
	send, sendOK := executors["ask_crew"].(func(context.Context, map[string]interface{}) (string, error))
	read, readOK := executors["read_agent_messages"].(func(context.Context, map[string]interface{}) (string, error))
	if !sendOK || !readOK {
		reply.Error = "Crew asks are unavailable to this automation. Open its Crew chat to check access."
		return reply
	}
	raw, err := send(ctx, map[string]interface{}{"crew": crew, "message": message, "chat_id": arguments["chat_id"], "submission_id": id})
	if err != nil {
		reply.Error = redaction.Text(err.Error())
		return reply
	}
	if json.Unmarshal([]byte(raw), &receipt) != nil || receipt == nil {
		receipt = map[string]interface{}{}
		receipt["status"] = "unknown"
		reply.Value = receipt
		reply.Error = "The message may have been sent but its receipt is unreadable. Inspect this run; do not resend."
		return reply
	}
	inbox, _ := receipt["inbox_id"].(string)
	sentID, _ := receipt["message_id"].(string)
	if inbox == "" || sentID == "" {
		receipt["status"] = "unknown"
		reply.Value = receipt
		reply.Error = "The message may have been sent but its receipt is incomplete. Inspect this run; do not resend."
		return reply
	}
	receipt["status"], receipt["note"] = "pending", "Sent; no reply yet, do not resend."
	reply.OK, reply.Value = true, receipt
	log.Note("ask accepted inbox=" + inbox + " message=" + sentID)
	// The accepted send survives a timeout/disconnect before any reply is read.
	if err := persist(reply); err != nil {
		reply.OK = false
		reply.Error = "The message was sent but its receipt could not be saved. Inspect this run; do not resend."
		return reply
	}
	raw, err = read(ctx, map[string]interface{}{"inbox_id": inbox, "after": receipt["sequence"], "wait_seconds": wait})
	if err != nil {
		if ctx.Err() != nil {
			return reply
		}
		reply.OK = false
		reply.Error = "The message was sent but replies are unavailable: " + redaction.Text(err.Error()) + " Do not resend."
		return reply
	}
	var result struct {
		Messages []struct {
			InboxID   string `json:"inbox_id"`
			MessageID string `json:"message_id"`
			Message   string `json:"message"`
		} `json:"messages"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil {
		reply.OK = false
		reply.Error = "The message was sent but its reply receipt is unreadable. Inspect this run; do not resend."
		return reply
	}
	for _, item := range result.Messages {
		if item.InboxID != inbox || item.MessageID == "" || item.MessageID == sentID || strings.TrimSpace(item.Message) == "" {
			continue
		}
		answer := redaction.Text(item.Message)
		file := filepath.ToSlash(filepath.Join("runs", hcpo.selectedRunFolder, "helpers", "answers", id+".txt"))
		if err := hcpo.writePythonHelperState(context.WithoutCancel(ctx), file, answer); err != nil {
			reply.OK = false
			reply.Error = "The reply arrived but could not be saved. Inspect this inbox; do not resend."
			return reply
		}
		reply.Text, reply.AnswerFile = scriptOutputPreview(answer, log.Path()), workflowAbsPath(hcpo.GetWorkspacePath(), file)
		receipt["status"], receipt["note"], receipt["reply_message_id"] = "replied", "A matching reply was received.", item.MessageID
		log.Note("ask reply inbox=" + inbox + " message=" + item.MessageID)
		_, _ = log.Stream("reply").Write([]byte(answer + "\n"))
		break
	}
	return reply
}

func (hcpo *StepBasedWorkflowOrchestrator) savePythonAskEvidence(ctx context.Context, id string, arguments map[string]interface{}, reply pythonHelperReply) error {
	receipt, _ := reply.Value.(map[string]interface{})
	crew, _ := arguments["crew"].(string)
	evidence := PythonAskEvidence{ID: id, Crew: boundedRunEvidenceText(crew), LogFile: reply.LogFile, AnswerFile: reply.AnswerFile}
	evidence.Status, _ = receipt["status"].(string)
	evidence.InboxID, _ = receipt["inbox_id"].(string)
	evidence.MessageID, _ = receipt["message_id"].(string)
	evidence.ReplyMessageID, _ = receipt["reply_message_id"].(string)
	detail := reply.Error
	if detail == "" {
		detail, _ = receipt["note"].(string)
	}
	evidence.Detail = boundedRunEvidenceText(detail)
	return hcpo.upsertRunMetadata(context.WithoutCancel(ctx), hcpo.selectedRunFolder, func(meta map[string]interface{}) {
		items, _ := meta["python_asks"].(map[string]interface{})
		if items == nil {
			items = map[string]interface{}{}
		}
		items[id] = evidence
		meta["python_asks"] = items
	})
}
