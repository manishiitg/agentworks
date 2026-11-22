package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// crewAgentWhenOffline is the owner's own agent card line for what teammates get while this agent is offline (#3194):
// their agents wait for it, or read its chat from the server's synced copy when it is shown to teammates.
type crewAgentWhenOffline struct {
	Line string `json:"line"`
}

func crewAgentHeaderStatus(t crewTeam, row crewTeamRegisteredAgent) *crewAgentWhenOffline {
	if row.Place != "mac" && row.Kind != "terminal" {
		return nil
	}
	if row.Rule.ChatShare != crewChatShareLive {
		return &crewAgentWhenOffline{Line: "Teammates' asks wait for you"}
	}
	b, ok := crewOfflineRowBinding(t, row)
	if !ok {
		return &crewAgentWhenOffline{Line: "Teammates' asks wait for you · chat not on the server yet"}
	}
	dir, err := crewOfflineDirectory(t.ProjectID, row.ID)
	if err != nil {
		return nil
	}
	h, err := readCrewOfflineHistory(dir, b)
	if err != nil || h.Revision == 0 {
		return &crewAgentWhenOffline{Line: "Teammates' asks wait for you · chat not on the server yet"}
	}
	if h.Offset != h.SourceSize {
		return &crewAgentWhenOffline{Line: fmt.Sprintf("Teammates can wait or read this chat · copying %d%%", h.Offset*100/max(h.SourceSize, 1))}
	}
	line := "Teammates can wait or read this chat"
	if info, err := os.Stat(filepath.Join(dir, "history.json")); err == nil {
		line += fmt.Sprintf(" · synced %dm ago", int(time.Since(info.ModTime()).Minutes()))
	}
	return &crewAgentWhenOffline{Line: line}
}
