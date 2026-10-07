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

// ask_goal_lead (PLAT-697 phase 4): a workflow's chats and steps ask its Goal
// Lead, by the same function-call mechanism as ask_project_chat
// (startCrewFunctionCall with a chat target): the call has an id and a saved
// record, runs as a turn in the Goal Lead conversation (queued behind its
// current turn), and the Goal Lead's final reply is the answer. The answer is
// a recommendation: the Goal Lead does not decide for the owner. The caller
// and the workflow come from the trusted session (pulseToolScope), never from
// arguments. Asks are capped per workflow like asks between a Code's chats.

const (
	goalLeadAskCreatedBy     = "platform (goal lead)"
	goalLeadAsksPerHour      = 20
	goalLeadAskMessageRunes  = 8000
	goalLeadAskMaxWaitSecond = 120
)

func goalLeadAskFunction() crewFunction {
	return crewFunction{
		Name:        crewFunctionAskName,
		Description: "Ask the workflow's Goal Lead for a recommendation on goal work.",
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

// admitGoalLeadAsk caps asks to one workflow's Goal Lead per hour: chats and
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
		return fmt.Errorf("refused: this workflow's Goal Lead was asked %d times in the last hour; continue with what you have and leave the question in your result", goalLeadAsksPerHour)
	}
	goalLeadAsks.byWorkflow[workspacePath] = append(kept, now)
	return nil
}

// askGoalLead starts an ask_goal_lead call from callerLabel (a chat or step of
// the workflow at workspacePath, callerSession its session) and waits up to
// wait for the answer.
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
	// ask_project_chat: the workflow and its Goal Lead are both participants
	// of the call chain, so the chain guard does not read the ask as a loop.
	callerChat := &codeChat{Key: "session:" + firstNonEmptyTrimmed(callerSession, "unknown"), Name: firstNonEmptyTrimmed(callerLabel, label+" chat")}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Label: callerChat.Name, Path: workspacePath, Chat: callerChat}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: workspacePath, Label: label + " Goal Lead", Manifest: manifest,
		Chat: &codeChat{Key: "goal-lead", ID: "goal-lead", Name: label + " Goal Lead", SessionID: conv.SessionID}}
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
	if !call.settled() {
		out["next"] = "The Goal Lead is still answering. Call ask_goal_lead again later with the same message and submission_id to read its answer (get_function_call(call_id) where you have it), or continue without it."
	}
	return out, nil
}

// runGoalLeadAsk runs an ask as a turn in the Goal Lead conversation and
// settles the call with its final reply.
func (api *StreamingAPI) runGoalLeadAsk(call *crewFunctionCall, target triggerTarget, caller triggerLinkCaller, message string, timeout time.Duration) {
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
		go api.watchAskActivity(call, target.Chat.SessionID, fmt.Sprintf("the Goal Lead of %q", target.Label), timeout, turnDone)
	}
	reply, _, err := api.runGoalLeadTurn(ctx, target.Path, goalLeadTurn{Kind: goalLeadTurnAsk, From: caller.Label, Body: message, CallID: call.ID})
	close(turnDone)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			call.settle("failed", nil, fmt.Sprintf("the Goal Lead was still not done after %s", hardCap))
			return
		}
		call.settle("failed", nil, fmt.Sprintf("the Goal Lead did not answer: %v", err))
		return
	}
	if strings.TrimSpace(reply) == "" {
		call.settle("failed", nil, "the Goal Lead finished without a reply")
		return
	}
	call.settle("completed", map[string]interface{}{"answer": truncateTriggerTargetResult(reply)}, "")
}

// createGoalLeadAskTool is ask_goal_lead for the workflow's chats and steps.
func createGoalLeadAskTool() (llmtypes.Tool, func(context.Context, map[string]interface{}) (string, error)) {
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name: "ask_goal_lead",
		Description: "Ask this workflow's Goal Lead (the persistent owner of the workflow's goal) for a recommendation on goal work: which option serves the goal, what to prioritise, what the owner already said (goal memory). " +
			"It runs as a turn in the Goal Lead's own conversation and answers with a recommendation, never a decision for the owner. Make the message self-contained. " +
			"Waits up to wait_seconds (default 60) for the answer; otherwise returns a call_id to read later with get_function_call. Capped per workflow per hour. Only for workflows with a goal (soul.md and a primary goal metric).",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message":       map[string]interface{}{"type": "string", "minLength": 1, "description": "The question, with the context the Goal Lead needs (what you are doing, the options, the evidence)."},
				"wait_seconds":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": goalLeadAskMaxWaitSecond},
				"submission_id": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this ask; reuse it after an uncertain retry to get the original call."},
			},
			"required": []string{"message"},
		}),
	}}
	execute := func(ctx context.Context, args map[string]interface{}) (string, error) {
		api := pulsePlatformAPI
		if api == nil {
			return "", fmt.Errorf("ask_goal_lead is unavailable in this process")
		}
		sessionID := strings.TrimSpace(executor.SessionIDFromContext(ctx))
		if sessionID == "" {
			sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		}
		if isGoalLeadSessionID(sessionID) {
			return "", fmt.Errorf("the Goal Lead cannot ask itself")
		}
		workspacePath, claims, err := api.pulseToolScope(ctx, "", false)
		if err != nil {
			return "", err
		}
		if !workflowHasGoal(ctx, workspacePath) {
			return "", fmt.Errorf("this workflow has no Goal Lead yet: it needs soul.md and a primary goal metric")
		}
		wait := 60 * time.Second
		if raw, ok := args["wait_seconds"]; ok {
			seconds := intToolArg(map[string]interface{}{"v": raw}, "v")
			if seconds < 0 || seconds > goalLeadAskMaxWaitSecond {
				return "", fmt.Errorf("wait_seconds must be 0-%d", goalLeadAskMaxWaitSecond)
			}
			wait = time.Duration(seconds) * time.Second
		}
		callerLabel := "a chat of this workflow"
		if _, background := stepworkflow.LookupWorkshopToolSession(sessionID); background {
			callerLabel = "a step or helper of this workflow"
		}
		out, err := api.askGoalLead(ctx, claims.UserID, workspacePath, sessionID, callerLabel, stringToolArg(args, "message"), wait, stringToolArg(args, "submission_id"))
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	}
	return tool, execute
}

// createGoalLeadChatKindTools are the phase 4 Pulse writes: focus areas and
// QA requests (executors in createPulseWorklistTools).
func createGoalLeadChatKindTools() []llmtypes.Tool {
	return []llmtypes.Tool{
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_focus_area",
			Description: "The Goal Lead's focus areas (what matters now, between soul.md and goal memory). action=propose: a new focus from evidence or the owner's words, with text, end_date (YYYY-MM-DD, at most 90 days), check (how it is judged, e.g. \"drafts waiting 3 -> 0\") and why; at most three open at once; it waits for the owner's one-click confirm and is not active before that. action=track: daily, progress moving, stuck (note = the one clear ask) or done. action=close: status done, expired (say why; propose extend, change or drop) or dropped, with a one-line lesson that also goes to goal memory. Focus areas change what you attend to, never what you may do.",
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
			Description: "Ask for QA / technical review as a separate run (the Goal Lead's sub-agent): a Pulse fix run (Technical Review+Fix in its own session) starts when the workflow is free and its short result comes back to the Goal Lead conversation (goal_lead.qa_results). Use it for a step that fails, outputs that look wrong, or a fix to verify; do not do long repair work in the Goal Lead conversation. One open request at a time, three a day.",
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
