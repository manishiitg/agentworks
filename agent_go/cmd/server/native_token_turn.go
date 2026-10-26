package server

import (
	"context"
	"fmt"
	"log"
)

// Bind the initiating connection and concrete project, never model arguments.
// Tools refresh their own claims; this check protects the final transcript save.
func (api *StreamingAPI) nativeTokenTurnCheck(ctx context.Context, sessionID string, req QueryRequest) (func(context.Context) error, error) {
	check := func(context.Context) error { return nil }
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.AccessToken == nil {
		return check, nil
	}
	// Keep only the target, rather than retaining the request's secret inputs.
	target := QueryRequest{AgentProfileID: req.AgentProfileID, AgentProfileVersion: req.AgentProfileVersion,
		AgentProfileConversationKey: req.AgentProfileConversationKey, SelectedFolder: req.SelectedFolder,
		AgentMode: req.AgentMode, PhaseID: req.PhaseID, PresetQueryID: req.PresetQueryID}
	access, err := api.conversationTargetAccess(ctx, target)
	if err != nil || access == WorkflowAccessNone {
		return nil, fmt.Errorf("project is no longer shared with this connection")
	}
	tokenID := claims.AccessToken.ID
	userID := claims.UserID
	check = func(ctx context.Context) error {
		fresh, err := activeExternalGrantClaims(ctx, tokenID)
		if err == nil && fresh.UserID == userID {
			level, targetErr := api.conversationTargetAccess(context.WithValue(ctx, UserContextKey, fresh), target)
			if targetErr == nil && level != WorkflowAccessNone {
				return nil
			}
		}
		log.Printf("[TOKEN_TURN] denied session=%s token=%s reason=connection_or_project_access_changed", sessionID, tokenID)
		return fmt.Errorf("connection or project access changed; reconnect or ask the owner for access")
	}
	return check, check(ctx)
}
