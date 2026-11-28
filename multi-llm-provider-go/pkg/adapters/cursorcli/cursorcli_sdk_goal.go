package cursorcli

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Cursor goals (#4623). Cursor SDK 1.5 offers the agent its own goal tools (CreateGoal, UpdateGoal with only
// active or complete) and keeps an ACTIVE goal going inside one run; the goal lives on the agent's checkpoint
// (goalState). Pausing is Cursor's client-side control, which the SDK does not offer. So:
//   - set and resume (Start again) are a turn asking the agent to create the goal and work on it;
//   - the goal's state is read from the checkpoint when the turn ends (the bridge's "goal" event);
//   - Stop ends the run and AgentWorks records the goal as stopped; Cursor's own goal is still ACTIVE then
//     and would carry on in the next message, so that next message first asks the agent to end it (the
//     bridge sends the note only while the checkpoint still says ACTIVE).
//
// show, pause and clear never reach Cursor; the server answers them.

const cursorGoalEndNote = "<system_notification source=\"agentworks\">The user stopped the active goal. Before anything else, end it: call UpdateGoal (cursor namespace, through CallDynamicTool) with status complete. Do no more work on it; then answer only the user's message below.</system_notification>\n\n"

func cursorGoalPrompt(action, objective string) string {
	again := ""
	if action == "resume" {
		again = " This goal was stopped earlier; pick it up where the work stopped."
	}
	return "The user asked for a long-running goal." + again + " Call CreateGoal (cursor namespace, through CallDynamicTool) with exactly this objective, then work on it until it is done:\n\n" + objective
}

type cursorGoalTurnKey struct{}

// cursorGoalTurnState is this turn's goal: a goal command (goal) or the first message after Stop or Clear (end).
type cursorGoalTurnState struct {
	goal *cursorGoal
	end  bool
}

// cursorGoal is one goal turn of a Cursor chat.
type cursorGoal struct {
	owner, objective, action string
	started                  time.Time
}

// cursorGoalTurn reads the chat's admitted goal command for this turn. A set or resume replaces the person's
// pending /goal text with the goal prompt; a goal pause (the next message after Stop or Clear) asks the bridge to
// end Cursor's still-active goal first. Unsupported actions never reach a turn.
func cursorGoalTurn(ctx context.Context, owner string, conversation []llmtypes.MessageContent) ([]llmtypes.MessageContent, *cursorGoal, bool, error) {
	_, endGoal := llmtypes.NativeGoalTurnPauseForSession(ctx, owner)
	command, ok := llmtypes.NativeGoalCommandForSession(ctx, owner)
	if !ok {
		return conversation, nil, endGoal, nil
	}
	if command.Action != "set" && command.Action != "resume" {
		return nil, nil, false, fmt.Errorf("Cursor can't %s a goal from a turn", command.Action)
	}
	objective := strings.TrimSpace(command.Objective)
	if objective == "" {
		return nil, nil, false, fmt.Errorf("No goal is set")
	}
	end := len(conversation)
	for end > 0 && conversation[end-1].Role == llmtypes.ChatMessageTypeHuman {
		end--
	}
	out := append(append([]llmtypes.MessageContent{}, conversation[:end]...), llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: cursorGoalPrompt(command.Action, objective)}}})
	return out, &cursorGoal{owner: owner, objective: objective, action: command.Action, started: time.Now()}, false, nil
}

// cursorGoalStatus maps Cursor's GoalStatus (enum number or name) to the goal bar's status.
func cursorGoalStatus(status string) string {
	switch strings.ToUpper(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(status)), "GOAL_STATUS_")) {
	case "1", "ACTIVE":
		return "active"
	case "2", "PAUSED":
		return "paused"
	case "3", "COMPLETE":
		return "complete"
	case "4", "CLEARED":
		return "cleared"
	}
	return ""
}

// cursorGoalUpdate turns the bridge's goal report into the chat's goal. A goal turn whose run ended while
// Cursor still had the goal ACTIVE has stopped working on it: it shows as stopped, and Start again picks it up.
// A report of no goal on a goal turn means this Cursor did not create one (its goal tools were not offered).
func cursorGoalUpdate(agentID string, goal *cursorGoal, event cursorSDKEvent) (llmtypes.NativeGoalUpdate, bool) {
	// Only a goal turn reports; a late report from an earlier goal turn never changes a later turn's chat.
	if goal == nil {
		return llmtypes.NativeGoalUpdate{}, false
	}
	status := cursorGoalStatus(event.Status)
	objective := strings.TrimSpace(event.Text)
	if objective == "" {
		objective = goal.objective
	}
	update := llmtypes.NativeGoalUpdate{Provider: "cursor-cli", NativeSessionID: agentID, Reason: "native_turn_finished"}
	unreadable := strings.TrimSpace(event.Status) == "unknown"
	switch {
	case (status == "" || unreadable) && !event.Done:
		return update, false // not created yet, or not readable now
	case unreadable:
		status = "complete" // the goal turn finished; Cursor's own state could not be read
	case status == "", status == "cleared":
		update.Reason = "goal_not_created"
		return update, true
	case status == "active" && event.Done:
		status, update.Reason = "paused", "native_run_ended"
	}
	seconds := event.DurationMS / 1000
	if seconds <= 0 {
		seconds = int64(time.Since(goal.started).Seconds())
	}
	update.Goal = &llmtypes.NativeGoal{Objective: objective, Status: status, TimeUsedSeconds: &seconds}
	return update, true
}

func (g *cursorGoal) log(agentID, status, reason string) {
	log.Printf("[AGENT_GOAL] session=%s provider=cursor-cli native=%s action=recorded status=%s reason=%s", g.owner, agentID, status, reason)
}
