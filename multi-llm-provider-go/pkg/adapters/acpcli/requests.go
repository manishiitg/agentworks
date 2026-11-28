package acpcli

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
)

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionRequest struct {
	SessionID string `json:"sessionId"`
	ToolCall  struct {
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		Kind       string          `json:"kind"`
		RawInput   json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []permissionOption `json:"options"`
}

func (p permissionRequest) option(kinds ...string) string {
	for _, kind := range kinds {
		for _, o := range p.Options {
			if o.Kind == kind && strings.TrimSpace(o.OptionID) != "" {
				return o.OptionID
			}
		}
	}
	return ""
}

func selected(optionID string) map[string]any {
	if optionID == "" {
		return cancelledOutcome()
	}
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
}

func cancelledOutcome() map[string]any {
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

// answer replies to a request the agent makes of the client. Questions for
// the person run in the background (requests) so the turn keeps reading
// events; ctx carries the turn's question card scope and its cancellation.
func (s *session) answer(ctx context.Context, msg message, requests *nativeinput.Requests) {
	switch msg.Method {
	case "session/request_permission":
		var req permissionRequest
		_ = json.Unmarshal(msg.Params, &req)
		s.mu.Lock()
		current := s.sessionID
		s.mu.Unlock()
		if req.SessionID != current {
			_ = s.rpc.reply(msg.ID, cancelledOutcome())
			return
		}
		if s.questionPrefix != "" && strings.HasPrefix(req.ToolCall.ToolCallID, s.questionPrefix) {
			requests.Start(ctx, string(msg.ID), func(qctx context.Context) {
				_ = s.rpc.reply(msg.ID, s.askQuestion(qctx, req))
			})
			return
		}
		if s.autoApprove {
			// The chat's tools mode already allows the agent's tools, the
			// same rule the other coding agents launch with.
			choice := req.option("allow_once", "allow_always")
			log.Printf("[NATIVE_APPROVAL] provider=%s agent=%s kind=permission tool_kind=%s action=auto_allow", s.agent.providerID(), s.agent.ID, req.ToolCall.Kind)
			_ = s.rpc.reply(msg.ID, selected(choice))
			return
		}
		if s.preApproved != nil && s.preApproved(req.ToolCall.Title, req.ToolCall.RawInput) {
			// The chat's own AgentWorks tools: already authorised for this chat, so the agent's reviewer
			// sending them does not make the person approve each one.
			log.Printf("[NATIVE_APPROVAL] provider=%s agent=%s kind=permission tool_kind=%s action=pre_approved", s.agent.providerID(), s.agent.ID, req.ToolCall.Kind)
			_ = s.rpc.reply(msg.ID, selected(req.option("allow_once", "allow_always")))
			return
		}
		requests.Start(ctx, string(msg.ID), func(qctx context.Context) {
			_ = s.rpc.reply(msg.ID, s.askPermission(qctx, req))
		})
	case "elicitation/create":
		requests.Start(ctx, string(msg.ID), func(qctx context.Context) {
			_ = s.rpc.reply(msg.ID, s.askForm(qctx, msg.Params))
		})
	default:
		log.Printf("[ACP_CLI] session=%s agent=%s method=%s action=refused reason=unsupported_client_method", s.rpc.owner, s.agent.ID, msg.Method)
		_ = s.rpc.replyError(msg.ID, -32601, "method not supported by this client")
	}
}

// askQuestion asks the person the agent's own question (agy sends them as permission requests whose options
// are the answers) and replies with the option they chose. Unanswered or cancelled questions answer nothing.
func (s *session) askQuestion(ctx context.Context, req permissionRequest) map[string]any {
	question := nativeinput.Question{ID: req.ToolCall.ToolCallID, Header: "Question", Question: strings.TrimSpace(req.ToolCall.Title)}
	if question.Question == "" {
		question.Question = "Choose an option."
	}
	byLabel := map[string]string{}
	for _, o := range req.Options {
		id := strings.TrimSpace(o.OptionID)
		if id == "" {
			continue
		}
		label := strings.TrimSpace(o.Name)
		if label == "" {
			label = id
		}
		if _, taken := byLabel[label]; taken {
			label += " (" + id + ")"
		}
		byLabel[label] = id
		question.Options = append(question.Options, nativeinput.Option{Label: label})
	}
	if len(question.Options) == 0 {
		return cancelledOutcome()
	}
	answers, err := nativeinput.Ask(ctx, []nativeinput.Question{question})
	if err != nil || len(answers) != 1 || len(answers[0].SelectedLabels) != 1 {
		log.Printf("[NATIVE_APPROVAL] provider=%s agent=%s kind=question action=unanswered", s.agent.providerID(), s.agent.ID)
		return cancelledOutcome()
	}
	log.Printf("[NATIVE_APPROVAL] provider=%s agent=%s kind=question action=answered", s.agent.providerID(), s.agent.ID)
	return selected(byLabel[answers[0].SelectedLabels[0]])
}

// askPermission shows the agent's permission request as the chat's approval
// card. Unanswered, dismissed or cancelled requests never allow.
func (s *session) askPermission(ctx context.Context, req permissionRequest) map[string]any {
	once, always := req.option("allow_once"), req.option("allow_always")
	if once == "" && always == "" {
		return cancelledOutcome()
	}
	var b strings.Builder
	b.WriteString("Allow " + s.agent.Label + " to run this?\n")
	if title := strings.TrimSpace(req.ToolCall.Title); title != "" {
		b.WriteString("\n" + title)
	}
	var input struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		File    string `json:"file_path"`
	}
	if json.Unmarshal(req.ToolCall.RawInput, &input) == nil {
		for _, detail := range []string{input.Command, input.Path, input.File} {
			if detail = strings.TrimSpace(detail); detail != "" && !strings.Contains(b.String(), detail) {
				b.WriteString("\n" + detail)
			}
		}
	}
	choice, err := nativeinput.ApproveCommandChoice(ctx, b.String(), once != "" && always != "")
	action, reply := "decline", selected(req.option("reject_once", "reject_always"))
	switch {
	case err != nil:
		action, reply = "cancel", cancelledOutcome()
	case choice == nativeinput.ApprovalAlways:
		action, reply = "allow_always", selected(req.option("allow_always", "allow_once"))
	case choice == nativeinput.ApprovalOnce:
		action, reply = "allow_once", selected(req.option("allow_once", "allow_always"))
	}
	log.Printf("[NATIVE_APPROVAL] provider=%s agent=%s kind=permission tool_kind=%s action=%s", s.agent.providerID(), s.agent.ID, req.ToolCall.Kind, action)
	return reply
}

// askForm shows an MCP-style form or link request as the chat's question card.
func (s *session) askForm(ctx context.Context, raw json.RawMessage) map[string]any {
	var req struct {
		Mode            string          `json:"mode"`
		Message         string          `json:"message"`
		URL             string          `json:"url"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	_ = json.Unmarshal(raw, &req)
	var result nativeinput.FormResult
	switch req.Mode {
	case "url":
		result = nativeinput.AskURL(ctx, s.agent.providerID(), s.agent.Label, req.Message, req.URL)
	case "form", "":
		if s.agent.QuestionForm {
			if form, ok := parseQuestionForm(req.RequestedSchema); ok {
				result = form.ask(ctx, s.agent.ID)
				break
			}
		}
		result = nativeinput.AskForm(ctx, s.agent.providerID(), s.agent.Label, req.Message, req.RequestedSchema)
	default:
		log.Printf("[NATIVE_FORM] provider=%s agent=%s mode=%s action=decline reason=unsupported_mode", s.agent.providerID(), s.agent.ID, req.Mode)
		result = nativeinput.FormResult{Action: "decline"}
	}
	out := map[string]any{"action": result.Action}
	if result.Action == "accept" {
		content := result.Content
		if content == nil {
			content = map[string]any{}
		}
		out["content"] = content
	}
	return out
}
