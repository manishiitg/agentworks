package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// A 1:1 Slack DM with a workflow's or crew's own bot runs as the AgentWorks
// user the sender maps to, with that user's own access: an owner or editor
// gets the full chat, a reader gets Run mode, anyone else is refused
// (docs/design/bot_identity_model.md). Channels, private channels and group
// DMs are groups and keep running as the route in Run mode (bot_route).
//
// The Slack service proves the sender at ingress (a full member of the app's
// own team, a true 1:1 DM checked with Slack, one directory account for the
// email). The query boundary and every tool call re-check what can change
// without Slack: the email still maps to the same account, the account is
// enabled, and the DM's bot still serves this destination.

const slackDMProvider = "bot_user"

// slackDMUserForEmail maps a Slack sender to exactly one enabled account in
// users.json. No directory, no match, several matches or a disabled account
// map to nobody: a DM never falls back to the machine's owner.
func slackDMUserForEmail(email string) (string, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", false
	}
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		return "", false
	}
	var match *UserRecord
	for i := range dir.Users {
		if strings.EqualFold(strings.TrimSpace(dir.Users[i].Email), email) {
			if match != nil {
				return "", false
			}
			match = &dir.Users[i]
		}
	}
	if match == nil || match.Disabled || strings.TrimSpace(match.ID) == "" {
		return "", false
	}
	return match.ID, true
}

// revalidateSlackDMPrincipal binds a DM turn to its sender's account and to
// the bot's own destination. It sets an execution principal (kind slack_dm)
// so the Slack tool reads this DM and nothing else; the principal carries no
// resource owner, so attachments and access are the sender's own.
func (api *StreamingAPI) revalidateSlackDMPrincipal(ctx context.Context, claims *UserClaims, req QueryRequest) (context.Context, error) {
	if req.BotPlatform != "slack" || strings.TrimSpace(req.BotChannelID) == "" {
		return ctx, fmt.Errorf("Slack DM has no trusted channel binding")
	}
	if userID, ok := slackDMUserForEmail(req.BotUserEmail); !ok || userID != claims.UserID {
		return ctx, fmt.Errorf("this Slack account no longer maps to an AgentWorks user")
	}
	var route services.ChannelRoute
	if services.SlackRoutingInstalled() {
		// The DM's target must still be one this app offers and the sender
		// can still reach with their own access (PLAT-668).
		matched, found := api.slackDMRouteMatching(ctx, req.BotConnectionID, claims.UserID, req)
		if !found {
			return ctx, fmt.Errorf("this Slack bot no longer reaches that target for you")
		}
		route = matched
	} else {
		_, routes, err := api.slackRoutes(ctx)
		if err != nil {
			return ctx, err
		}
		legacy, found, dedicated := api.slackRouteForConnection(ctx, req.BotConnectionID, req.BotChannelID, routes)
		if !found || !dedicated {
			return ctx, fmt.Errorf("direct messages need a workflow's or crew's own Slack bot")
		}
		route = legacy
	}
	if !services.SlackRouteAllowsEmail(route, req.BotUserEmail) {
		return ctx, fmt.Errorf("Slack email is blocked")
	}
	if !slackRouteFolderMatches(route, req.SelectedFolder) || req.AgentProfileID != route.ProfileID || req.PresetQueryID != route.WorkflowID || !sameProjectConversation(route.ConversationKey, req.AgentProfileConversationKey) {
		return ctx, fmt.Errorf("query does not match the Slack bot's destination")
	}
	copy := *claims
	copy.ExecutionPrincipal = &ExecutionPrincipal{Kind: "slack_dm", ID: "slack-dm:" + strings.TrimSpace(req.BotUserID), Target: route, AuditActor: req.BotUserID}
	if record := directoryUserFor(claims.UserID, "", ""); record != nil {
		copy.Username, copy.Email = record.Username, record.Email
	}
	return context.WithValue(ctx, UserContextKey, &copy), nil
}
