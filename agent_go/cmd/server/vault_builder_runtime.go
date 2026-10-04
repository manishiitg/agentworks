package server

import (
	"context"
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/mcpagent/executor"
)

// Set only by the authenticated, live-revalidated tool binding. Never accepted
// from tool arguments, browser headers, or a product MCP HTTP request.
type vaultBuilderKey struct{}
type vaultBuilderAuthority struct{ Person, Session string }

func vaultBuilderAdministrator(person string) bool {
	claims := &UserClaims{UserID: person}
	access := userAccessForClaims(claims)
	return activeMCPPerson(person) && access.Admin && userAllowedProduct(claims, "mcp-gateway")
}

func (api *StreamingAPI) vaultBuilderAuthority(ctx context.Context, person string) (vaultBuilderAuthority, bool, error) {
	authority, present := ctx.Value(vaultBuilderKey{}).(vaultBuilderAuthority)
	if !present {
		return authority, false, nil
	}
	if authority.Person != person || authority.Session == "" || executor.SessionIDFromContext(ctx) != authority.Session || api.mcpSessionPerson(authority.Session) != person || !vaultBuilderAdministrator(person) {
		return authority, true, errors.New("Vault builder administrator or session access changed")
	}
	return authority, true, nil
}

func vaultBuilderQuery(req QueryRequest, claims *UserClaims, authoritySession, toolSession string, readOnly bool) bool {
	return req.AgentProfileID == caplayerproduct.ProfileID && req.AgentMode == "multi-agent" && req.TriggeredBy == "" && req.BotPlatform == "" && req.SelectedFolder == caplayerproduct.WorkspaceRoot && !readOnly && authoritySession == toolSession && claims != nil && claims.AccessToken == nil && claims.ExecutionPrincipal == nil && claims.ExternalBuilderOperationID == "" && claims.Scope == "" && claims.BotRouteGrant == "" && claims.Provider != "bot_owner" && claims.Provider != "bot_route" && claims.Provider != slackDMProvider
}
