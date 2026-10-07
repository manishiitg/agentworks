package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
)

// Slack reaches a workflow's Goal Lead (PLAT-697 phase 4) by the slug
// "<workflow-slug>-goal" on any app that reaches the workflow: Slack
// membership decides the channels, the slug picks the target (PLAT-668). The
// Goal Lead is never attached on its own; it rides on its workflow's target,
// so it is reachable exactly where the workflow is, and the server lets only
// people with access to the workflow talk to it. A thread or DM bound to it
// keeps talking to it. Its replies come from the one Goal Lead conversation,
// not from a bot session of the thread.

const (
	SlackGoalLeadAgent      = "goal_lead"
	SlackGoalLeadSlugSuffix = "-goal"
)

// GoalLeadSlackFunc answers one message for a workflow's Goal Lead; reply
// posts into the message's thread.
type GoalLeadSlackFunc func(ctx context.Context, msg BotIncomingMessage, reply func(string))

var goalLeadSlackHandler atomic.Pointer[GoalLeadSlackFunc]

// SetGoalLeadSlackHandler installs the server's Goal Lead turn.
func SetGoalLeadSlackHandler(fn GoalLeadSlackFunc) {
	if fn == nil {
		goalLeadSlackHandler.Store(nil)
		return
	}
	goalLeadSlackHandler.Store(&fn)
}

// goalLeadSlugMatches returns the workflow targets in set whose slug, plus
// "-goal", is word.
func goalLeadSlugMatches(set SlackTargetSet, word string) []int {
	word = strings.ToLower(strings.TrimSpace(word))
	if !strings.HasSuffix(word, SlackGoalLeadSlugSuffix) {
		return nil
	}
	var out []int
	for _, index := range set.WithSlug(strings.TrimSuffix(word, SlackGoalLeadSlugSuffix)) {
		target := set.Targets[index]
		if strings.TrimSpace(target.Ref.ProfileID) == "" && strings.TrimSpace(target.Ref.Agent) == "" {
			out = append(out, index)
		}
	}
	return out
}

// GoalLeadSlackTarget is the Goal Lead riding on a workflow target.
func GoalLeadSlackTarget(base SlackTarget) SlackTarget {
	target := base
	target.Ref.Agent = SlackGoalLeadAgent
	target.Slug = base.Slug + SlackGoalLeadSlugSuffix
	target.Label = strings.TrimSpace(firstNonEmptyString(base.Label, base.Slug) + " Goal Lead")
	target.Legacy = false
	return target
}

// goalLeadRoute marks a workflow route for its Goal Lead.
func goalLeadRoute(route *ChannelRoute) *ChannelRoute {
	if route == nil || IsRevokedSlackRoute(*route) || strings.TrimSpace(route.WorkflowID) == "" {
		return route
	}
	marked := *route
	marked.GoalLead = true
	return &marked
}

func goalLeadAmbiguousReply(slug string) string {
	return fmt.Sprintf("More than one workflow here is called `%s`. Ask in the workflow's own channel, or use its own bot.", strings.TrimSuffix(slug, SlackGoalLeadSlugSuffix))
}

// handleGoalLeadMessage runs a Goal Lead message in the background and reports
// whether it took the message.
func (m *BotConversationManager) handleGoalLeadMessage(msg BotIncomingMessage) bool {
	if msg.PresetWorkflow == nil || !msg.PresetWorkflow.GoalLead || !strings.EqualFold(strings.TrimSpace(msg.Platform), "slack") {
		return false
	}
	fn := goalLeadSlackHandler.Load()
	if fn == nil {
		return false
	}
	thread := ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, ConnectionID: msg.ConnectionID}
	if thread.ThreadTS == "" {
		thread.ThreadTS = msg.ChannelID
	}
	connector := m.GetConnector(msg.Platform)
	reply := func(text string) {
		text = strings.TrimSpace(text)
		if connector == nil || text == "" {
			return
		}
		if _, err := connector.SendThreadMessage(context.Background(), thread, text); err != nil {
			log.Printf("[SLACK_GOAL_LEAD] reply to %s failed: %v", thread.Key(), err)
		}
	}
	go (*fn)(context.Background(), msg, reply)
	return true
}
