package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// ask_builder sends a conversational message to the owner's Builder chat.
// The receiving turn stays within current Pulse authority. Replies are
// optional explicit messages and are never captured from final chat text.

const (
	pulseBuilderAskCreatedBy = "platform (pulse ask builder)"
	pulseBuilderAskToolName  = "ask_builder"
	pulseBuilderChatKey      = "goal-lead"
)

func isPulseBuilderAsk(target triggerTarget, fn crewFunction) bool {
	return target.Kind == triggerCallerWorkflow && target.Chat != nil && fn.CreatedBy == pulseBuilderAskCreatedBy
}

type pulseBuilderAskRequest struct {
	UserID        string
	WorkspacePath string
	PulseSession  string
	Message       string
	BuilderChat   string
	Perms         stepworkflow.GoalWorkPermissions
	Wait          time.Duration
	SubmissionID  string
	InboxID       string
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

// askBuilder admits one explicit message to the Builder conversation.
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
	builderSession, err := findPulseBuilderChat(api, ctx, req.UserID, manifest, req.WorkspacePath, req.BuilderChat)
	if err != nil {
		return nil, err
	}
	if builderSession == "" {
		return map[string]interface{}{"status": "no_builder_chat", "note": "The owner has no Builder chat for this workflow yet. Read planning/changelog and the run records yourself, or ask the owner."}, nil
	}
	label := firstNonEmptyTrimmed(manifest.Label, req.WorkspacePath)
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Label: label + " Pulse", Path: req.WorkspacePath,
		Chat: &codeChat{Key: pulseBuilderChatKey, ID: pulseBuilderChatKey, Name: label + " Pulse", SessionID: req.PulseSession}}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: req.WorkspacePath, Label: label + " Builder chat", Manifest: manifest,
		Chat: &codeChat{Key: "session:" + builderSession, ID: builderSession, Name: label + " Builder chat", SessionID: builderSession}}
	out, err := api.sendAgentMessage(ctx, req.UserID, caller, target, req.Message, req.InboxID, req.SubmissionID)
	if err != nil {
		return nil, err
	}
	out["builder_session_id"] = builderSession
	_ = appendGoalLeadMessage(ctx, req.WorkspacePath, GoalLeadMessage{Role: "ask_builder", Text: "To the Builder chat: " + req.Message})
	return out, nil
}

// Historical request/result records remain readable in goal checks. New
// conversational messages live in the durable messaging store.
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
		Name:        pulseBuilderAskToolName,
		Description: "Send an explicit message to this workflow's Builder chat (the owner's most recently active one, where they can watch). Ask what changed or request a change within your permission levels. Returns an inbox/reply address and an acknowledgement. The Builder chooses whether and when to send a reply; final chat text is not forwarded. Read explicit replies with read_agent_messages(inbox_id), assess their evidence and record goal work yourself. Busy Builder chats receive the message after their current turn.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message":            map[string]interface{}{"type": "string", "minLength": 1, "description": "Your message to the Builder chat, as you would write it to a colleague."},
				"builder_session_id": map[string]interface{}{"type": "string", "description": "Optional: one of the owner's Builder chats of this workflow (e.g. a plan change's session_id); default the most recently active one."},
				"inbox_id":           map[string]interface{}{"type": "string", "description": "Existing conversation/reply address when continuing a conversation."},
				"submission_id":      map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this message; reuse it after an uncertain retry to get the original acknowledgement."},
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
		out, err := api.askBuilder(ctx, pulseBuilderAskRequest{
			UserID: claims.UserID, WorkspacePath: workspacePath, PulseSession: sessionID,
			Message: stringToolArg(args, "message"), BuilderChat: stringToolArg(args, "builder_session_id"),
			Perms: perms, InboxID: stringToolArg(args, "inbox_id"), SubmissionID: stringToolArg(args, "submission_id"),
		})
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	}
	return tool, execute
}
