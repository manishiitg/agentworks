package services

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/slack-go/slack"
)

// Inbound slug routing (docs/design/slack_slugs.md, PLAT-668). Parsing runs
// before routing and only against the allowed list: a first word that is not
// an allowed slug is message text, never a target.

// slackChannelPick is how one channel message is routed.
type slackChannelPick struct {
	text  string
	route *ChannelRoute
	// reply answers instead of a turn (list, a refused re-pick, an ack).
	reply string
	// choices asks with one button per target; the message waits.
	choices []SlackTarget
	slug    string
	// bind binds the thread to this target when the turn starts.
	bind *SlackTarget
	// handled: nothing more to do (e.g. a blocked sender).
	handled bool
}

func containsIndex(indexes []int, index int) bool {
	for _, candidate := range indexes {
		if candidate == index {
			return true
		}
	}
	return false
}

func pickTargets(set SlackTargetSet, indexes []int) []SlackTarget {
	out := make([]SlackTarget, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, set.Targets[index])
	}
	return out
}

// pickChannelRoute routes a channel message on this app: the thread's bound
// target (a different slug is refused), else "@bot <slug>", else the
// channel's default, else (on a mention) one button per allowed target.
func (s *SlackService) pickChannelRoute(ctx context.Context, userEmail, channelID, threadTS, text string, isMention bool) slackChannelPick {
	pick := s.selectChannelTarget(ctx, channelID, threadTS, text, isMention)
	if pick.route != nil && !IsRevokedSlackRoute(*pick.route) && !SlackRouteAllowsEmail(*pick.route, userEmail) {
		return slackChannelPick{handled: true}
	}
	return pick
}

func (s *SlackService) selectChannelTarget(ctx context.Context, channelID, threadTS, text string, isMention bool) slackChannelPick {
	hooks := currentSlackRoutingHooks()
	if hooks == nil {
		return slackChannelPick{text: text, route: s.resolveSlackThreadRoute(ctx, channelID, threadTS)}
	}
	channelID = NormalizeSlackChannelID(channelID)
	thread := ThreadID{Platform: "slack", ChannelID: channelID, ThreadTS: threadTS, ConnectionID: s.connectionID}
	set := hooks.Channel(ctx, s.connectionID, channelID)
	if set.Disabled {
		return slackChannelPick{text: text, route: RevokedSlackRoute()}
	}
	if len(set.Targets) == 0 {
		return slackChannelPick{text: text}
	}
	var matches []int
	slug, rest := "", text
	// goal: the slug was "<workflow-slug>-pulse", the workflow's Pulse.
	goal := false
	if isMention {
		if word, remainder := SplitSlackSlugWord(text); word == "list" && remainder == "" {
			return slackChannelPick{reply: FormatSlackTargetList(set, "in this channel")}
		}
		matches, slug, rest = PickSlackSlug(text, set)
		if len(matches) == 0 {
			if word, remainder := SplitSlackSlugWord(text); word != "" {
				if goalMatches := goalLeadSlugMatches(set, word); len(goalMatches) > 0 {
					matches, slug, rest, goal = goalMatches, word, remainder, true
				}
			}
		}
	}
	if bound, found := LoadSlackThreadTarget(ctx, thread); found {
		// A Pulse thread rides on its workflow's target.
		index := set.Find(bound.Ref.Base())
		if index < 0 {
			// Removed from the channel's list (or switched off): the thread
			// stops, it is never handed to another target.
			return slackChannelPick{text: text, route: RevokedSlackRoute()}
		}
		boundGoal := strings.EqualFold(strings.TrimSpace(bound.Ref.Agent), SlackGoalLeadAgent)
		if len(matches) > 0 {
			if !containsIndex(matches, index) || goal != boundGoal {
				return slackChannelPick{reply: fmt.Sprintf("This thread is with `%s`. Start a new thread (mention me in the channel) to talk to `%s`.", firstNonEmptyString(bound.Slug, set.Targets[index].Slug), slug)}
			}
			text = rest
		}
		route := slackSetRoute(ctx, hooks, s.connectionID, channelID, set.Targets[index])
		if boundGoal {
			route = goalLeadRoute(route)
		}
		return slackChannelPick{text: text, route: route}
	}
	chosen := -1
	switch {
	case len(matches) == 1:
		chosen = matches[0]
		text = rest
	case len(matches) > 1 && goal:
		return slackChannelPick{reply: goalLeadAmbiguousReply(slug)}
	case len(matches) > 1:
		return slackChannelPick{text: rest, choices: pickTargets(set, matches), slug: slug}
	case len(set.Targets) == 1:
		// One target in the channel: it answers, no slug needed. With
		// several, people add the slug or pick from buttons; a saved
		// default never applies to people (owner, 2026-10-07).
		chosen = 0
	case !isMention:
		// A plain reply in a thread no target holds starts nothing.
		return slackChannelPick{text: text}
	default:
		all := make([]int, len(set.Targets))
		for i := range all {
			all[i] = i
		}
		return slackChannelPick{text: text, choices: pickTargets(set, all)}
	}
	target := set.Targets[chosen]
	pick := slackChannelPick{text: text, route: slackSetRoute(ctx, hooks, s.connectionID, channelID, target)}
	if goal {
		target = GoalLeadSlackTarget(target)
		pick.route = goalLeadRoute(pick.route)
	}
	if isMention && !IsRevokedSlackRoute(*pick.route) {
		pick.bind = &target
		if len(matches) == 1 && strings.TrimSpace(text) == "" {
			pick.reply = fmt.Sprintf("This thread is now with *%s*. Reply here with your question.", firstNonEmptyString(target.Label, target.Slug))
		}
	}
	return pick
}

// deliverChannelPick posts what a pick answers instead of a turn, remembers a
// message waiting on buttons, and binds the thread. It reports whether the
// turn should start.
func (s *SlackService) deliverChannelPick(ctx context.Context, msg BotIncomingMessage, raw *slack.Msg, pick slackChannelPick) bool {
	thread := ThreadID{Platform: "slack", ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, ConnectionID: s.connectionID}
	if pick.bind != nil {
		if err := SaveSlackThreadTarget(ctx, thread, SlackThreadTarget{Ref: pick.bind.Ref, Slug: pick.bind.Slug, BoundBy: msg.UserID}); err != nil {
			log.Printf("[SLACK_SLUG] binding thread %s to %s failed: %v", thread.Key(), pick.bind.Slug, err)
		}
	}
	if len(pick.choices) > 0 {
		msg.Text = pick.text
		rememberSlackPendingPick(thread, msg, raw, pick.choices)
		if _, err := s.SendThreadMessageWithBlocks(ctx, thread, slackPickPrompt(pick.choices, pick.slug), slackPickBlocks(pick.choices)); err != nil {
			log.Printf("[SLACK_SLUG] target prompt failed: %v", err)
		}
		return false
	}
	if pick.reply != "" {
		if _, err := s.SendThreadMessage(ctx, thread, pick.reply); err != nil {
			log.Printf("[SLACK_SLUG] reply failed: %v", err)
		}
		return false
	}
	return !pick.handled
}

// dryRunPickReply is what a dry run reports for a pick that answers instead
// of a turn.
func dryRunPickReply(pick slackChannelPick) string {
	if pick.reply != "" {
		return pick.reply
	}
	if len(pick.choices) > 0 {
		names := make([]string, 0, len(pick.choices))
		for _, choice := range pick.choices {
			names = append(names, choice.Slug)
		}
		return slackPickPrompt(pick.choices, pick.slug) + " [buttons: " + strings.Join(names, ", ") + "]"
	}
	return ""
}

// handleSlackPick handles a target button: it rechecks that the target is
// still allowed here (and, in a DM, that the person can reach it), binds the
// thread, and runs the waiting message. Only the asker's click counts.
func (s *SlackService) handleSlackPick(ctx context.Context, channelID, threadTS, userID, value string) {
	hooks := currentSlackRoutingHooks()
	if hooks == nil {
		return
	}
	channelID = NormalizeSlackChannelID(channelID)
	thread := ThreadID{Platform: "slack", ChannelID: channelID, ThreadTS: threadTS, ConnectionID: s.connectionID}
	dm := IsSlackDMChannel(channelID)
	pending, waiting := takeSlackPendingPick(thread)
	if waiting && pending.msg.UserID != userID {
		rememberSlackPendingPick(thread, pending.msg, pending.raw, pending.choices)
		return
	}
	if dm && !waiting {
		_, _ = s.SendThreadMessage(ctx, thread, "That question expired. Start your message with a slug instead.")
		return
	}
	ref := slackRefFromKey(value)
	var set SlackTargetSet
	routeChannel := channelID
	if dm {
		set = hooks.DM(ctx, s.connectionID)
		routeChannel = ""
	} else {
		set = hooks.Channel(ctx, s.connectionID, channelID)
	}
	index := set.Find(ref)
	if set.Disabled || index < 0 {
		_, _ = s.SendThreadMessage(ctx, thread, "That choice is no longer available here.")
		return
	}
	target := set.Targets[index]
	if bound, found := LoadSlackThreadTarget(ctx, thread); found && !dm && !bound.Ref.Same(target.Ref) {
		_, _ = s.SendThreadMessage(ctx, thread, fmt.Sprintf("This thread is already with `%s`. Start a new thread to talk to `%s`.", bound.Slug, target.Slug))
		return
	}
	route := slackSetRoute(ctx, hooks, s.connectionID, routeChannel, target)
	if IsRevokedSlackRoute(*route) {
		_, _ = s.SendThreadMessage(ctx, thread, "That choice is no longer available here.")
		return
	}
	if dm {
		if !hooks.CanReach(ctx, pending.msg.WorkspaceUserID, *route) {
			_, _ = s.SendThreadMessage(ctx, thread, "You can't reach that one.")
			return
		}
	} else if !SlackRouteAllowsEmail(*route, s.resolveUserEmail(userID)) {
		return
	}
	if err := SaveSlackThreadTarget(ctx, thread, SlackThreadTarget{Ref: target.Ref, Slug: target.Slug, BoundBy: userID}); err != nil {
		log.Printf("[SLACK_SLUG] binding thread %s to %s failed: %v", thread.Key(), target.Slug, err)
	}
	label := firstNonEmptyString(target.Label, target.Slug)
	if !waiting || strings.TrimSpace(pending.msg.Text) == "" {
		_, _ = s.SendThreadMessage(ctx, thread, fmt.Sprintf("This thread is now with *%s*. Reply here with your question.", label))
		return
	}
	if s.messageHandler == nil {
		return
	}
	msg := pending.msg
	msg.PresetWorkflow = route
	msg.Text = s.appendSlackFileContext(ctx, msg.Text, pending.raw, channelID, msg.UserEmail, route)
	s.messageHandler(msg)
}

// slackDMPick is how one DM is routed.
type slackDMPick struct {
	text    string
	route   *ChannelRoute
	reply   string
	choices []SlackTarget
	bind    *SlackTarget
}

// pickDMRoute routes a DM from accountID: "<slug> ..." (also after the bot's
// mention) picks a target and is remembered for the DM; otherwise the
// remembered one, else the app's own target, else the list. Only targets the
// person can reach with their own access count; a slug they cannot reach is
// message text.
func (s *SlackService) pickDMRoute(ctx context.Context, hooks *SlackRoutingHooks, accountID, channelID, text string) slackDMPick {
	set := hooks.DM(ctx, s.connectionID)
	if set.Disabled {
		return slackDMPick{reply: "This bot is switched off. Ask its owner to turn it back on."}
	}
	routes := map[int]*ChannelRoute{}
	reachable := func(index int) bool {
		if route, ok := routes[index]; ok {
			return route != nil
		}
		route, err := hooks.Route(ctx, s.connectionID, "", set.Targets[index])
		if err != nil || route == nil || !hooks.CanReach(ctx, accountID, *route) {
			routes[index] = nil
			return false
		}
		routes[index] = route
		return true
	}
	mine := func() SlackTargetSet {
		out := SlackTargetSet{Default: -1}
		for i := range set.Targets {
			if reachable(i) {
				if i == set.Default {
					out.Default = len(out.Targets)
				}
				out.Targets = append(out.Targets, set.Targets[i])
			}
		}
		return out
	}
	word, remainder := SplitSlackSlugWord(text)
	if word == "list" && remainder == "" {
		return slackDMPick{reply: FormatSlackTargetList(mine(), "for you here")}
	}
	var matches []int
	for _, index := range set.WithSlug(word) {
		if reachable(index) {
			matches = append(matches, index)
		}
	}
	// "<workflow-slug>-pulse": the workflow's Pulse, for people who can
	// reach the workflow.
	goal := false
	if len(matches) == 0 {
		for _, index := range goalLeadSlugMatches(set, word) {
			if reachable(index) {
				matches = append(matches, index)
			}
		}
		goal = len(matches) > 0
	}
	thread := ThreadID{Platform: "slack", ChannelID: channelID, ThreadTS: channelID, ConnectionID: s.connectionID}
	chosen := -1
	switch {
	case len(matches) == 1:
		chosen = matches[0]
		text = remainder
	case len(matches) > 1 && goal:
		return slackDMPick{reply: goalLeadAmbiguousReply(word)}
	case len(matches) > 1:
		return slackDMPick{text: remainder, choices: pickTargets(set, matches)}
	default:
		if bound, found := LoadSlackThreadTarget(ctx, thread); found {
			if index := set.Find(bound.Ref.Base()); index >= 0 && reachable(index) {
				chosen = index
				goal = strings.EqualFold(strings.TrimSpace(bound.Ref.Agent), SlackGoalLeadAgent)
			}
		}
	}
	if chosen < 0 {
		// Nothing picked: one reachable target answers; with several, the
		// bot asks with buttons (as in a channel).
		var reachableIdx []int
		for i := range set.Targets {
			if reachable(i) {
				reachableIdx = append(reachableIdx, i)
			}
		}
		switch len(reachableIdx) {
		case 0:
			return slackDMPick{reply: "Nothing is reachable for you through this bot yet. A workflow's, Crew's or Code's owner turns it on in its Integrations → Slack tab."}
		case 1:
			chosen = reachableIdx[0]
		default:
			return slackDMPick{text: text, choices: pickTargets(set, reachableIdx)}
		}
	}
	target := set.Targets[chosen]
	pick := slackDMPick{text: text, route: routes[chosen]}
	if goal {
		target = GoalLeadSlackTarget(target)
		pick.route = goalLeadRoute(pick.route)
	}
	if len(matches) == 1 {
		pick.bind = &target
		if strings.TrimSpace(text) == "" {
			pick.reply = fmt.Sprintf("Now talking to *%s*. Ask away; start a message with another slug to switch.", firstNonEmptyString(target.Label, target.Slug))
		}
	}
	return pick
}
