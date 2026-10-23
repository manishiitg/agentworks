package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ask_pulse sends a conversational message to the workflow's Pulse. Replies
// are explicit messages, never captured from the recipient's final chat turn.
// The caller and workflow come from the trusted session, not tool arguments.

const (
	goalLeadAskCreatedBy     = "platform (goal lead)"
	goalLeadAsksPerHour      = 20
	goalLeadAskMessageRunes  = 8000
	goalLeadAskMaxWaitSecond = 120
)

func isGoalLeadAsk(target triggerTarget, fn crewFunction) bool {
	return target.Kind == triggerCallerWorkflow && target.Chat != nil && fn.CreatedBy == goalLeadAskCreatedBy
}

func (api *StreamingAPI) messageGoalLead(ctx context.Context, userID, workspacePath, callerSession, callerLabel, message, inboxID, submissionID string) (map[string]interface{}, error) {
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
	if !manifest.PulseEnabled() || !workflowHasSoul(ctx, workspacePath) {
		return nil, fmt.Errorf("this workflow's Autopilot is off or has no goal")
	}
	conv, err := ensureGoalLeadConversation(ctx, workspacePath, firstNonEmptyTrimmed(manifest.ID, workspacePath), time.Now().UTC(), false, nil)
	if err != nil {
		return nil, err
	}
	label := firstNonEmptyTrimmed(manifest.Label, workspacePath)
	callerChat := &codeChat{Key: "session:" + callerSession, Name: firstNonEmptyTrimmed(callerLabel, label+" chat"), SessionID: callerSession}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Label: callerChat.Name, Path: workspacePath, Chat: callerChat}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: workspacePath, Label: label + " Autopilot", Manifest: manifest,
		Chat: &codeChat{Key: "goal-lead", ID: "goal-lead", Name: label + " Autopilot", SessionID: conv.SessionID}}
	return api.sendAgentMessage(ctx, userID, caller, target, message, inboxID, submissionID)
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
	description := "Send an explicit message to this workflow's Autopilot, the agent that owns its goal. Use it to discuss progress, priorities or the owner's direction. Returns an inbox/reply address and a transport acknowledgement. Autopilot chooses whether and when to reply with a message; its final chat answer is not forwarded. Read replies with read_agent_messages(inbox_id). Only for workflows with Autopilot enabled and a goal."
	if name == goalLeadAskToolAliasName {
		description = "Old name of ask_pulse, kept for one release; use ask_pulse. " + description
	}
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        name,
		Description: description,
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message":       map[string]interface{}{"type": "string", "minLength": 1, "description": "Your message to Autopilot, as you would write it to a colleague."},
				"inbox_id":      map[string]interface{}{"type": "string", "description": "Existing conversation/reply address, when continuing a conversation."},
				"submission_id": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this message; reuse it after an uncertain retry to get the original acknowledgement."},
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
			return "", fmt.Errorf("the Autopilot cannot ask itself")
		}
		// Pulse is talked to from the app and MCP, not from Slack or other bot
		// conversations (owner, 2026-10-08).
		if api.sessionIsBotConversation(sessionID) {
			return "", fmt.Errorf("Autopilot is not available from Slack or other bot conversations; tell the person to talk to Autopilot from the workflow's Builder chat in AgentWorks (#pulse) or an MCP client")
		}
		workspacePath, claims, err := api.pulseToolScope(ctx, "", false)
		if err != nil {
			return "", err
		}
		if !workflowHasGoal(ctx, workspacePath) {
			return "", fmt.Errorf("this workflow's Autopilot is off or has no goal yet (Autopilot on and soul/soul.md)")
		}
		callerLabel := api.goalLeadAskCaller(ctx, workspacePath, sessionID, claims)
		out, err := api.messageGoalLead(ctx, claims.UserID, workspacePath, sessionID, callerLabel, stringToolArg(args, "message"), stringToolArg(args, "inbox_id"), stringToolArg(args, "submission_id"))
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
// QA requests (executors in createPulseTools).

// sessionIsBotConversation reports a Slack (or other bot) conversation, or a
// chat started from one.
func (api *StreamingAPI) sessionIsBotConversation(sessionID string) bool {
	if _, bot := api.botExecutionForSession(sessionID); bot {
		return true
	}
	info, ok := api.getActiveSession(sessionID)
	return ok && info != nil && (info.BotPlatform != "" || strings.HasPrefix(info.TurnProvider, "bot_"))
}
