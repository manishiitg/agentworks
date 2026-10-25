package server

import "strings"

// One user, one chat (docs/design/bot_identity_model.md): the same
// conversation takes turns from its owner's web chat, WhatsApp and Slack DMs.
// A bot turn marks the live session (BotPlatform, a "bot:" trigger, the Slack
// execution binding) and trackActiveSession keeps marks a later turn does not
// send — so without this, the owner's next web turn would run as a bot turn
// (bot-origin tools) and every tool call would fail the origin guard.
//
// clearBotOriginForOwnerTurn ends the previous bot turn's marks when the
// session's own owner sends a plain interactive turn. Scheduled, child,
// notification and bot turns, and anyone other than the owner, keep them.
func (api *StreamingAPI) clearBotOriginForOwnerTurn(sessionID string, claims *UserClaims, req QueryRequest) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return
	}
	switch claims.Provider {
	case "bot_route", "bot_owner", slackDMProvider:
		return
	}
	if claims.ExecutionPrincipal != nil || claims.BotRouteGrant != "" || req.BotPlatform != "" || strings.TrimSpace(req.TriggeredBy) != "" ||
		req.ParentSessionID != "" || req.SessionKind != "" || req.IsAutoNotification {
		return
	}
	api.activeSessionsMux.Lock()
	active := api.activeSessions[sessionID]
	cleared := false
	if active != nil && active.UserID == claims.UserID && active.ParentSessionID == "" && active.SessionKind == "" &&
		(active.BotPlatform != "" || strings.HasPrefix(active.TriggeredBy, "bot:")) {
		active.BotPlatform = ""
		active.TriggeredBy = ""
		cleared = true
	}
	api.activeSessionsMux.Unlock()
	if cleared {
		api.botExecutionSessions.Delete(sessionID)
	}
}
