package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
)

// Resolve authority again immediately before a queued delivery enters a turn.
// Persist only the connection ID, never its bearer token. Own named-agent mail
// follows current account access across CLI sign-in replacement; narrowed keys,
// other people and project callers retain their original grant checks.
func (api *StreamingAPI) recheckAgentMessageAdmission(ctx context.Context, c agentConversation, m agentMessage) error {
	source, dest := c.Endpoints[m.From], c.Endpoints[1-m.From]
	if err := api.validateRelayMessage(ctx, source, m); err != nil {
		return err
	}
	if source.ConditionJob != "" {
		if api.productSchedules == nil {
			return fmt.Errorf("watcher caller unavailable")
		}
		job, err := api.productSchedules.Job(ctx, source.UserID, source.ConditionJob)
		if err != nil {
			if strings.HasPrefix(err.Error(), "product schedule ") && strings.HasSuffix(err.Error(), "not found for this user") {
				return err
			}
			return conditiontrigger.HoldError{Err: err}
		}
		if job.Schedule.Condition == nil {
			return fmt.Errorf("watcher caller was removed")
		}
		if !job.Effective().Enabled {
			// Automatic completion stops future checks, not the exact action
			// already accepted by this one-time watcher. All access checks below
			// still apply; a manual pause before acceptance still holds delivery.
			l, err := api.productSchedules.conditionService().engine.Read(ctx, conditionKey(job))
			if err != nil {
				return conditiontrigger.HoldError{Err: err}
			}
			cfg := job.Schedule.Condition
			if !cfg.Once || l.OnceCompletedAt.IsZero() || l.OnceRevision != cfg.Revision() || m.Submission != conditiontrigger.Submission(conditionKey(job), l.OnceEventID) {
				return conditiontrigger.ErrPaused
			}
		}
		if err := api.productSchedules.conditionService().authorize(ctx, job, *job.Schedule.Condition); err != nil {
			var permanent conditiontrigger.PermanentError
			if errors.As(err, &permanent) {
				return err
			}
			return conditiontrigger.HoldError{Err: err}
		}
	}
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
		if err != nil && source.External && source.NamedAgentID != "" {
			if current, e := currentRelaySenderClaims(ctx, source, m.SenderConnectionID); e == nil {
				claims, err = current, nil
			}
		}
		if err != nil && source.External && source.NamedAgentID != "" && source.UserID == dest.UserID && strings.HasPrefix(m.SenderConnectionID, "oauth-") {
			// The historical CLI grant proves provenance only. It is never made
			// live again and gives no authority to a different account or agent.
			store, e := openMCPOAuthStore()
			if e == nil {
				var user, client string
				e = store.db.QueryRowContext(ctx, `SELECT user_id,client_id FROM tokens WHERE family_id=? LIMIT 1`, strings.TrimPrefix(m.SenderConnectionID, "oauth-")).Scan(&user, &client)
				store.Close()
				if e == nil && user == source.UserID && client == cliOAuthClientID {
					person := &UserClaims{UserID: source.UserID}
					access := userAccessForClaims(person)
					if !access.Disabled && (IsMultiUserMode() && access.Known || !IsMultiUserMode() && user == GetDefaultUserID()) {
						claims = person
						err = nil
					}
				}
			}
		}
		if err != nil || claims.UserID != source.UserID {
			return fmt.Errorf("sending connection is no longer authorized")
		}
		if source.NamedAgentID != "" {
			agent, err := namedAgentByID(ctx, source.UserID, source.NamedAgentID)
			if err != nil || !namedAgentConnectionAllowed(agent, claims) {
				return fmt.Errorf("sending named agent is no longer authorized")
			}
		}
		view := hostedInboxReplyClaims(claims, c, m.From)
		if err := api.externalAuthorizeMessagePeer(context.WithValue(ctx, UserContextKey, claims), view, dest, true); err != nil {
			return err
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
			if strings.Contains(e.ChatKey, codeChatSideMarker) && !api.authorizeGoalMessageTab(context.WithValue(ctx, UserContextKey, claims), e) {
				return fmt.Errorf("project chat was closed or ownership changed")
			}
			manifest, found, err := ReadWorkflowManifest(ctx, e.Path)
			if err != nil || !found || manifest == nil || manifest.ID != e.ID {
				return fmt.Errorf("message workflow is unavailable or was replaced")
			}
			if workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
				return fmt.Errorf("message participant no longer has workflow access")
			}
		} else if e.Kind == triggerCallerCrew {
			if api.productSchedules == nil || api.productSchedules.registry == nil {
				return fmt.Errorf("Crew runtime unavailable")
			}
			profile, err := api.productSchedules.registry.Resolve(e.Profile, 0, actor)
			if err != nil || !userAllowedProduct(&UserClaims{UserID: actor}, profile.Product) {
				return fmt.Errorf("message participant no longer has Crew access")
			}
			binding, err := resolveCrewProjectBinding(ctx, actor, profile, e.ID, "")
			if err != nil || (binding.OwnerID != e.UserID && !(binding.OwnedByCaller && isSideChatConversationKey(e.ChatKey) && crewIsCoOwner(binding.Binding.WorkspacePath, e.UserID))) || canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath)) != canonicalCrewWorkspaceRoot(e.Path) {
				return fmt.Errorf("message Crew is unavailable or ownership changed")
			}
		}
	}
	return nil
}

func agentMessageConnectionClaims(ctx context.Context, id string) (*UserClaims, error) {
	if strings.HasPrefix(id, "oauth-") {
		// A saved message or already-authenticated stream follows the sign-in,
		// not the short-lived access token used when it started. Incoming HTTP
		// still authenticates its bearer separately. LiveFamily rechecks the
		// parent/binding, revocation, refresh expiry and current account access.
		store, err := openMCPOAuthStore()
		if err != nil {
			return nil, err
		}
		defer store.Close()
		grant, err := store.LiveFamily(ctx, strings.TrimPrefix(id, "oauth-"))
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
