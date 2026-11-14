package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// A join has a time, not a new sharing mode. Each first-discovered agent gets
// an ordinary In team choice: pre-join/unknown stays private; post-join follows
// the normal new-agent default. Existing registry choices always win.
func crewTeamAgentJoinedAfter(joined, created, firstHistory string) bool {
	cutoff, err := time.Parse(time.RFC3339Nano, joined)
	if err != nil {
		return false
	}
	var first time.Time
	for _, raw := range []string{created, firstHistory} {
		if at, e := time.Parse(time.RFC3339Nano, raw); e == nil && (first.IsZero() || at.Before(first)) {
			first = at
		}
	}
	return !first.IsZero() && first.After(cutoff)
}

// Only the local project's owner supplies this metadata, from that project's
// registry and private history index. No history text crosses the Mac boundary.
func crewTeamJoinAgentDefaults(user, project, workspace string) func(ProductConversationRecord) *bool {
	state := readCrewTeamState(project)
	if !IsLocalMode() || ownerOfCrewProjectID(project) != user || state.MemberOf == "" {
		return func(ProductConversationRecord) *bool { return nil }
	}
	firstHistory := map[string]string{}
	if roots, found, err := crewStorageRoots(workspace); err == nil && found {
		raw, e := os.ReadFile(filepath.Join(roots.Private, "builder", "conversation", chatHistoryIndexFileName))
		if e == nil {
			var index chatHistoryIndex
			if json.Unmarshal(raw, &index) == nil {
				for _, entry := range index.Entries {
					s := entry.Session
					if s.UserID != "" && s.UserID != user {
						continue
					}
					at, e := time.Parse(time.RFC3339Nano, s.CreatedAt)
					if e != nil {
						continue
					}
					previous, e := time.Parse(time.RFC3339Nano, firstHistory[s.SessionID])
					if e != nil || at.Before(previous) {
						firstHistory[s.SessionID] = s.CreatedAt
					}
				}
			}
		}
	}
	return func(record ProductConversationRecord) *bool {
		inTeam := crewTeamAgentJoinedAfter(state.JoinedAt, record.CreatedAt, firstHistory[record.SessionID])
		return &inTeam
	}
}
