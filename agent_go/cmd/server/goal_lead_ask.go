package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// ask_pulse (PLAT-697 phase 4): a workflow's chats and steps ask its
// Pulse, by the same function-call mechanism as ask_project_chat
// (startCrewFunctionCall with a chat target): the call has an id and a saved
// record, runs as a turn in the Pulse conversation (queued behind its
// current turn), and the Pulse's final reply is the answer. The answer is
// a recommendation: the Pulse does not decide for the owner. The caller
// and the workflow come from the trusted session (pulseToolScope), never from
// arguments. Asks are capped per workflow like asks between a Code's chats.
//
// The owner talks to Pulse only through their Builder chat (owner,
// 2026-10-08; the Pulse tab has no input): the Builder chat relays the
// owner's question or direction with ask_pulse and shows the reply. Such an
// ask is an owner relay (goalLeadAskCaller), so Pulse records lasting
// direction in goal memory or proposes a focus area, as it does for Slack.
//
// Threads (owner, 2026-10-08: "they are not having a conversation, just
// exchanging one-off msgs"): an ask can continue an earlier one on the same
// topic with its thread_id, so Pulse reads it as the next round of the same
// exchange in its conversation. Pulse either asks the chat back for a fact
// ("question: …", the chat answers in the same thread) or concludes with
// "decision: …" and "owner_needed: yes|no (why)", which closes the thread. At
// most goalLeadThreadMaxRounds rounds per thread; every round also counts
// toward the hourly cap. Pulse asks back in its reply, never by calling the
// chat, so no new ping-pong path between the two chats exists.

const (
	goalLeadAskCreatedBy     = "platform (goal lead)"
	goalLeadAsksPerHour      = 20
	goalLeadAskMessageRunes  = 8000
	goalLeadAskMaxWaitSecond = 120
	goalLeadThreadMaxRounds  = 6
	goalLeadThreadIdle       = 24 * time.Hour
)

// goalLeadThread is one ask_pulse exchange on one topic between a chat of the
// workflow and its Pulse.
type goalLeadThread struct {
	workspacePath string
	callerSession string
	rounds        int
	closed        bool
	updatedAt     time.Time
}

var goalLeadThreads = struct {
	sync.Mutex
	byID map[string]*goalLeadThread
}{byID: map[string]*goalLeadThread{}}

// goalLeadThreadRefusalLocked says why threadID may not take another round
// from callerSession; the caller holds goalLeadThreads.
func goalLeadThreadRefusalLocked(threadID, workspacePath, callerSession string) error {
	thread := goalLeadThreads.byID[threadID]
	switch {
	case thread == nil || thread.workspacePath != workspacePath || thread.callerSession != callerSession:
		return fmt.Errorf("unknown thread_id %q for this chat; omit thread_id to start a new thread", threadID)
	case thread.closed:
		return fmt.Errorf("thread %s is concluded: Pulse gave its decision. Act on it; omit thread_id to start a new thread on another topic", threadID)
	case thread.rounds >= goalLeadThreadMaxRounds:
		return fmt.Errorf("thread %s reached %d rounds: act on Pulse's last answer, or ask the owner once quoting it", threadID, goalLeadThreadMaxRounds)
	}
	return nil
}

// checkGoalLeadThread reports whether threadID may take another round.
func checkGoalLeadThread(threadID, workspacePath, callerSession string) error {
	goalLeadThreads.Lock()
	defer goalLeadThreads.Unlock()
	return goalLeadThreadRefusalLocked(strings.TrimSpace(threadID), workspacePath, callerSession)
}

// admitGoalLeadThreadRound opens a thread (threadID empty) or admits the next
// round of one: the same workflow and asking chat, not concluded, under the
// round cap. It returns the thread id and the round number.
func admitGoalLeadThreadRound(threadID, workspacePath, callerSession string, now time.Time) (string, int, error) {
	goalLeadThreads.Lock()
	defer goalLeadThreads.Unlock()
	for id, thread := range goalLeadThreads.byID {
		if now.Sub(thread.updatedAt) > goalLeadThreadIdle {
			delete(goalLeadThreads.byID, id)
		}
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = newGoalLeadID("PT-")
		goalLeadThreads.byID[threadID] = &goalLeadThread{workspacePath: workspacePath, callerSession: callerSession, rounds: 1, updatedAt: now}
		return threadID, 1, nil
	}
	if err := goalLeadThreadRefusalLocked(threadID, workspacePath, callerSession); err != nil {
		return "", 0, err
	}
	thread := goalLeadThreads.byID[threadID]
	thread.rounds++
	thread.updatedAt = now
	return threadID, thread.rounds, nil
}

func closeGoalLeadThread(threadID string) {
	goalLeadThreads.Lock()
	defer goalLeadThreads.Unlock()
	if thread := goalLeadThreads.byID[strings.TrimSpace(threadID)]; thread != nil {
		thread.closed = true
	}
}

// goalLeadAnswer is what an ask turn's reply says in its closing lines.
type goalLeadAnswer struct {
	Decision       string
	OwnerNeeded    string // "yes", "no" or "" when missing
	OwnerNeededWhy string
	Question       string
}

var (
	goalLeadDecisionLine    = regexp.MustCompile(`(?im)^[\s*_>-]*decision[*_]*\s*:[*_\s]*(.+?)\s*$`)
	goalLeadOwnerNeededLine = regexp.MustCompile(`(?im)^[\s*_>-]*owner_needed[*_]*\s*:[*_\s]*(yes|no)\b[*_]*(.*?)\s*$`)
	goalLeadQuestionLine    = regexp.MustCompile(`(?im)^[\s*_>-]*question[*_]*\s*:[*_\s]*(.+?)\s*$`)
)

func parseGoalLeadAnswer(reply string) goalLeadAnswer {
	var out goalLeadAnswer
	if m := goalLeadDecisionLine.FindStringSubmatch(reply); m != nil {
		out.Decision = strings.TrimSpace(m[1])
	}
	if m := goalLeadOwnerNeededLine.FindStringSubmatch(reply); m != nil {
		out.OwnerNeeded = strings.ToLower(m[1])
		out.OwnerNeededWhy = strings.Trim(strings.TrimSpace(m[2]), " ()-:,;.–—")
	}
	if m := goalLeadQuestionLine.FindStringSubmatch(reply); m != nil {
		out.Question = strings.TrimSpace(m[1])
	}
	return out
}

// concluded: Pulse gave both closing lines.
func (a goalLeadAnswer) concluded() bool { return a.Decision != "" && a.OwnerNeeded != "" }

// goalLeadAskNext tells the asking chat what to do with Pulse's answer.
func goalLeadAskNext(a goalLeadAnswer, threadID string) string {
	switch {
	case a.concluded() && a.OwnerNeeded == "no":
		return "Pulse decided; the thread is closed. Pulse is the goal expert: do not ask the owner again. Act on the decision (if Pulse already made the change, check and report it), then tell the owner in one line: \"Pulse recommended <decision> because <why>; done.\""
	case a.concluded():
		return "Pulse says the owner must decide. Ask the owner once, quoting Pulse's recommendation and why it is their call; the thread is closed."
	case a.Question != "":
		return fmt.Sprintf("Pulse needs a fact before it decides. Find it, then answer with ask_pulse(thread_id=%q, message=<the fact>). Do not ask the owner for it unless only they can know it.", threadID)
	default:
		return fmt.Sprintf("Pulse gave no decision lines. If you need its decision, ask_pulse(thread_id=%q) once more asking for \"decision:\" and \"owner_needed:\"; otherwise treat its answer as advice.", threadID)
	}
}

func goalLeadAskFunction() crewFunction {
	return crewFunction{
		Name:        crewFunctionAskName,
		Description: "Ask the workflow's Pulse for a recommendation on goal work.",
		InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"message"}, "properties": map[string]interface{}{
			"message":   map[string]interface{}{"type": "string"},
			"thread_id": map[string]interface{}{"type": "string"},
			"round":     map[string]interface{}{"type": "integer"},
		}},
		ResultSchema: map[string]interface{}{"type": "object", "required": []interface{}{"answer"}, "properties": map[string]interface{}{
			"answer":           map[string]interface{}{"type": "string"},
			"thread_id":        map[string]interface{}{"type": "string"},
			"round":            map[string]interface{}{"type": "integer"},
			"decision":         map[string]interface{}{"type": "string"},
			"owner_needed":     map[string]interface{}{"type": "string"},
			"owner_needed_why": map[string]interface{}{"type": "string"},
			"pulse_question":   map[string]interface{}{"type": "string"},
			"thread_open":      map[string]interface{}{"type": "boolean"},
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

// askGoalLead starts an ask_pulse call from callerLabel (a chat or step of
// the workflow at workspacePath, callerSession its session) and waits up to
// wait for the answer. ownerRelay marks the owner's own words from their
// Builder chat. threadID continues an earlier ask on the same topic.
func (api *StreamingAPI) askGoalLead(ctx context.Context, userID, workspacePath, callerSession, callerLabel, message, threadID string, ownerRelay bool, wait time.Duration, submissionID string) (map[string]interface{}, error) {
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
	callerChat := &codeChat{Key: "session:" + firstNonEmptyTrimmed(callerSession, "unknown"), Name: firstNonEmptyTrimmed(callerLabel, label+" chat")}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Label: callerChat.Name, Path: workspacePath, Chat: callerChat, OwnerRelay: ownerRelay}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: workspacePath, Label: label + " Pulse", Manifest: manifest,
		Chat: &codeChat{Key: "goal-lead", ID: "goal-lead", Name: label + " Pulse", SessionID: conv.SessionID}}
	callerKey := firstNonEmptyTrimmed(callerSession, "unknown")
	if strings.TrimSpace(threadID) != "" {
		// Reject a bad thread before it costs an hourly ask.
		if err := checkGoalLeadThread(threadID, workspacePath, callerKey); err != nil {
			return nil, err
		}
	}
	if err := admitGoalLeadAsk(workspacePath, time.Now()); err != nil {
		return nil, err
	}
	threadID, round, err := admitGoalLeadThreadRound(threadID, workspacePath, callerKey, time.Now())
	if err != nil {
		return nil, err
	}
	call, err := api.startCrewFunctionCall(ctx, userID, caller, target, goalLeadAskFunction(), map[string]interface{}{"message": message, "thread_id": threadID, "round": round}, triggerTargetDefaultTimeout, submissionID)
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
	out["thread_id"], out["round"] = threadID, round
	if !call.settled() {
		out["next"] = "The Pulse is still answering. Read its answer later with get_function_call(call_id), or continue without it."
	} else if result, ok := out["result"].(map[string]interface{}); ok {
		answer, _ := result["answer"].(string)
		out["next"] = goalLeadAskNext(parseGoalLeadAnswer(answer), threadID)
	}
	return out, nil
}

// runGoalLeadAsk runs an ask as a turn in the Pulse conversation and
// settles the call with its final reply.
func (api *StreamingAPI) runGoalLeadAsk(call *crewFunctionCall, target triggerTarget, caller triggerLinkCaller, args map[string]interface{}, timeout time.Duration) {
	message, _ := args["message"].(string)
	message = strings.TrimSpace(message)
	threadID, _ := args["thread_id"].(string)
	round := intToolArg(args, "round")
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
	reply, _, err := api.runGoalLeadTurn(ctx, target.Path, goalLeadTurn{Kind: goalLeadTurnAsk, From: caller.Label, Body: message, CallID: call.ID, OwnerRelay: caller.OwnerRelay, ThreadID: threadID, Round: round})
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
	parts := parseGoalLeadAnswer(reply)
	if parts.concluded() {
		closeGoalLeadThread(threadID)
	}
	result := map[string]interface{}{"answer": truncateTriggerTargetResult(reply), "thread_id": threadID, "round": round, "thread_open": !parts.concluded()}
	if parts.Decision != "" {
		result["decision"] = parts.Decision
	}
	if parts.OwnerNeeded != "" {
		result["owner_needed"], result["owner_needed_why"] = parts.OwnerNeeded, parts.OwnerNeededWhy
	}
	if parts.Question != "" && !parts.concluded() {
		result["pulse_question"] = parts.Question
	}
	call.settle("completed", result, "")
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
	description := "Ask this workflow's Pulse (the persistent owner of the workflow's goal) for a recommendation on goal work: which option serves the goal, what to prioritise, what the owner already said (goal memory). " +
		"In the Builder chat: when the owner asks how the goal is doing, why Pulse did or recommended something, or give direction for Pulse (\"tell Pulse to focus on X this week\"), pass their words with ask_pulse and show Pulse's answer; do not answer goal status yourself. Pulse records lasting direction in goal memory or proposes a focus area for the owner to confirm. " +
		"Pulse is the goal expert: its answer ends with question: (it needs a fact; answer in the same thread_id) or decision: and owner_needed:. With owner_needed: no, act on the decision without asking the owner again and tell them in one line (\"Pulse recommended X because Y; done.\"); with yes, ask the owner once, quoting Pulse. Follow the result's next. " +
		"When the owner approves something Pulse recommended: if pulse.autonomy.change is auto, pass the go-ahead to Pulse (same thread) and it makes the change; otherwise make the edit yourself and tell Pulse with a short \"done: ...\" so it records it in goal memory. " +
		"It runs as a turn in its own conversation. Make the message self-contained. " +
		"Waits up to wait_seconds (default 60) for the answer; otherwise returns a call_id to read later with get_function_call. Capped per workflow per hour. Only for workflows with a goal (soul.md and a primary goal metric)."
	if name == goalLeadAskToolAliasName {
		description = "Old name of ask_pulse, kept for one release; use ask_pulse. " + description
	}
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        name,
		Description: description,
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message":       map[string]interface{}{"type": "string", "minLength": 1, "description": "The question, with the context the Pulse needs (what you are doing, the options, the evidence)."},
				"thread_id":     map[string]interface{}{"type": "string", "description": "Continue an earlier ask on the same topic (the thread_id it returned): answer Pulse's question or follow up. Omit for a new topic."},
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
		workspacePath, claims, err := api.pulseToolScope(ctx, "", false)
		if err != nil {
			return "", err
		}
		if !workflowHasGoal(ctx, workspacePath) {
			return "", fmt.Errorf("this workflow has no goal yet: it needs soul.md and a primary goal metric")
		}
		wait := 60 * time.Second
		if raw, ok := args["wait_seconds"]; ok {
			seconds := intToolArg(map[string]interface{}{"v": raw}, "v")
			if seconds < 0 || seconds > goalLeadAskMaxWaitSecond {
				return "", fmt.Errorf("wait_seconds must be 0-%d", goalLeadAskMaxWaitSecond)
			}
			wait = time.Duration(seconds) * time.Second
		}
		callerLabel, ownerRelay := api.goalLeadAskCaller(ctx, workspacePath, sessionID, claims)
		out, err := api.askGoalLead(ctx, claims.UserID, workspacePath, sessionID, callerLabel, stringToolArg(args, "message"), stringToolArg(args, "thread_id"), ownerRelay, wait, stringToolArg(args, "submission_id"))
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	}
	return tool, execute
}

// goalLeadAskCaller names who asks and whether the ask carries the owner's own
// words. Only a person's Builder chat relays the owner: attended (not a step
// or helper, not scheduled), not a Slack/WhatsApp channel, in Builder
// (workshop) mode, by someone who may change the workflow. Steps, scheduled
// runs, bot channels, Run chats and readers ask for a recommendation only, so
// they cannot write "the owner said" into goal memory.
func (api *StreamingAPI) goalLeadAskCaller(ctx context.Context, workspacePath, sessionID string, claims *UserClaims) (string, bool) {
	if _, background := stepworkflow.LookupWorkshopToolSession(sessionID); background {
		return "a step or helper of this workflow", false
	}
	if isScheduledSession(sessionID) {
		return "a scheduled run of this workflow", false
	}
	info, ok := api.getActiveSession(sessionID)
	if !ok || info == nil || info.BotPlatform != "" || strings.HasPrefix(info.TurnProvider, "bot_") ||
		isScheduledSessionIdentity(sessionID, info.TriggeredBy) {
		return "a chat of this workflow", false
	}
	if normalizeChatHistoryWorkshopMode(info.WorkshopMode) == "run" || claims == nil {
		return "a Run chat of this workflow", false
	}
	if level, _ := workflowAccessForWorkspacePath(ctx, claims, workspacePath); level != WorkflowAccessOwner && level != WorkflowAccessWrite {
		return "a Run chat of this workflow", false
	}
	who := firstNonEmptyTrimmed(claims.Username, claims.Email, "the owner")
	return who + " in the Builder chat", true
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
