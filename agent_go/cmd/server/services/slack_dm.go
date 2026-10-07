package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// 1:1 Slack DMs with a workflow's or crew's own bot run as the sender's
// AgentWorks account, with that account's own access
// (docs/design/bot_identity_model.md). Here the Slack side is proven: the
// conversation is a true 1:1 DM between the sender and this bot (asked of
// Slack, not read from the channel ID), the sender is a full member of the
// app's own team, and their Slack email maps to exactly one enabled account.
// The server re-checks the mapping and destination at every turn and tool
// call (slack_dm.go in the server package).

// SlackDMSender is what Slack reports about a DM and its sender.
type SlackDMSender struct {
	Name     string
	Email    string
	TeamID   string
	IsBot    bool
	Deleted  bool
	Guest    bool // is_restricted / is_ultra_restricted
	External bool // is_stranger: a Slack Connect user from another org
	// OneToOne: conversations.info says this is an IM with exactly this user.
	OneToOne bool
}

var (
	slackDMUserResolverMu sync.RWMutex
	slackDMUserResolver   func(email string) (userID string, ok bool)
)

// SetSlackDMUserResolver installs the account lookup for DM senders (the
// server's users.json). Without it no DM runs as a user.
func SetSlackDMUserResolver(fn func(email string) (string, bool)) {
	slackDMUserResolverMu.Lock()
	defer slackDMUserResolverMu.Unlock()
	slackDMUserResolver = fn
}

func resolveSlackDMUser(email string) (string, bool) {
	slackDMUserResolverMu.RLock()
	fn := slackDMUserResolver
	slackDMUserResolverMu.RUnlock()
	if fn == nil {
		return "", false
	}
	return fn(email)
}

// SetDMSenderLookup replaces the Slack API lookup of DM senders (tests).
func (s *SlackService) SetDMSenderLookup(fn func(ctx context.Context, userID, channelID string) (SlackDMSender, error)) {
	s.dmSenderLookup = fn
}

func (s *SlackService) lookupDMSender(ctx context.Context, userID, channelID string) (SlackDMSender, error) {
	if s.dmSenderLookup != nil {
		return s.dmSenderLookup(ctx, userID, channelID)
	}
	if s.client == nil {
		return SlackDMSender{}, fmt.Errorf("Slack client unavailable")
	}
	user, err := s.client.GetUserInfoContext(ctx, userID)
	if err != nil {
		return SlackDMSender{}, fmt.Errorf("users.info: %w", err)
	}
	channel, err := s.client.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channelID})
	if err != nil {
		return SlackDMSender{}, fmt.Errorf("conversations.info (needs im:read): %w", err)
	}
	return SlackDMSender{
		Name:     firstNonEmptyString(user.Profile.DisplayName, user.Profile.RealName, user.RealName, user.Name, userID),
		Email:    user.Profile.Email,
		TeamID:   user.TeamID,
		IsBot:    user.IsBot,
		Deleted:  user.Deleted,
		Guest:    user.IsRestricted || user.IsUltraRestricted,
		External: user.IsStranger,
		OneToOne: channel.IsIM && channel.User == userID,
	}, nil
}

// directMessage proves a DM and builds its bot message. refusal is the reply
// the sender gets instead of a turn; ignore means say nothing (other bots).
// With slug routing (PLAT-668) the platform bot takes DMs too, and the
// message reaches the target its slug (or the DM's remembered pick, or the
// app's own target) names, among those the sender can reach with their own
// access; pick carries the binding and any button prompt.
func (s *SlackService) directMessage(ctx context.Context, userID, channelID, threadTS, messageTS, text string, isThreadReply bool) (msg BotIncomingMessage, refusal string, ignore bool, pick slackDMPick) {
	hooks := currentSlackRoutingHooks()
	var route *ChannelRoute
	if hooks == nil {
		legacy, dedicated := DedicatedSlackRoute(ctx, s.connectionID, channelID)
		if !dedicated || legacy == nil {
			return msg, "I answer direct messages only as a workflow's or crew's own bot. Mention me in a channel instead.", false, pick
		}
		route = legacy
	}
	sender, err := s.lookupDMSender(ctx, userID, channelID)
	if err != nil {
		log.Printf("[SLACK_DM] connection=%s user=%s channel=%s: %v", s.connectionID, userID, channelID, err)
		return msg, "I couldn't verify this conversation with Slack. The app needs the im:read, im:history and users:read.email scopes; ask its owner to add them and reinstall.", false, pick
	}
	if sender.IsBot || sender.Deleted {
		return msg, "", true, pick
	}
	if !sender.OneToOne {
		return msg, "I act as you only in a 1:1 direct message. Mention me in a channel instead.", false, pick
	}
	if sender.Guest || sender.External || (s.teamID != "" && sender.TeamID != "" && sender.TeamID != s.teamID) {
		return msg, "Direct messages are for full members of this Slack workspace. Mention me in a channel instead.", false, pick
	}
	email := strings.TrimSpace(sender.Email)
	if route != nil && !SlackRouteAllowsEmail(*route, email) {
		return msg, "This bot blocks your email.", false, pick
	}
	accountID, ok := resolveSlackDMUser(email)
	if !ok {
		log.Printf("[SLACK_DM] connection=%s user=%s email=%q maps to no single enabled account", s.connectionID, userID, email)
		return msg, "Your Slack email doesn't match an AgentWorks account, so I can't act as you here. Ask an admin to add it, or mention me in a channel.", false, pick
	}
	if hooks != nil {
		pick = s.pickDMRoute(ctx, hooks, accountID, channelID, text)
		text, route = pick.text, pick.route
		if route != nil && !SlackRouteAllowsEmail(*route, email) {
			return msg, "This bot blocks your email.", false, slackDMPick{}
		}
	}
	return BotIncomingMessage{
		Platform:        "slack",
		UserID:          userID,
		UserName:        sender.Name,
		UserEmail:       email,
		WorkspaceUserID: accountID,
		ChannelID:       channelID,
		ConnectionID:    s.connectionID,
		ThreadTS:        threadTS,
		Text:            text,
		MessageTS:       messageTS,
		Timestamp:       time.Now(),
		IsThreadReply:   isThreadReply,
		IsMention:       true,
		PresetWorkflow:  route,
		DirectMessage:   true,
	}, "", false, pick
}

func (s *SlackService) handleSlackDirectMessage(ev *slackevents.MessageEvent) {
	// Edits, deletions and joins are not new messages; file shares are.
	if s.messageHandler == nil || ev.BotID != "" || (ev.SubType != "" && ev.SubType != "file_share") || strings.TrimSpace(ev.User) == "" {
		return
	}
	if s.botUserID != "" && ev.User == s.botUserID {
		return
	}
	if s.isDuplicateMessage(ev.Channel + ":" + ev.TimeStamp) {
		return
	}
	// A DM is one continuous chat: top-level messages share one
	// conversation whose thread id is the DM channel itself, and the bot
	// replies directly (slackThreadOption). A reply someone makes inside a
	// thread is answered in that thread, in the same chat.
	threadTS := ev.ThreadTimeStamp
	isThreadReply := threadTS != "" && threadTS != ev.TimeStamp
	if !isThreadReply {
		threadTS = ev.Channel
	}
	ctx := context.Background()
	text := s.stripMention(ev.Text)
	msg, refusal, ignore, pick := s.directMessage(ctx, ev.User, ev.Channel, threadTS, ev.TimeStamp, text, isThreadReply)
	if ignore {
		return
	}
	thread := ThreadID{Platform: "slack", ChannelID: ev.Channel, ThreadTS: threadTS, ConnectionID: s.connectionID}
	if refusal == "" {
		refusal = pick.reply
	}
	if pick.bind != nil {
		// The pick is remembered for the whole DM until another slug is used.
		if err := SaveSlackThreadTarget(ctx, thread, SlackThreadTarget{Ref: pick.bind.Ref, Slug: pick.bind.Slug, BoundBy: ev.User}); err != nil {
			log.Printf("[SLACK_DM] remembering %s failed: %v", pick.bind.Slug, err)
		}
	}
	if refusal != "" {
		if _, err := s.SendThreadMessage(ctx, thread, refusal); err != nil {
			log.Printf("[SLACK_DM] refusal reply failed: %v", err)
		}
		return
	}
	if len(pick.choices) > 0 {
		msg.Text = pick.text
		rememberSlackPendingPick(ThreadID{Platform: "slack", ChannelID: ev.Channel, ThreadTS: ev.Channel, ConnectionID: s.connectionID}, msg, ev.Message, pick.choices)
		if _, err := s.SendThreadMessageWithBlocks(ctx, thread, slackPickPrompt(pick.choices, ""), slackPickBlocks(pick.choices)); err != nil {
			log.Printf("[SLACK_DM] target prompt failed: %v", err)
		}
		return
	}
	log.Printf("[SLACK_DM] connection=%s user=%s account=%s channel=%s thread=%s", s.connectionID, ev.User, msg.WorkspaceUserID, ev.Channel, threadTS)
	msg.Text = s.appendSlackFileContext(ctx, msg.Text, ev.Message, ev.Channel, msg.UserEmail, msg.PresetWorkflow)
	s.messageHandler(msg)
}

// DryRunDirectMessage builds the message a 1:1 DM from senderSlackID would
// produce on this app, running the same proof as a real DM. Nothing is posted.
func (s *SlackService) DryRunDirectMessage(ctx context.Context, senderSlackID, channelID, text string) (BotIncomingMessage, string) {
	// A top-level DM: the conversation is the DM channel itself.
	channelID = strings.ToUpper(strings.TrimSpace(channelID))
	messageTS := fmt.Sprintf("dryrun.%d", time.Now().UnixNano())
	msg, refusal, ignore, pick := s.directMessage(ctx, senderSlackID, channelID, channelID, messageTS, text, false)
	if ignore {
		return msg, "ignored: the sender is a bot or a deleted account"
	}
	if refusal == "" {
		refusal = pick.reply
	}
	if refusal == "" && len(pick.choices) > 0 {
		refusal = dryRunPickReply(slackChannelPick{choices: pick.choices})
	}
	return msg, refusal
}
