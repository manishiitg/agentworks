package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// externalMissingScopeHint says which scopes would let this connection call a
// tool it was refused, so the person knows what to add when reconnecting. It
// asks the same permission check, with scopes added: first a single scope,
// then the smallest set that works (a Brain save needs both read and write).
// Only scopes the account could hold are named. Empty when nothing helps (a
// role or Crew/workflow bound is what refuses it).
func externalMissingScopeHint(claims *UserClaims, tool externalTool) string {
	if claims == nil || claims.AccessToken == nil {
		return ""
	}
	allowsWith := func(extra []string) bool {
		widened := *claims
		token := *claims.AccessToken
		token.Scopes = append(append([]string{}, token.Scopes...), extra...)
		widened.AccessToken = &token
		return externalTokenAllows(&widened, tool)
	}
	eligible := []string{}
	for _, scope := range accesstokens.Scopes {
		if claims.AccessToken.Allows(scope) || len(mcpOAuthScopesFor(claims, []string{scope})) == 0 {
			continue
		}
		eligible = append(eligible, scope)
	}
	singles := []string{}
	for _, scope := range eligible {
		if allowsWith([]string{scope}) {
			singles = append(singles, scope)
		}
	}
	if len(singles) > 0 {
		return " It needs the scope " + strings.Join(singles, " or ") + "; reconnect with it added (agentworks login --scopes ...)."
	}
	if !allowsWith(eligible) {
		return ""
	}
	need := append([]string{}, eligible...)
	for i := 0; i < len(need); {
		without := append(append([]string{}, need[:i]...), need[i+1:]...)
		if allowsWith(without) {
			need = without
			continue
		}
		i++
	}
	return " It needs the scopes " + strings.Join(need, " and ") + "; reconnect with them added (agentworks login --scopes ...)."
}
