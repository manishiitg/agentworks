package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// ask_builder: the Pulse talks to the workflow's Builder chat, the reverse of
// ask_pulse and by the same function-call mechanism (call id, saved record,
// chain guard, 20 an hour per workflow, a bounded wait). The message arrives
// in the owner's most recently active Builder chat as plain text from the
// Pulse, where the owner can watch, and the chat's reply comes back. While
// that turn runs, the Builder chat's tools are held to the Pulse's own
// permission levels (the phase 2 guard), so the Pulse cannot get done through
// the Builder what it may not do itself; deleting, replacing the plan and
// migrations stay refused either way (owner, 2026-10-08: two agents talking).

const (
	pulseBuilderAskCreatedBy = "platform (pulse ask builder)"
	pulseBuilderAskToolName  = "ask_builder"
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

type pulseBuilderAskRequest struct {
	UserID        string
	WorkspacePath string
	PulseSession  string
	Message       string
	BuilderChat   string
	Perms         stepworkflow.GoalWorkPermissions
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
	if err := pulseBuilderPingPong(manifest.ID); err != nil {
		return nil, err
	}
	builderSession, err := findPulseBuilderChat(api, ctx, req.UserID, manifest, req.WorkspacePath, req.BuilderChat)
	if err != nil {
		return nil, err
	}
	if builderSession == "" {
		return map[string]interface{}{"status": "no_builder_chat", "note": "The owner has no Builder chat for this workflow yet. Read planning/changelog and the run records yourself, or ask the owner."}, nil
	}
	if api.conversationTurnOccupied(builderSession) {
		return nil, fmt.Errorf("the Builder chat is busy with another turn now (the owner may be using it); try again on a later turn")
	}
	if !pulseBuilderAsks.admit(req.WorkspacePath, time.Now(), goalLeadAsksPerHour) {
		return nil, fmt.Errorf("refused: the Builder chat was asked %d times in the last hour; continue with what you have", goalLeadAsksPerHour)
	}
	label := firstNonEmptyTrimmed(manifest.Label, req.WorkspacePath)
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Label: label + " Pulse", Path: req.WorkspacePath,
		Chat: &codeChat{Key: pulseBuilderChatKey, ID: pulseBuilderChatKey, Name: label + " Pulse", SessionID: req.PulseSession}}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: req.WorkspacePath, Label: label + " Builder chat", Manifest: manifest,
		Chat: &codeChat{Key: "session:" + builderSession, ID: builderSession, Name: label + " Builder chat", SessionID: builderSession}}
	args := map[string]interface{}{"message": req.Message, "level": req.Perms.Level, "run": req.Perms.Run,
		"outward": req.Perms.Outward, "change": req.Perms.Change, "reshape": req.Perms.Reshape}
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
	if !call.settled() {
		out["next"] = "The Builder chat is still replying. Its reply is saved and shown to you as builder_asks on your next goal check (or read it with get_function_call(call_id))."
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

// pulseBuilderAskText is the Builder chat's turn: the Pulse's message, with
// its sender.
func pulseBuilderAskText(label, message string) string {
	return label + ": " + strings.TrimSpace(message)
}

// runPulseBuilderAsk runs the ask as a turn in the Builder chat and settles
// the call with its final reply.
func (api *StreamingAPI) runPulseBuilderAsk(call *crewFunctionCall, target triggerTarget, caller triggerLinkCaller, args map[string]interface{}, timeout time.Duration) {
	hardCap := crewFunctionHardCap(timeout)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: call.UserID}), hardCap)
	defer cancel()
	ctx = virtualtools.WithFeedbackOperation(ctx, call.ID)
	message, _ := args["message"].(string)
	// The exact levels Pulse is held to travel with the call; the Builder chat
	// is held to the same ones.
	level := 0
	switch v := args["level"].(type) {
	case int:
		level = v
	case float64:
		level = int(v)
	}
	run, _ := args["run"].(bool)
	outward, _ := args["outward"].(bool)
	change, _ := args["change"].(bool)
	reshape, _ := args["reshape"].(bool)
	perms := stepworkflow.GoalWorkPermissions{Level: level, Run: run, Outward: outward, Change: change, Reshape: reshape}
	session := target.Chat.SessionID
	recordPulseBuilderAsk(ctx, target.Path, call.ID, "message", session, message)
	// The record comes first: a waiting caller reads it as soon as the call
	// settles.
	settle := func(status, answer, failure string) {
		finishPulseBuilderAsk(context.WithoutCancel(ctx), target.Path, call.ID, status, firstNonEmptyTrimmed(answer, failure))
		text := "The Builder chat answered: " + answer
		if status != "completed" {
			text = "The Builder chat did not answer: " + failure
		}
		_ = appendGoalLeadMessage(context.WithoutCancel(ctx), target.Path, GoalLeadMessage{Role: "builder_answer", Text: text})
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
	_ = appendGoalLeadMessage(ctx, target.Path, GoalLeadMessage{Role: "ask_builder", Text: "To the Builder chat: " + message})
	query := QueryRequest{
		Query: pulseBuilderAskText(caller.Label, message), AgentMode: "workflow_phase", PhaseID: "workflow-builder",
		PresetQueryID: target.Manifest.ID, SelectedFolder: target.Path,
		TriggeredBy: "external", TriggeredByLabel: "Pulse",
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
	// The chat shows this message as sent by Pulse, with its icon.
	if api.eventStore != nil {
		api.eventStore.ExpectUserMessageSender(session, "pulse", caller.Label)
	}
	// The Builder chat's tools for this turn are held to the Pulse's own
	// levels; deleting, replacing the plan and migrations stay refused.
	release := beginGoalWorkTurn(session, perms)
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
		Description: "Talk to this workflow's Builder chat (the owner's most recently active one, where they can watch): ask what changed and why or what the owner decided, or ask it to make a change. It works within your own permission levels, and its reply comes back. " +
			"Waits up to wait_seconds (default 90) for the reply; a later reply reaches your next goal check as builder_asks. Capped at 20 an hour per workflow.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message":            map[string]interface{}{"type": "string", "minLength": 1, "description": "Your message to the Builder chat, as you would write it to a colleague."},
				"builder_session_id": map[string]interface{}{"type": "string", "description": "Optional: one of the owner's Builder chats of this workflow (e.g. a plan change's session_id); default the most recently active one."},
				"wait_seconds":       map[string]interface{}{"type": "integer", "minimum": 0, "maximum": goalLeadAskMaxWaitSecond},
				"submission_id":      map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this message; reuse it after an uncertain retry to get the original call."},
			},
			"required": []string{"message"},
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
			Message: stringToolArg(args, "message"), BuilderChat: stringToolArg(args, "builder_session_id"),
			Perms: perms, Wait: wait, SubmissionID: stringToolArg(args, "submission_id"),
		})
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	}
	return tool, execute
}
