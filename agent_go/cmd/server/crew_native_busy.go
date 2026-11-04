package server

import (
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"strings"
)

func (api *StreamingAPI) crewNativeTurnRunning(roots crewroot.Roots) bool {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	for _, s := range api.activeSessions {
		if s == nil {
			continue
		}
		at := canonicalCrewWorkspaceRoot(s.WorkspacePath)
		if at != roots.Virtual && !strings.HasPrefix(at, roots.Virtual+"/") {
			continue
		}
		if s.Status == "running" || s.Status == "paused" || s.HasRunningBackgroundAgents {
			return true
		}
	}
	return false
}

// A per-agent publish must not stop or wait for unrelated Crew conversations.
func (api *StreamingAPI) crewAgentTurnRunning(session string) bool {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	s := api.activeSessions[session]
	return s != nil && (s.Status == "running" || s.Status == "paused" || s.HasRunningBackgroundAgents)
}
