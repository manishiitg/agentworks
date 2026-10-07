package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// ask_builder (PLAT-697): the Pulse asks the workflow's Builder chat, the
// reverse of ask_pulse, by the same function-call mechanism (a call id and a
// saved record, the chain guard, 20 an hour per workflow, a bounded wait).
//
//   - kind=question: "what changed in step X and why?", "what did the owner
//     decide about Dubai?". The Builder chat answers from its own
//     conversation; nothing in it may change, run or send anything for it.
//   - kind=fix: a concrete repair with evidence. Only when the turn holds
//     pulse.autonomy.change=auto. At ask the request becomes one decision with
//     the Pulse's recommendation; the owner's Accept sends it to the Builder
//     chat through the existing apply-in-chat path (decision_apply_chat.go).
//     Never for soul.md, deletions or contract migrations, and never from a
//     failed-run turn (questions only there).
//
// The target is the owner's most recently active Builder chat for the
// workflow (live first, else the latest saved one, as the web Builder
// restores it). With none, a question is refused and a fix becomes a decision;
// no chat is created. The ask runs there as a normal turn, so the owner sees
// the request and the reply in that chat. While it runs, that chat's tools are
// held by the phase 2 guard: a question may not change, run or send anything;
// a fix may change the workflow but not run it, send, delete or migrate.

const (
	pulseBuilderAskCreatedBy = "platform (pulse ask builder)"
	pulseBuilderAskToolName  = "ask_builder"
	pulseBuilderAskKindQ     = "question"
	pulseBuilderAskKindFix   = "fix"
	pulseBuilderChatKey      = "goal-lead"
)

func pulseBuilderAskFunction() crewFunction {
	return crewFunction{
		Name:        crewFunctionAskName,
		Description: "The workflow's Pulse asks its Builder chat a question or for a bounded fix.",
		InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"message"}, "properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string"},
		}},
		ResultSchema: map[string]interface{}{"type": "object", "required": []interface{}{"answer"}, "properties": map[string]interface{}{
			"answer": map[string]interface{}{"type": "string"},
		}},
		CreatedBy: pulseBuilderAskCreatedBy,
	}
}

func isPulseBuilderAsk(target triggerTarget, fn crewFunction) bool {
	return target.Kind == triggerCallerWorkflow && target.Chat != nil && fn.CreatedBy == pulseBuilderAskCreatedBy
}

// hourlyAskLimiter caps asks per workflow per hour.
type hourlyAskLimiter struct {
	sync.Mutex
	byWorkflow map[string][]time.Time
}

func (l *hourlyAskLimiter) admit(workspacePath string, now time.Time, perHour int) bool {
	l.Lock()
	defer l.Unlock()
	if l.byWorkflow == nil {
		l.byWorkflow = map[string][]time.Time{}
	}
	kept := l.byWorkflow[workspacePath][:0]
	for _, at := range l.byWorkflow[workspacePath] {
		if now.Sub(at) < time.Hour {
			kept = append(kept, at)
		}
	}
	if len(kept) >= perHour {
		l.byWorkflow[workspacePath] = kept
		return false
	}
	l.byWorkflow[workspacePath] = append(kept, now)
	return true
}

var pulseBuilderAsks = &hourlyAskLimiter{}

// goalLeadTurnKinds records the kind of the Pulse turn running in a session,
// so ask_builder knows a failed-run turn (questions only).
var goalLeadTurnKinds sync.Map

func markGoalLeadTurnKind(sessionID, kind string) func() {
	goalLeadTurnKinds.Store(sessionID, kind)
	return func() { goalLeadTurnKinds.CompareAndDelete(sessionID, kind) }
}

func goalLeadTurnKindFor(sessionID string) string {
	kind, _ := goalLeadTurnKinds.Load(strings.TrimSpace(sessionID))
	value, _ := kind.(string)
	return value
}

// pulseBuilderForbiddenFix names what a fix request may never ask for.
var pulseBuilderForbiddenFix = regexp.MustCompile(`(?i)soul\.md|\bsoul file\b|\b(?:delete|remove|drop)\b[^.]{0,40}\b(?:steps?|schedules?|routes?|groups?|plan|workflow)\b|\bmigrat|contract[_ ]version|replace the (?:whole )?plan|create_plan`)

// pulseBuilderAskRequest is one ask_builder call from a Pulse turn.
type pulseBuilderAskRequest struct {
	UserID        string
	WorkspacePath string
	PulseSession  string
	Kind          string
	Message       string
	Evidence      string
	Title         string
	Why           string
	BuilderChat   string
	Perms         stepworkflow.GoalWorkPermissions
	TurnKind      string
	Wait          time.Duration
	SubmissionID  string
}

// findPulseBuilderChat returns the Builder chat ask_builder targets: the one
// requested when it is the owner's Builder chat of this workflow, else the
// owner's most recently active one; "" when there is none. Tests replace it.
var findPulseBuilderChat = func(api *StreamingAPI, ctx context.Context, userID string, manifest *WorkflowManifest, workspacePath, requested string) (string, error) {
	userCtx := internalBotRequestContext(ctx, userID)
	if requested = strings.TrimSpace(requested); requested != "" {
		if separateWorkflowConversation(requested) || isGoalLeadSessionID(requested) {
			return "", fmt.Errorf("builder_session_id %q is not a Builder chat", requested)
		}
		if active, ok := api.getActiveSession(requested); ok && active != nil && active.AgentMode == "workflow_phase" &&
			(active.UserID == "" || active.UserID == userID) && strings.Trim(active.WorkspacePath, "/") == workspacePath {
			return requested, nil
		}
		if _, found, err := findWorkflowBuilderConversationPathForSession(userCtx, userID, requested, workspacePath); err == nil && found {
			return requested, nil
		}
		return "", fmt.Errorf("builder_session_id %q is not one of the owner's Builder chats of this workflow; leave it empty to use the most recent one", requested)
	}
	return api.userWorkflowChat(ctx, userID, services.ChannelRoute{WorkflowID: manifest.ID, WorkspacePath: workspacePath}), nil
}

// pulseBuilderAskTurn, when set (tests), replaces the Builder chat turn.
var pulseBuilderAskTurn func(ctx context.Context, reqMap map[string]interface{}, sessionID, userID string) (internalSessionTurnResult, error)

// askBuilder runs one ask_builder call.
func (api *StreamingAPI) askBuilder(ctx context.Context, req pulseBuilderAskRequest) (map[string]interface{}, error) {
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	if req.Kind == "" {
		req.Kind = pulseBuilderAskKindQ
	}
	if req.Kind != pulseBuilderAskKindQ && req.Kind != pulseBuilderAskKindFix {
		return nil, fmt.Errorf("kind must be question or fix")
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return nil, fmt.Errorf("message is required")
	}
	if len([]rune(req.Message)) > goalLeadAskMessageRunes {
		return nil, fmt.Errorf("message is longer than %d characters; point to files in the workflow for detail", goalLeadAskMessageRunes)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, req.WorkspacePath)
	if err != nil || !found || manifest == nil {
		return nil, fmt.Errorf("cannot read this workflow")
	}
	if req.Kind == pulseBuilderAskKindFix {
		if req.TurnKind == goalLeadTurnRunFailed {
			return nil, fmt.Errorf("refused: a failed-run turn may ask the Builder chat questions only; for a failure that threatens the goal call record_pulse_qa_request")
		}
		if match := pulseBuilderForbiddenFix.FindString(req.Message + "\n" + req.Title); match != "" {
			return nil, fmt.Errorf("refused: ask_builder never asks for soul.md edits, deletions or contract migrations (matched %q); propose it to the owner as a decision instead", match)
		}
		if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Why) == "" || strings.TrimSpace(req.Evidence) == "" {
			return nil, fmt.Errorf("a fix request needs title (one plain sentence for the owner), why (how it serves the goal) and evidence (runs, steps, numbers)")
		}
		if err := checkPulsePlainText("evidence", plainTextField{name: "title", text: req.Title, maxLen: 200}, plainTextField{name: "why", text: req.Why, maxLen: 400}); err != nil {
			return nil, err
		}
		if !req.Perms.Change {
			return api.pulseBuilderFixDecision(ctx, req, "pulse.autonomy.change is ask for this turn")
		}
	}
	if err := pulseBuilderPingPong(manifest.ID); err != nil {
		return nil, err
	}
	builderSession, err := findPulseBuilderChat(api, ctx, req.UserID, manifest, req.WorkspacePath, req.BuilderChat)
	if err != nil {
		return nil, err
	}
	if builderSession == "" {
		if req.Kind == pulseBuilderAskKindFix {
			return api.pulseBuilderFixDecision(ctx, req, "the workflow has no Builder chat of the owner's yet")
		}
		return map[string]interface{}{"status": "no_builder_chat", "note": "The owner has no Builder chat for this workflow yet, so there is no conversation to ask. Read planning/changelog and the run records yourself, or ask the owner in your message."}, nil
	}
	if api.conversationTurnOccupied(builderSession) {
		return nil, fmt.Errorf("the Builder chat is busy with another turn now (the owner may be using it); ask again on a later turn")
	}
	if !pulseBuilderAsks.admit(req.WorkspacePath, time.Now(), goalLeadAsksPerHour) {
		return nil, fmt.Errorf("refused: the Builder chat was asked %d times in the last hour; continue with what you have", goalLeadAsksPerHour)
	}
	label := firstNonEmptyTrimmed(manifest.Label, req.WorkspacePath)
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Label: label + " Pulse", Path: req.WorkspacePath,
		Chat: &codeChat{Key: pulseBuilderChatKey, ID: pulseBuilderChatKey, Name: label + " Pulse", SessionID: req.PulseSession}}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: req.WorkspacePath, Label: label + " Builder chat", Manifest: manifest,
		Chat: &codeChat{Key: "session:" + builderSession, ID: builderSession, Name: label + " Builder chat", SessionID: builderSession}}
	args := map[string]interface{}{"message": req.Message, "kind": req.Kind}
	if req.Evidence != "" {
		args["evidence"] = strings.TrimSpace(req.Evidence)
	}
	call, err := api.startCrewFunctionCall(ctx, req.UserID, caller, target, pulseBuilderAskFunction(), args, triggerTargetDefaultTimeout, req.SubmissionID)
	if err != nil {
		return nil, err
	}
	if req.Wait > 0 {
		select {
		case <-call.done:
		case <-time.After(req.Wait):
		case <-ctx.Done():
		}
	}
	out := call.snapshot()
	out["builder_session_id"] = builderSession
	out["kind"] = req.Kind
	if !call.settled() {
		out["next"] = "The Builder chat is still working. Its answer is saved and shown to you as builder_asks on your next goal check (or read it with get_function_call(call_id)); continue without it."
	} else if req.Kind == pulseBuilderAskKindQ {
		out["next"] = "Record what matters from this answer in goal memory: record_pulse_goal_memory(source=\"builder_answer\"), one dated line."
	}
	return out, nil
}

// pulseBuilderPingPong refuses an ask_builder from a Pulse turn that a
// Builder chat started with ask_pulse: the answer goes back on its own. The
// chain guard already refuses the same chat; this also covers the owner's
// other Builder chats. Steps that asked the Pulse may still be followed by a
// question to the Builder.
func pulseBuilderPingPong(workflowID string) error {
	pulseKey := crewFunctionChatChainKey(crewFunctionScopedKey(triggerCallerWorkflow, "", workflowID, ""), pulseBuilderChatKey)
	chain, _ := crewFunctionChainFor(pulseKey)
	marker := crewFunctionChatMarker + "session:"
	for _, key := range chain {
		i := strings.Index(key, marker)
		if i < 0 {
			continue
		}
		session := key[i+len(marker):]
		if _, background := stepworkflow.LookupWorkshopToolSession(session); background {
			continue
		}
		return fmt.Errorf("refused: a Builder chat asked you (ask_pulse) in this exchange; answer it in your reply instead of asking a Builder chat back")
	}
	return nil
}

// pulseBuilderFixDecision turns a fix request into one decision with the
// Pulse's recommendation. Accept sends it to the Builder chat (apply in chat).
func (api *StreamingAPI) pulseBuilderFixDecision(ctx context.Context, req pulseBuilderAskRequest, why string) (map[string]interface{}, error) {
	input, err := createReportHumanInput(ctx, req.WorkspacePath, ReportHumanInputCreateRequest{
		Source:   "strategic_review",
		Priority: "normal",
		Question: strings.TrimSpace(req.Title),
		Context:  "Pulse wants the Builder chat to make this fix:\n" + req.Message + "\n\nWhy: " + strings.TrimSpace(req.Why) + "\n\nAccept sends it to the Builder chat, which makes the change where you can watch.",
		Options: []ReportHumanInputOption{
			{ID: "approve", Title: "Make this fix", Description: "The Builder chat makes the change and checks it."},
			{ID: "decline", Title: "Leave it as it is", Description: "Nothing changes."},
		},
		Evidence:      strings.TrimSpace(req.Evidence),
		CreatedBy:     "pulse",
		CreatedByKind: "agent",
		CreatedVia:    pulseBuilderAskToolName,
		SessionID:     req.PulseSession,
		ApplyContract: ReportHumanInputApplyContract{Mode: "targeted_fixer", ApprovedScope: req.Message},
	})
	if err != nil {
		return nil, fmt.Errorf("could not record the fix as a decision: %w", err)
	}
	recCtx := executor.WithSessionID(ctx, req.PulseSession)
	if _, err := recordPulseRecommendationFromToolArgs(recCtx, map[string]interface{}{
		"workspace_path": req.WorkspacePath, "input_id": input.ID, "option_id": "approve",
		"why": req.Why, "evidence": req.Evidence, "confidence": "medium",
	}); err != nil {
		return nil, fmt.Errorf("decision %s was created but the recommendation was not recorded: %w", input.ID, err)
	}
	return map[string]interface{}{
		"status":   "decision_created",
		"input_id": input.ID,
		"reason":   "Not sent to the Builder chat: " + why + ".",
		"note":     "The fix is now one decision with your recommendation (Make this fix). The owner's Accept sends it to the Builder chat. Name it in your check summary and goal message; do not ask again.",
	}, nil
}

// pulseBuilderAskText is the Builder chat's turn for an ask.
func pulseBuilderAskText(callID, label, kind, message, evidence string) string {
	if kind == pulseBuilderAskKindFix {
		var b strings.Builder
		fmt.Fprintf(&b, "[Function call %s] The %s (the platform's owner of this workflow's goal) asks this Builder chat for a fix (ask_builder; the owner set pulse.autonomy.change to auto):\n\n%s\n", callID, label, strings.TrimSpace(message))
		if evidence = strings.TrimSpace(evidence); evidence != "" {
			fmt.Fprintf(&b, "\nEvidence: %s\n", evidence)
		}
		b.WriteString("\nMake the smallest change that does this with the normal typed Builder tools, check it (validate_plan_change when the plan changed) and re-read what you changed. Do not edit soul/soul.md, delete steps or schedules, replace the plan or migrate contracts; if the fix needs any of that, change nothing and say so. Do not run the workflow, back up, publish or notify. Your final reply goes back to the Pulse: what you changed, what you checked and what a later run must still prove.")
		return b.String()
	}
	return fmt.Sprintf("[Function call %s] The %s (the platform's owner of this workflow's goal) asks this Builder chat a question (ask_builder):\n\n%s\n\nAnswer from this conversation and the workflow's files: what changed, when and why, and what the owner said or decided. Say plainly what you do not know. Do not change, run or send anything for this question. Your final reply goes back to the Pulse; make it self-contained.", callID, label, strings.TrimSpace(message))
}

// runPulseBuilderAsk runs the ask as a turn in the Builder chat and settles
// the call with its final reply.
func (api *StreamingAPI) runPulseBuilderAsk(call *crewFunctionCall, target triggerTarget, caller triggerLinkCaller, args map[string]interface{}, timeout time.Duration) {
	hardCap := crewFunctionHardCap(timeout)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: call.UserID}), hardCap)
	defer cancel()
	ctx = virtualtools.WithFeedbackOperation(ctx, call.ID)
	message, _ := args["message"].(string)
	kind, _ := args["kind"].(string)
	evidence, _ := args["evidence"].(string)
	session := target.Chat.SessionID
	recordPulseBuilderAsk(ctx, target.Path, call.ID, kind, session, message)
	// The record comes first: a waiting caller reads it as soon as the call
	// settles.
	settle := func(status, answer, failure string) {
		finishPulseBuilderAsk(context.WithoutCancel(ctx), target.Path, call.ID, status, firstNonEmptyTrimmed(answer, failure))
		text := "The Builder chat answered: " + answer
		if status != "completed" {
			text = "The Builder chat did not answer: " + failure
		}
		_ = appendGoalLeadMessage(context.WithoutCancel(ctx), target.Path, GoalLeadMessage{Role: "builder_answer", Source: kind, Text: text})
		if status == "completed" {
			call.settle(status, map[string]interface{}{"answer": truncateTriggerTargetResult(answer)}, "")
		} else {
			call.settle(status, nil, failure)
		}
	}
	if target.Manifest == nil {
		settle("failed", "", "workflow is unavailable")
		return
	}
	_ = appendGoalLeadMessage(ctx, target.Path, GoalLeadMessage{Role: "ask_builder", Source: kind, Text: "Asked the Builder chat (" + kind + "): " + message})
	query := QueryRequest{
		Query: pulseBuilderAskText(call.ID, caller.Label, kind, message, evidence), AgentMode: "workflow_phase", PhaseID: "workflow-builder",
		PresetQueryID: target.Manifest.ID, SelectedFolder: target.Path,
		TriggeredBy: "external", TriggeredByLabel: "Pulse (" + kind + ")",
		ExecutionOptions: &ExecutionOptions{WorkshopMode: "workshop"},
	}
	if api.workflowAskSessionExists(session, target.Path) {
		query.RestoredConversationSessionID = session
	}
	reqMap, err := queryRequestToMap(query)
	if err != nil {
		settle("failed", "", "cannot build the request: "+err.Error())
		return
	}
	call.mu.Lock()
	call.Status = "running"
	call.RunID, call.RunIDs = session, []string{session}
	call.mu.Unlock()
	call.persist()
	// The Builder chat's tools for this turn: a question changes, runs and
	// sends nothing; a fix may change the workflow, never run it or send.
	// Deleting, replacing the plan and migrations stay refused either way.
	release := beginGoalWorkTurn(session, stepworkflow.GoalWorkPermissions{Change: kind == pulseBuilderAskKindFix})
	defer release()
	turnDone := make(chan struct{})
	go api.watchAskActivity(call, session, fmt.Sprintf("the Builder chat of %q", target.Manifest.Label), timeout, turnDone)
	var result internalSessionTurnResult
	if pulseBuilderAskTurn != nil {
		result, err = pulseBuilderAskTurn(ctx, reqMap, session, call.UserID)
	} else {
		result, err = api.startSessionInternalWithResult(ctx, reqMap, session, call.UserID, nil)
	}
	close(turnDone)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			settle("failed", "", fmt.Sprintf("the Builder chat was still not done after %s", hardCap))
			return
		}
		settle("failed", "", fmt.Sprintf("the Builder chat did not answer: %v", err))
		return
	}
	answer := strings.TrimSpace(result.FinalResponse)
	if answer == "" {
		settle("failed", "", "the Builder chat finished without a reply")
		return
	}
	settle("completed", answer, "")
}

// The asks and answers, kept with the Pulse state so a late answer reaches the
// next goal check (builder_asks).
const goalLeadBuilderAsksSchema = `CREATE TABLE IF NOT EXISTS goal_lead_builder_asks (
	call_id TEXT PRIMARY KEY,
	asked_at TEXT NOT NULL,
	kind TEXT NOT NULL,
	builder_session TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'running',
	answer TEXT NOT NULL DEFAULT '',
	answered_at TEXT NOT NULL DEFAULT ''
)`

func withGoalLeadBuilderAsks(ctx context.Context, workspacePath string, create bool, fn func(*sql.DB) error) error {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, create)
	if err != nil || db == nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, goalLeadBuilderAsksSchema); err != nil {
		return err
	}
	return fn(db)
}

func recordPulseBuilderAsk(ctx context.Context, workspacePath, callID, kind, session, message string) {
	_ = withGoalLeadBuilderAsks(ctx, workspacePath, true, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO goal_lead_builder_asks (call_id, asked_at, kind, builder_session, message) VALUES (?,?,?,?,?)`,
			callID, formatStoredTime(time.Now().UTC()), kind, session, goalLeadShortText(message))
		return err
	})
}

func finishPulseBuilderAsk(ctx context.Context, workspacePath, callID, status, answer string) {
	if runes := []rune(answer); len(runes) > goalLeadMessageMaxRunes {
		answer = string(runes[:goalLeadMessageMaxRunes]) + "…"
	}
	_ = withGoalLeadBuilderAsks(ctx, workspacePath, true, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `UPDATE goal_lead_builder_asks SET status=?, answer=?, answered_at=? WHERE call_id=?`,
			status, answer, formatStoredTime(time.Now().UTC()), callID)
		return err
	})
}

// recentPulseBuilderAsks are the asks since since, newest first.
func recentPulseBuilderAsks(ctx context.Context, workspacePath string, since time.Time, limit int) []map[string]string {
	out := []map[string]string{}
	_ = withGoalLeadBuilderAsks(ctx, workspacePath, false, func(db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `SELECT call_id, asked_at, kind, message, status, answer, answered_at FROM goal_lead_builder_asks
			WHERE asked_at > ? ORDER BY asked_at DESC LIMIT ?`, formatStoredTime(since.UTC()), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, at, kind, message, status, answer, answeredAt string
			if err := rows.Scan(&id, &at, &kind, &message, &status, &answer, &answeredAt); err != nil {
				return err
			}
			out = append(out, map[string]string{"call_id": id, "asked_at": at, "kind": kind, "message": message, "status": status, "answer": answer, "answered_at": answeredAt})
		}
		return rows.Err()
	})
	return out
}

// createPulseBuilderAskTool is ask_builder, for the Pulse conversation only.
func createPulseBuilderAskTool() (llmtypes.Tool, func(context.Context, map[string]interface{}) (string, error)) {
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name: pulseBuilderAskToolName,
		Description: "Pulse only: ask this workflow's Builder chat (the owner's most recently active one), where the owner can watch. kind=question: what changed in a step and why, what the owner decided; it answers from its own conversation and changes nothing. " +
			"kind=fix: one bounded repair with evidence (\"make step-growth-summary record the subscriber delta every run\"); sent only when pulse.autonomy.change is auto for this turn, otherwise it becomes one decision with your recommendation and the owner's Accept sends it to the Builder chat. Never for soul.md, deletions or contract migrations; a failed-run turn may only ask questions; never ask back a Builder chat that asked you. " +
			"Waits up to wait_seconds (default 90) for the reply; a later reply reaches your next goal check as builder_asks. Capped at 20 an hour per workflow.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"kind":               map[string]interface{}{"type": "string", "enum": []string{pulseBuilderAskKindQ, pulseBuilderAskKindFix}},
				"message":            map[string]interface{}{"type": "string", "minLength": 1, "description": "Self-contained question, or the exact fix (step ids, what to change); the Builder chat does not see your conversation."},
				"evidence":           map[string]interface{}{"type": "string", "description": "fix: runs, steps and numbers that show the problem."},
				"title":              map[string]interface{}{"type": "string", "description": "fix: one plain sentence for the owner, used when it becomes a decision (no code terms)."},
				"why":                map[string]interface{}{"type": "string", "description": "fix: how the fix serves the goal, plain words, at most 400 characters."},
				"builder_session_id": map[string]interface{}{"type": "string", "description": "Optional: one of the owner's Builder chats of this workflow (e.g. a plan change's session_id); default the most recently active one."},
				"wait_seconds":       map[string]interface{}{"type": "integer", "minimum": 0, "maximum": goalLeadAskMaxWaitSecond},
				"submission_id":      map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this ask; reuse it after an uncertain retry to get the original call."},
			},
			"required": []string{"kind", "message"},
		}),
	}}
	execute := func(ctx context.Context, args map[string]interface{}) (string, error) {
		api := pulsePlatformAPI
		if api == nil {
			return "", fmt.Errorf("%s is unavailable in this process", pulseBuilderAskToolName)
		}
		sessionID := strings.TrimSpace(executor.SessionIDFromContext(ctx))
		if sessionID == "" {
			sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		}
		if !isGoalLeadSessionID(sessionID) {
			return "", fmt.Errorf("%s is the Pulse's tool; a chat asks the Pulse with ask_pulse", pulseBuilderAskToolName)
		}
		workspacePath, claims, err := api.pulseToolScope(ctx, "", false)
		if err != nil {
			return "", err
		}
		perms, held := goalWorkTurnPermissions(sessionID)
		if !held {
			perms, _ = goalWorkAutonomy(ctx, workspacePath)
		}
		wait := 90 * time.Second
		if raw, ok := args["wait_seconds"]; ok {
			seconds := intToolArg(map[string]interface{}{"v": raw}, "v")
			if seconds < 0 || seconds > goalLeadAskMaxWaitSecond {
				return "", fmt.Errorf("wait_seconds must be 0-%d", goalLeadAskMaxWaitSecond)
			}
			wait = time.Duration(seconds) * time.Second
		}
		out, err := api.askBuilder(ctx, pulseBuilderAskRequest{
			UserID: claims.UserID, WorkspacePath: workspacePath, PulseSession: sessionID,
			Kind: stringToolArg(args, "kind"), Message: stringToolArg(args, "message"), Evidence: stringToolArg(args, "evidence"),
			Title: stringToolArg(args, "title"), Why: stringToolArg(args, "why"), BuilderChat: stringToolArg(args, "builder_session_id"),
			Perms: perms, TurnKind: goalLeadTurnKindFor(sessionID), Wait: wait, SubmissionID: stringToolArg(args, "submission_id"),
		})
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	}
	return tool, execute
}
