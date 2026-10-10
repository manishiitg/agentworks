package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// ask_pulse: a chat or step of the workflow talks to its Pulse. The message
// goes into the Pulse conversation as plain text from the asking chat, and
// Pulse's reply comes back as the answer: two agents talking, nothing more
// (owner, 2026-10-08). It uses the function-call mechanism of ask_project_chat
// (call id, saved record, chain guard) and an hourly cap per workflow. The
// caller and the workflow come from the trusted session, never from arguments.

const (
	goalLeadAskCreatedBy     = "platform (goal lead)"
	goalLeadAsksPerHour      = 20
	goalLeadAskMessageRunes  = 8000
	goalLeadAskMaxWaitSecond = 120
)

func goalLeadAskFunction() crewFunction {
	return crewFunction{
		Name:        crewFunctionAskName,
		Description: "Talk to the workflow's Pulse.",
		InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"message"}, "properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string"},
		}},
		ResultSchema: map[string]interface{}{"type": "object", "required": []interface{}{"answer"}, "properties": map[string]interface{}{
			"answer": map[string]interface{}{"type": "string"},
		}},
		CreatedBy: goalLeadAskCreatedBy,
	}
}

func isGoalLeadAsk(target triggerTarget, fn crewFunction) bool {
	return target.Kind == triggerCallerWorkflow && target.Chat != nil && fn.CreatedBy == goalLeadAskCreatedBy
}

var goalLeadAsks = struct {
	sync.Mutex
	byWorkflow map[string][]time.Time
}{byWorkflow: map[string][]time.Time{}}

// admitGoalLeadAsk caps asks to one workflow's Pulse per hour: chats and
// steps can otherwise ask it in a loop.
func admitGoalLeadAsk(workspacePath string, now time.Time) error {
	goalLeadAsks.Lock()
	defer goalLeadAsks.Unlock()
	kept := goalLeadAsks.byWorkflow[workspacePath][:0]
	for _, at := range goalLeadAsks.byWorkflow[workspacePath] {
		if now.Sub(at) < time.Hour {
			kept = append(kept, at)
		}
	}
	if len(kept) >= goalLeadAsksPerHour {
		goalLeadAsks.byWorkflow[workspacePath] = kept
		return fmt.Errorf("refused: this workflow's Pulse was asked %d times in the last hour; continue with what you have and leave the question in your result", goalLeadAsksPerHour)
	}
	goalLeadAsks.byWorkflow[workspacePath] = append(kept, now)
	return nil
}

// askGoalLead sends message from callerLabel (a chat or step of the workflow
// at workspacePath, callerSession its session) to the Pulse and waits up to
// wait for its reply.
func (api *StreamingAPI) askGoalLead(ctx context.Context, userID, workspacePath, callerSession, callerLabel, message string, wait time.Duration, submissionID string) (map[string]interface{}, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}
	if len([]rune(message)) > goalLeadAskMessageRunes {
		return nil, fmt.Errorf("message is longer than %d characters; point to files in the workflow for detail", goalLeadAskMessageRunes)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest == nil {
		return nil, fmt.Errorf("cannot read this workflow")
	}
	conv, err := ensureGoalLeadConversation(ctx, workspacePath, firstNonEmptyTrimmed(manifest.ID, workspacePath), time.Now().UTC(), false, nil)
	if err != nil {
		return nil, err
	}
	label := firstNonEmptyTrimmed(manifest.Label, workspacePath)
	// The caller is one chat (session) of the workflow, as a sibling chat is in
	// ask_project_chat: the workflow and its Pulse are both participants
	// of the call chain, so the chain guard does not read the ask as a loop.
	callerChat := &codeChat{Key: "session:" + firstNonEmptyTrimmed(callerSession, "unknown"), Name: firstNonEmptyTrimmed(callerLabel, label+" chat"), SessionID: callerSession}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Label: callerChat.Name, Path: workspacePath, Chat: callerChat}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: workspacePath, Label: label + " Pulse", Manifest: manifest,
		Chat: &codeChat{Key: "goal-lead", ID: "goal-lead", Name: label + " Pulse", SessionID: conv.SessionID}}
	if err := admitGoalLeadAsk(workspacePath, time.Now()); err != nil {
		return nil, err
	}
	call, err := api.startCrewFunctionCall(ctx, userID, caller, target, goalLeadAskFunction(), map[string]interface{}{"message": message}, triggerTargetDefaultTimeout, submissionID)
	if err != nil {
		return nil, err
	}
	if wait > 0 {
		select {
		case <-call.done:
		case <-time.After(wait):
		case <-ctx.Done():
		}
	}
	out := call.snapshot()
	addFunctionCallPending(out, call)
	if out["status"] != "completed" && out["status"] != "failed" {
		out["next"] = "Pulse is still replying. Read it later with get_function_call(call_id), or continue without it."
		// A workflow step owns its execution, not a retained server chat. Do
		// not redirect its reply to an unrelated Builder or parent session.
		if _, step := stepworkflow.LookupWorkshopToolSession(callerSession); !step && callerSession != "" && !isScheduledSession(callerSession) {
			id, watchErr := api.startCrewFunctionWatch(QueryRequest{SelectedFolder: workspacePath, PresetQueryID: manifest.ID}, callerSession, userID, call, triggerTargetDefaultTimeout)
			if watchErr != nil {
				out["auto_notify_error"] = watchErr.Error()
			} else {
				out["execution_id"], out["auto_notify"] = id, true
				out["next"] = "Pulse is still replying. Its result will be delivered automatically to this chat."
			}
		}
	}
	return out, nil
}

// runGoalLeadAsk runs an ask as a turn in the Pulse conversation and
// settles the call with its final reply.
func (api *StreamingAPI) runGoalLeadAsk(call *crewFunctionCall, target triggerTarget, caller triggerLinkCaller, args map[string]interface{}, timeout time.Duration) {
	message, _ := args["message"].(string)
	message = strings.TrimSpace(message)
	hardCap := crewFunctionHardCap(timeout)
	ctx, cancel := context.WithTimeout(context.Background(), hardCap)
	defer cancel()
	call.mu.Lock()
	call.Status = "running"
	if target.Chat != nil {
		call.RunID, call.RunIDs = target.Chat.SessionID, []string{target.Chat.SessionID}
	}
	call.mu.Unlock()
	call.persist()
	turnDone := make(chan struct{})
	if target.Chat != nil {
		go api.watchAskActivity(call, target.Chat.SessionID, fmt.Sprintf("the Pulse of %q", target.Label), timeout, turnDone)
	}
	reply, _, err := api.runGoalLeadTurn(ctx, target.Path, goalLeadTurn{Kind: goalLeadTurnAsk, From: caller.Label, Body: message})
	close(turnDone)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			call.settle("failed", nil, fmt.Sprintf("the Pulse was still not done after %s", hardCap))
			return
		}
		call.settle("failed", nil, fmt.Sprintf("the Pulse did not answer: %v", err))
		return
	}
	if strings.TrimSpace(reply) == "" {
		call.settle("failed", nil, "the Pulse finished without a reply")
		return
	}
	call.settle("completed", map[string]interface{}{"answer": truncateTriggerTargetResult(reply)}, "")
}

// Ask tool names: ask_pulse; ask_goal_lead is its old name, kept working for
// one release (owner, 2026-10-07: users see "Pulse", not "Goal Lead").
const (
	goalLeadAskToolName      = "ask_pulse"
	goalLeadAskToolAliasName = "ask_goal_lead"
)

type goalLeadAskTool struct {
	tool    llmtypes.Tool
	execute func(context.Context, map[string]interface{}) (string, error)
}

// createGoalLeadAskTools are ask_pulse and its alias ask_goal_lead for the
// workflow's chats and steps.
func createGoalLeadAskTools() []goalLeadAskTool {
	out := []goalLeadAskTool{}
	for _, name := range []string{goalLeadAskToolName, goalLeadAskToolAliasName} {
		tool, execute := createGoalLeadAskTool(name)
		out = append(out, goalLeadAskTool{tool: tool, execute: execute})
	}
	return out
}

// createGoalLeadAskTool is one name of the ask tool.
func createGoalLeadAskTool(name string) (llmtypes.Tool, func(context.Context, map[string]interface{}) (string, error)) {
	description := "Talk to this workflow's Pulse, the agent that owns the workflow's goal. Your message goes into Pulse's conversation as a message from this chat, and Pulse's reply comes back. " +
		"Use it for anything about the goal: how it is doing, what to prioritise, why Pulse did something, or to pass on the owner's direction. Pulse is the goal expert: act on what it says unless it says the owner must decide. " +
		"Waits up to wait_seconds (default 60) for the reply. A later reply is automatically delivered to this chat; workflow steps and scheduled execution sessions retain their own call_id to read with get_function_call. Capped per workflow per hour. Only for workflows with a goal."
	if name == goalLeadAskToolAliasName {
		description = "Old name of ask_pulse, kept for one release; use ask_pulse. " + description
	}
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        name,
		Description: description,
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message":       map[string]interface{}{"type": "string", "minLength": 1, "description": "Your message to Pulse, as you would write it to a colleague."},
				"wait_seconds":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": goalLeadAskMaxWaitSecond},
				"submission_id": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this ask; reuse it after an uncertain retry to get the original call."},
			},
			"required": []string{"message"},
		}),
	}}
	execute := func(ctx context.Context, args map[string]interface{}) (string, error) {
		api := pulsePlatformAPI
		if api == nil {
			return "", fmt.Errorf("%s is unavailable in this process", name)
		}
		sessionID := strings.TrimSpace(executor.SessionIDFromContext(ctx))
		if sessionID == "" {
			sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		}
		if isGoalLeadSessionID(sessionID) {
			return "", fmt.Errorf("the Pulse cannot ask itself")
		}
		// Pulse is talked to from the app and MCP, not from Slack or other bot
		// conversations (owner, 2026-10-08).
		if api.sessionIsBotConversation(sessionID) {
			return "", fmt.Errorf("Pulse is not available from Slack or other bot conversations; tell the person to talk to Pulse from the workflow's Builder chat in AgentWorks (#pulse) or an MCP client")
		}
		workspacePath, claims, err := api.pulseToolScope(ctx, "", false)
		if err != nil {
			return "", err
		}
		if !workflowHasGoal(ctx, workspacePath) {
			return "", fmt.Errorf("this workflow's Pulse is off or has no goal yet (Pulse on and soul/soul.md)")
		}
		wait := 60 * time.Second
		if raw, ok := args["wait_seconds"]; ok {
			seconds := intToolArg(map[string]interface{}{"v": raw}, "v")
			if seconds < 0 || seconds > goalLeadAskMaxWaitSecond {
				return "", fmt.Errorf("wait_seconds must be 0-%d", goalLeadAskMaxWaitSecond)
			}
			wait = time.Duration(seconds) * time.Second
		}
		callerLabel := api.goalLeadAskCaller(ctx, workspacePath, sessionID, claims)
		out, err := api.askGoalLead(ctx, claims.UserID, workspacePath, sessionID, callerLabel, stringToolArg(args, "message"), wait, stringToolArg(args, "submission_id"))
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	}
	return tool, execute
}

// goalLeadAskCaller names who is talking to Pulse, as the message's sender:
// the person in their Builder chat, a Run chat, a step or a scheduled run.
func (api *StreamingAPI) goalLeadAskCaller(ctx context.Context, workspacePath, sessionID string, claims *UserClaims) string {
	if _, background := stepworkflow.LookupWorkshopToolSession(sessionID); background {
		return "a step of this workflow"
	}
	if isScheduledSession(sessionID) {
		return "a scheduled run of this workflow"
	}
	info, ok := api.getActiveSession(sessionID)
	if !ok || info == nil || info.BotPlatform != "" || strings.HasPrefix(info.TurnProvider, "bot_") ||
		isScheduledSessionIdentity(sessionID, info.TriggeredBy) {
		return "a chat of this workflow"
	}
	if normalizeChatHistoryWorkshopMode(info.WorkshopMode) == "run" || claims == nil {
		return "a Run chat of this workflow"
	}
	if level, _ := workflowAccessForWorkspacePath(ctx, claims, workspacePath); level != WorkflowAccessOwner && level != WorkflowAccessWrite {
		return "a Run chat of this workflow"
	}
	return "the Builder chat (" + firstNonEmptyTrimmed(claims.Username, claims.Email, "the owner") + ")"
}

// createGoalLeadChatKindTools are the phase 4 Pulse writes: focus areas and
// QA requests (executors in createPulseWorklistTools).
func createGoalLeadChatKindTools() []llmtypes.Tool {
	return []llmtypes.Tool{
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_focus_area",
			Description: "The Pulse's focus areas (what matters now, between soul.md and goal memory). action=propose: a new focus from evidence or the owner's words, with text, end_date (YYYY-MM-DD, at most 90 days), check (how it is judged, e.g. \"drafts waiting 3 -> 0\") and why; at most three open at once; it waits for the owner's one-click confirm and is not active before that. action=track: daily, progress moving, stuck (note = the one clear ask) or done. action=close: status done, expired (say why; propose extend, change or drop) or dropped, with a one-line lesson that also goes to goal memory. Focus areas change what you attend to, never what you may do.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string"},
					"action":         map[string]interface{}{"type": "string", "enum": []string{"propose", "track", "close"}},
					"focus_id":       map[string]interface{}{"type": "string", "description": "track/close: the focus area's id (FA-...)."},
					"text":           map[string]interface{}{"type": "string", "description": "propose: the focus in plain words."},
					"end_date":       map[string]interface{}{"type": "string", "description": "propose: YYYY-MM-DD."},
					"check":          map[string]interface{}{"type": "string", "description": "propose: how this focus is judged."},
					"why":            map[string]interface{}{"type": "string", "description": "propose: the evidence or the owner's words."},
					"progress":       map[string]interface{}{"type": "string", "enum": []string{"moving", "stuck", "done"}},
					"note":           map[string]interface{}{"type": "string", "description": "track: one plain line; for stuck, the one clear ask."},
					"status":         map[string]interface{}{"type": "string", "enum": []string{"done", "expired", "dropped"}},
					"lesson":         map[string]interface{}{"type": "string", "description": "close: one line on what it taught."},
				},
				"required": []string{"workspace_path", "action"},
			}),
		}},
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_qa_request",
			Description: "Ask for QA / technical review as a separate run (the Pulse's sub-agent): a Pulse fix run (Technical Review+Fix in its own session) starts when the workflow is free and its short result comes back to the Pulse conversation (goal_lead.qa_results). Use it for a step that fails, outputs that look wrong, or a fix to verify; do not do long repair work in the Pulse conversation. One open request at a time, three a day.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string"},
					"what":           map[string]interface{}{"type": "string", "minLength": 1, "description": "What to check or fix, with the evidence (run, step, symptom), at most 600 characters."},
				},
				"required": []string{"workspace_path", "what"},
			}),
		}},
	}
}

// sessionIsBotConversation reports a Slack (or other bot) conversation, or a
// chat started from one.
func (api *StreamingAPI) sessionIsBotConversation(sessionID string) bool {
	if _, bot := api.botExecutionForSession(sessionID); bot {
		return true
	}
	info, ok := api.getActiveSession(sessionID)
	return ok && info != nil && (info.BotPlatform != "" || strings.HasPrefix(info.TurnProvider, "bot_"))
}
