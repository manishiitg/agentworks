package services

import (
	"context"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"strings"
)

// ErrLogicalCrewScope refuses a crew destination stored in its logical form.
var ErrLogicalCrewScope = errors.New("crew destination must use its physical project path")

// ValidateBotScope enforces the bot destination invariant
// (docs/design/bot_destination_scope.md): a stored crew destination is always
// the physical folder "_users/<owner>/...", which also names its owner. The
// logical form ("Chats/Work/projects/<id>") names no folder on the server and
// no owner; callers convert it at the API edge. Workflow destinations
// (no profile) are not constrained.
func ValidateBotScope(workspacePath, profileID string) error {
	if strings.TrimSpace(profileID) == "" || strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	if physicalBotScopeOwner(workspacePath) == "" {
		return fmt.Errorf("%w: %q", ErrLogicalCrewScope, strings.TrimSpace(workspacePath))
	}
	return nil
}

// physicalBotScopeOwner returns the owner named by a physical path, or "".
func physicalBotScopeOwner(workspacePath string) string {
	ref := workspaceref.MustParse(workspacePath)
	if !ref.HasOwner() || ref.Logical() == "" || strings.TrimSpace(ref.Owner()) == "" {
		return ""
	}
	return ref.Owner()
}

// ScopeResolver maps a stored destination to its physical form. It returns ""
// to keep the stored value (already physical, a workflow, or unresolvable).
// ownerHint is who is known to have created it, when recorded.
type ScopeResolver func(workspacePath, profileID, ownerHint string) string

// RewriteSlackScopes applies resolve to every connection scope and every
// per-connection channel route in one locked registry pass (the startup
// migration of docs/design/bot_destination_scope.md). It returns the IDs of
// connections whose own scope changed. Nothing is written when nothing
// changes.
func (s *SlackService) RewriteSlackScopes(ctx context.Context, resolve ScopeResolver) ([]string, error) {
	if resolve == nil {
		return nil, nil
	}
	needed := false
	for _, conn := range s.ListConnections() {
		if ValidateBotScope(conn.WorkspacePath, conn.ProfileID) != nil {
			needed = true
		}
		for _, route := range conn.ChannelRoutes {
			if ValidateBotScope(route.WorkspacePath, route.ProfileID) != nil {
				needed = true
			}
		}
	}
	if !needed {
		return nil, nil
	}
	var rescoped []string
	_, err := s.modifySlackRegistry(ctx, func(cfg *SlackConfig) (SlackConnection, error) {
		for i, conn := range cfg.Connections {
			if ValidateBotScope(conn.WorkspacePath, conn.ProfileID) != nil {
				if physical := resolve(conn.WorkspacePath, conn.ProfileID, ""); physical != "" {
					conn.WorkspacePath = physical
					rescoped = append(rescoped, conn.ID)
				}
			}
			for channelID, route := range conn.ChannelRoutes {
				if ValidateBotScope(route.WorkspacePath, route.ProfileID) != nil {
					if physical := resolve(route.WorkspacePath, route.ProfileID, route.AddedBy); physical != "" {
						route.WorkspacePath = physical
						conn.ChannelRoutes[channelID] = route
					}
				}
			}
			cfg.Connections[i] = conn
		}
		return SlackConnection{}, nil
	})
	return rescoped, err
}

// ApplyBotThreadFields sets the thread fields every bot turn carries:
// platform, channel, thread and arrival connection. All bot turn builders
// (workflow, crew conversation, default chat) use it so none can omit one; the
// crew builder once dropped the connection and revalidation refused the
// crew's own Slack bot (RTS 2026-09-26). The trigger stays with each builder
// (a bot workflow turn is scheduled-shaped, a chat turn is "bot:<platform>").
// platform may be empty when threadID names it.
func ApplyBotThreadFields(req map[string]interface{}, platform string, threadID ThreadID) {
	if req == nil {
		return
	}
	if platform = strings.TrimSpace(platform); platform == "" {
		platform = strings.TrimSpace(threadID.Platform)
	}
	if platform != "" {
		req["bot_platform"] = platform
	}
	if threadID.ChannelID != "" {
		req["bot_channel_id"] = threadID.ChannelID
	}
	if threadID.ThreadTS != "" {
		req["bot_thread_ts"] = threadID.ThreadTS
	}
	if threadID.ConnectionID != "" {
		req["bot_connection_id"] = threadID.ConnectionID
	}
}
