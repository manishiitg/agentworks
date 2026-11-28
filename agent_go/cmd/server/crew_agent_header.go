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
		return &crewAgentWhenOffline{Line: "When your Mac is off, questions wait until it's back."}
	}
	const copy = "When your Mac is off, teammates' agents read this chat's copy"
	b, ok := crewOfflineRowBinding(t, row)
	if !ok {
		return &crewAgentWhenOffline{Line: copy + " (not on the server yet)."}
	}
	dir, err := crewOfflineDirectory(t.ProjectID, row.ID)
	if err != nil {
		return nil
	}
	h, err := readCrewOfflineHistory(dir, b)
	if err != nil || h.Revision == 0 {
		return &crewAgentWhenOffline{Line: copy + " (not on the server yet)."}
	}
	if h.Offset != h.SourceSize && h.SourceSize > 0 {
		return &crewAgentWhenOffline{Line: fmt.Sprintf("%s (copying, %d%% done).", copy, h.Offset*100/h.SourceSize)}
	}
	if info, err := os.Stat(filepath.Join(dir, "history.json")); err == nil {
		return &crewAgentWhenOffline{Line: fmt.Sprintf("%s (synced %s).", copy, crewSyncedAgo(time.Since(info.ModTime())))}
	}
	return &crewAgentWhenOffline{Line: copy + "."}
}

// crewSyncedAgo says how long ago in plain words: "just now", "5 min ago", "3 h ago".
func crewSyncedAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
}
