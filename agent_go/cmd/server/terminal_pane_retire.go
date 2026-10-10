package server

import (
	"strings"
	"time"
)

// retireMissingTerminalPanes marks the session's terminals whose tmux session no longer exists as closed, right now,
// instead of waiting for the watchdog's confirmation checks. It leaves a session with a turn in flight alone (a pane can
// vanish and be replaced by the turn's own retry) and a pane that still exists. It returns how many it retired.
func (api *StreamingAPI) retireMissingTerminalPanes(sessionID string) int {
	sessionID = strings.TrimSpace(sessionID)
	if api == nil || api.terminalStore == nil || sessionID == "" || api.hasActiveTurnCancel(sessionID) {
		return 0
	}
	retired := 0
	for _, snap := range api.terminalStore.ListRaw(sessionID) {
		tmux := strings.TrimSpace(snap.TmuxSession)
		if tmux == "" || !terminalSnapshotHasLiveTmux(snap) || inspectCodingTmuxPaneState(tmux) != codingTmuxPaneMissing {
			continue
		}
		const reason = "tmux pane is gone"
		if snap.Active {
			api.terminalStore.MarkFailed(snap.TerminalID)
		}
		api.terminalStore.MarkProcessClosed(snap.TerminalID, reason)
		if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
			registry.MarkClosed(tmux, reason, time.Now())
		}
		retired++
	}
	return retired
}
