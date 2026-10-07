package server

import (
	"context"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// handleGoalLeadSlack answers a Slack message for a workflow's Pulse
// ("<workflow-slug>-pulse", services/slack_goal_lead.go) in its persistent
// conversation. The slug routing already checked that this app reaches the
// workflow here; only people with access to the workflow may talk to its Goal
// Lead: a DM's matched account, or a channel sender whose Slack email maps to
// an account with access.
func (api *StreamingAPI) handleGoalLeadSlack(ctx context.Context, msg services.BotIncomingMessage, reply func(string)) {
	route := msg.PresetWorkflow
	if route == nil {
		return
	}
	accountID := strings.TrimSpace(msg.WorkspaceUserID)
	if accountID == "" {
		if id, ok := slackDMUserForEmail(msg.UserEmail); ok {
			accountID = id
		}
	}
	if accountID == "" || !api.slackCanReach(ctx, accountID, *route) {
		reply("Only people with access to this workflow can talk to its Pulse. Ask its owner for access.")
		return
	}
	workspacePath := strings.Trim(strings.TrimSpace(route.WorkspacePath), "/")
	if !workflowHasGoal(ctx, workspacePath) {
		reply("This workflow has no goal yet: it needs soul.md and a primary goal metric. Set one up in its Builder chat.")
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	from := firstNonEmptyTrimmed(msg.UserName, msg.UserEmail, accountID) + " (Slack)"
	turnCtx, cancel := context.WithTimeout(ctx, goalLeadTurnHardCap)
	defer cancel()
	answer, _, err := api.runGoalLeadTurn(turnCtx, workspacePath, goalLeadTurn{Kind: goalLeadTurnSlack, From: from, Body: text})
	if err != nil {
		log.Printf("[PULSE] Slack turn for %s failed: %v", workspacePath, err)
		reply("The Pulse could not answer just now: " + err.Error())
		return
	}
	if strings.TrimSpace(answer) == "" {
		answer = "Done. The Pulse's notes are in the workflow's Pulse tab."
	}
	reply(answer)
}
