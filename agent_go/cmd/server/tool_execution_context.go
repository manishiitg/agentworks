package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// bindToolExecutionContext captures the authenticated query's identity, not
// model arguments or a fallback local user. Every platform tool registered on
// the query's definition receives these claims, regardless of its transport.
// Action-specific authorization remains in the tool/UI handler.
func (api *StreamingAPI) bindToolExecutionContext(requestCtx context.Context, session string, req QueryRequest, readOnly bool) func(context.Context, string) (context.Context, error) {
	return api.bindToolExecutionContextForSession(requestCtx, session, session, req, readOnly)
}

// bindToolExecutionContextForSession separates the durable authority session
// from the session that actually invokes tools. Root agents use the same value
// for both. Delegated/background agents use an isolated tool session while
// retaining the parent's authenticated owner and revocation checks.
func (api *StreamingAPI) bindToolExecutionContextForSession(requestCtx context.Context, authoritySession, toolSession string, req QueryRequest, readOnly bool) func(context.Context, string) (context.Context, error) {
	var bound *UserClaims
	if claims := GetUserFromContext(requestCtx); claims != nil {
		copy := *claims
		if claims.ExecutionPrincipal != nil {
			principal := *claims.ExecutionPrincipal
			copy.ExecutionPrincipal = &principal
		}
		bound = &copy
	}
	return func(ctx context.Context, tool string) (context.Context, error) {
		if bound == nil || strings.TrimSpace(bound.UserID) == "" || strings.TrimSpace(authoritySession) == "" || strings.TrimSpace(toolSession) == "" {
			return nil, fmt.Errorf("%s requires an authenticated session", tool)
		}
		if accessTokenRunToolDenied(bound, tool) {
			return nil, fmt.Errorf("%s is unavailable to external access tokens", tool)
		}
		callerSession := executor.SessionIDFromContext(ctx)
		if callerSession == "" {
			callerSession, _ = ctx.Value(common.ChatSessionIDKey).(string)
		}
		// Workflow steps use registered child/group MCP sessions for their
		// session-scoped HTTP bridge. They are owned by the authenticated parent
		// HTTP run and must retain that run's bound identity. Do not admit a
		// merely similar-looking session ID: only the live registry relationship
		// established by RegisterHTTPSession is authoritative.
		callerOwnedBySession := toolSession == authoritySession && callerSession != "" &&
			mcpclient.GetSessionRegistry().HTTPSessionForMCPSession(callerSession) == authoritySession
		if callerSession != "" && callerSession != toolSession && !callerOwnedBySession {
			return nil, fmt.Errorf("%s caller does not own this tool session", tool)
		}
		if claims := GetUserFromContext(ctx); claims != nil && (claims.UserID != bound.UserID || claims.Provider != bound.Provider || claims.BotRouteGrant != bound.BotRouteGrant) {
			return nil, fmt.Errorf("%s caller identity conflicts with its authenticated session", tool)
		}
		if userID, _ := ctx.Value(common.UserIDKey).(string); userID != "" && userID != bound.UserID {
			return nil, fmt.Errorf("%s caller identity conflicts with its authenticated session", tool)
		}
		if api.eventStore != nil {
			if owner := api.eventStore.GetSessionOwner(authoritySession); owner != "" && owner != bound.UserID {
				return nil, fmt.Errorf("%s session ownership changed; start a new turn", tool)
			}
		}
		// bot_route (Slack channel grants, revalidated below) and bot_owner
		// (WhatsApp turns as the paired owner, authenticated at message
		// ingress) are the two principals a bot-marked session may execute
		// under. Anything else bound here means the session changed origin
		// underneath its tools.
		if bound.Provider != "bot_route" && bound.Provider != "bot_owner" && bound.Provider != slackDMProvider {
			if _, bot := api.botExecutionForSession(authoritySession); bot {
				return nil, fmt.Errorf("%s session origin changed; start a new turn", tool)
			}
			// A bot-marked session whose current turn is the owner's own
			// Slack DM or WhatsApp message keeps the tools its CLI was
			// launched with: the owner check above holds, and one person has
			// one chat (senderProfileTurn), so a DM continues a warm CLI a
			// web turn started. Only a shared channel route may not run on a
			// person's tools. Refusing every bot-marked session broke all
			// tools of a Crew's main chat when a Slack DM followed a web turn
			// (RTS 2026-09-28).
			if active, _ := api.getActiveSession(authoritySession); active != nil && (active.BotPlatform != "" || strings.HasPrefix(active.TriggeredBy, "bot:")) && !ownersOwnBotTurn(active.TurnProvider) {
				return nil, fmt.Errorf("%s session origin changed; start a new turn", tool)
			}
		}
		copy := *bound
		ctx = context.WithValue(ctx, UserContextKey, &copy)
		ctx = context.WithValue(ctx, common.UserIDKey, copy.UserID)
		ctx = executor.WithSessionID(ctx, toolSession)
		if copy.Provider == "bot_route" || copy.Provider == slackDMProvider {
			validated, err := api.revalidateExecutionPrincipal(ctx, req)
			if err != nil {
				return nil, err
			}
			ctx = validated
		} else if access := userAccessForClaims(&copy); access.Disabled || directoryUserIsUnknown(&copy) {
			return nil, fmt.Errorf("%s account is unavailable", tool)
		}
		access, err := api.conversationTargetAccess(ctx, req)
		if err != nil {
			return nil, err
		}
		if access == WorkflowAccessNone || (!readOnly && access == WorkflowAccessRead) {
			return nil, fmt.Errorf("%s session permissions changed; start a new turn", tool)
		}
		return ctx, nil
	}
}

// ownersOwnBotTurn reports a bot turn that runs as the session's own person:
// their 1:1 Slack DM or their WhatsApp. A channel route, or a turn whose
// principal is unknown, is not.
func ownersOwnBotTurn(provider string) bool {
	return provider == slackDMProvider || provider == "bot_owner"
}
