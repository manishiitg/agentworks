package server

import (
	"context"
	"errors"
	"strings"
)

// Keep shared notification claiming, queuing and retrying, but enter Pulse
// through its own normal turn bootstrap. Reusing a stored Builder wrapper
// directly would bypass Pulse's current permissions and conversation log.
func (api *StreamingAPI) executePulseResultTurn(sessionID, message string, onComplete func(error)) bool {
	api.lastQueryMu.RLock()
	req, found := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	workspacePath := strings.TrimSpace(req.SelectedFolder)
	if !found || workspacePath == "" || api.conversationTurnOccupied(sessionID) || api.autoNotificationSessionUnreachable(sessionID) {
		return false
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), goalLeadTurnHardCap)
		defer cancel()
		_, _, err := api.runGoalLeadTurn(ctx, workspacePath, goalLeadTurn{
			Kind: goalLeadTurnFunctionResult, Body: message, ExpectedSessionID: sessionID,
		})
		// Disabled, stopped or rotated conversations retain the saved result
		// without reviving automatic work or retrying forever.
		if errors.Is(err, errPulseResultIneligible) {
			err = nil
		}
		if onComplete != nil {
			onComplete(err)
		}
		if err == nil && !api.autoNotificationSessionUnreachable(sessionID) {
			api.drainPendingAutoNotificationsAfterTurn(sessionID)
		}
	}()
	return true
}
