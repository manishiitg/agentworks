package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// crewAgentWhenOffline is the owner's own agent card line for what teammates get while this agent is offline (#3194):
// their agents wait for it, and read its chat from the server's synced copy (any time) when it is shown to teammates.
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
		if h.SourceSize > 0 {
			return &crewAgentWhenOffline{Line: fmt.Sprintf("Teammates' agents can read this chat · copying %d%% of %s", h.Offset*100/h.SourceSize, formatCrewSyncBytes(h.SourceSize))}
		}
		return &crewAgentWhenOffline{Line: "Teammates' agents can read this chat · copying 0%"}
	}
	line := "Teammates' agents can read this chat"
	size := h.SourceSize
	if info, err := os.Stat(filepath.Join(dir, "history.json")); err == nil {
		if size <= 0 {
			size = info.Size()
		}
		if size > 0 {
			line += " · " + formatCrewSyncBytes(size)
		}
		line += fmt.Sprintf(" · synced %dm ago", int(time.Since(info.ModTime()).Minutes()))
	} else if size > 0 {
		line += " · " + formatCrewSyncBytes(size)
	}
	return &crewAgentWhenOffline{Line: line}
}

func formatCrewSyncBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
