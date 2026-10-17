package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Runtime callers need the current account's full identity: older product access
// files may be keyed by username or email rather than its immutable user ID.
func currentProjectCallerClaims(ctx context.Context, userID string) *UserClaims {
	if !IsMultiUserMode() {
		if claims := GetUserFromContext(ctx); claims != nil && claims.UserID == userID {
			return claims
		}
		return &UserClaims{UserID: userID}
	}
	claims, err := accessTokenClaims(accesstokens.Token{UserID: userID})
	if err != nil {
		return nil
	}
	claims.AccessToken = nil
	return claims
}

func currentProductCallerHasAccess(ctx context.Context, userID, root string) bool {
	claims := currentProjectCallerClaims(ctx, userID)
	ref, ok := resolveCrewPath(ctx, userID, root)
	return ok && crewAccessFor(claims, ref) != crewAccessNone
}
