package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Resolve authority again immediately before a queued delivery enters a turn.
// Persist only the connection ID, never its bearer token; a reconnect can still
// continue an account inbox, but cannot revive an old revoked submission.
func (api *StreamingAPI) recheckAgentMessageAdmission(ctx context.Context, c agentConversation, m agentMessage) error {
	source, dest := c.Endpoints[m.From], c.Endpoints[1-m.From]
	if source.ShareID != "" || dest.ShareID != "" {
		store, err := readAgentMessageStore(ctx)
		if err != nil {
			return fmt.Errorf("sharing is unavailable")
		}
		if err := store.conversationShareErr(c, time.Now().UTC()); err != nil {
			return err
		}
	}
	if m.SenderConnectionID != "" {
		claims, err := agentMessageConnectionClaims(ctx, m.SenderConnectionID)
		if err != nil || claims.UserID != source.UserID {
			return fmt.Errorf("sending connection is no longer authorized")
		}
		if err := api.externalAuthorizeMessagePeer(context.WithValue(ctx, UserContextKey, claims), claims, dest, true); err != nil {
			return err
		}
		if source.NamedAgentID != "" {
			agent, err := namedAgentByID(ctx, source.UserID, source.NamedAgentID)
			if err != nil || !namedAgentConnectionAllowed(agent, claims) {
				return fmt.Errorf("sending named agent is no longer authorized")
			}
		}
	}
	for _, e := range c.Endpoints {
		claims := &UserClaims{UserID: e.UserID}
		if userAccessForClaims(claims).Disabled {
			return fmt.Errorf("message participant account is disabled")
		}
		if e.External {
			if e.NamedAgentID != "" {
				if _, err := namedAgentByID(ctx, e.UserID, e.NamedAgentID); err != nil {
					return err
				}
			}
			continue
		}
		actor := firstNonEmptyTrimmed(e.Guest, e.UserID)
		if userAccessForClaims(&UserClaims{UserID: actor}).Disabled {
			return fmt.Errorf("message participant account is disabled")
		}
		if e.Kind == triggerCallerWorkflow {
			manifest, found, err := ReadWorkflowManifest(ctx, e.Path)
			if err != nil || !found || manifest == nil || manifest.ID != e.ID {
				return fmt.Errorf("message workflow is unavailable or was replaced")
			}
			if workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
				return fmt.Errorf("message participant no longer has workflow access")
			}
		} else if e.Kind == triggerCallerCrew && e.Profile != codeproduct.ProfileID {
			if api.productSchedules == nil || api.productSchedules.registry == nil {
				return fmt.Errorf("Crew runtime unavailable")
			}
			profile, err := api.productSchedules.registry.Resolve(e.Profile, 0, actor)
			if err != nil || !userAllowedProduct(&UserClaims{UserID: actor}, profile.Product) {
				return fmt.Errorf("message participant no longer has Crew access")
			}
			binding, err := resolveCrewProjectBinding(ctx, actor, profile, e.ID, "")
			if err != nil || binding.OwnerID != e.UserID || canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath)) != canonicalCrewWorkspaceRoot(e.Path) {
				return fmt.Errorf("message Crew is unavailable or ownership changed")
			}
		}
	}
	return nil
}

func agentMessageConnectionClaims(ctx context.Context, id string) (*UserClaims, error) {
	if strings.HasPrefix(id, "oauth-") {
		grant, err := mcpOAuthFamilyActive(ctx, strings.TrimPrefix(id, "oauth-"))
		if err != nil {
			return nil, err
		}
		return accessTokenClaims(mcpOAuthTokenForGrant(grant))
	}
	store, err := openAccessTokens()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	token, err := store.Active(ctx, id, time.Now())
	if err != nil {
		return nil, err
	}
	return accessTokenClaims(token)
}
