package server

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// turnDiagnostics adds a Muse turn's content-free state (numbers and fixed words) to its still_running heartbeat:
// tmux reports its transcript cursor; MSP has no tmux pool. The heartbeat sweep runs
// on its own goroutine, so the request map is read under its lock.
func (api *StreamingAPI) turnDiagnostics(sessionID string) string {
	api.lastQueryMu.RLock()
	req, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if !ok || strings.TrimSpace(req.Provider) != "muse-cli" {
		return ""
	}
	if musecli.MuseMSPSelected() {
		return "transport=msp tmux_pool=not_applicable"
	}
	return musecli.RetainedTurnDiagnostics(sessionID)
}
