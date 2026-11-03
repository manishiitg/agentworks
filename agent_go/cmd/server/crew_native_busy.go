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
