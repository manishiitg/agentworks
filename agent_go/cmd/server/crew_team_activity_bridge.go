package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Captured with the committed bridge message, never reconstructed from later sharing.
// Old/private messages are not backfilled when an agent is subsequently shared.
type crewTeamActivityStamp struct {
	Project  string    `json:"project"`
	Agents   [2]string `json:"agents"`
	Bindings [2]string `json:"bindings"`
}
type crewTeamBridgeEvent struct {
	ThreadID string `json:"thread_id"`
	CanOpen  bool   `json:"can_open"`
}

func crewTeamActivityBinding(row crewTeamRegisteredAgent) string {
	values := []string{row.OwnerID, row.ChatKey}
	if row.Terminal != nil {
		values = append(values, row.Terminal.NamedAgentID, row.Terminal.Provider, row.Terminal.MacPair)
	}
	return strings.Join(values, "\x00")
}

// Resolve only a genuine registered agent endpoint, not a person, guest, workflow,
// published Run thread or an unrelated Crew that happens to use the same name.
func crewTeamActivityEndpoint(t crewTeam, e agentMessageEndpoint) (crewTeamRegisteredAgent, bool) {
	if e.ConditionJob != "" || e.Guest != "" || e.ShareID != "" || e.OfflineCopyTeam != "" || e.PublishedAgent != "" {
		return crewTeamRegisteredAgent{}, false
	}
	for _, row := range t.Agents {
		if row.OwnerID != e.UserID || !row.InTeam || !t.member(row.OwnerID) || !crewTeamAccountActive(row.OwnerID) {
			continue
		}
		if e.TeamAgentID != "" && row.ID != e.TeamAgentID {
			continue
		}
		if e.NamedAgentID != "" {
			if row.Kind == "terminal" && row.Terminal != nil && row.Terminal.NamedAgentID == e.NamedAgentID {
				return row, true
			}
			continue
		}
		if e.Kind != triggerCallerCrew || e.Profile != crewProfileID || e.External || e.ID != t.ProjectID {
			continue
		}
		if e.ChatKey == row.ChatKey || crewRelayTwin(t.ProjectID) && row.OwnerID == t.OwnerID && e.ChatKey == macSharedAgentChatKey(t.ProjectID, row.Tab) {
			return row, true
		}
	}
	return crewTeamRegisteredAgent{}, false
}

// Called under the existing mailbox write lock. This reads registry metadata only:
// no named-mailbox lookup or Mac round trip, and no nested mailbox lock.
func crewTeamActivityStampFor(c agentConversation) *crewTeamActivityStamp {
	var t crewTeam
	var err error
	for _, e := range c.Endpoints {
		if e.Kind == triggerCallerCrew && e.ID != "" {
			t, err = readCrewTeam(e.ID)
			break
		}
		if e.NamedAgentID != "" {
			var joined bool
			t, _, joined, err = terminalCrewForNamed(e.NamedAgentID)
			if joined || err != nil {
				break
			}
		}
	}
	if err != nil || !t.On || t.RosterSource != "" || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return nil
	}
	a, ok := crewTeamActivityEndpoint(t, c.Endpoints[0])
	if !ok {
		return nil
	}
	b, ok := crewTeamActivityEndpoint(t, c.Endpoints[1])
	if !ok || a.ID == b.ID {
		return nil
	}
	return &crewTeamActivityStamp{Project: t.ProjectID, Agents: [2]string{a.ID, b.ID}, Bindings: [2]string{crewTeamActivityBinding(a), crewTeamActivityBinding(b)}}
}

func crewTeamActivityBridgeRows(ctx context.Context, t crewTeam, caller string, c agentConversation, m agentMessage, transport map[string]bool) ([2]crewTeamRegisteredAgent, bool) {
	var rows [2]crewTeamRegisteredAgent
	stamp := m.TeamActivity
	if stamp == nil || stamp.Project != t.ProjectID || !crewTeamReachOpen(t, caller) || m.From < 0 || m.From > 1 || m.Kind != "" || m.SenderNonOwnerTurn || m.SenderWebsiteSession != "" {
		return rows, false
	}
	for i, e := range c.Endpoints {
		row, ok := crewTeamActivityEndpoint(t, e)
		if !ok || row.ID != stamp.Agents[i] || crewTeamActivityBinding(row) != stamp.Bindings[i] || !crewTeamSourceRowAdmits(t, caller, row) {
			return rows, false
		}
		if row.Kind == "terminal" {
			valid, known := transport[row.ID]
			if !known {
				_, err := currentCrewTeamTerminal(ctx, t, row)
				valid = err == nil
				transport[row.ID] = valid
			}
			if !valid {
				return rows, false
			}
		}
		rows[i] = row
	}
	return rows, rows[0].ID != rows[1].ID
}

func crewTeamBridgeActivity(ctx context.Context, t crewTeam, caller string, store agentMessageStore) []crewTeamFeedItem {
	out := []crewTeamFeedItem{}
	labels := crewTeamPersonLabels(t)
	transport := map[string]bool{}
	for _, c := range store.Conversations {
		for _, m := range c.Messages {
			rows, ok := crewTeamActivityBridgeRows(ctx, t, caller, c, m, transport)
			if !ok {
				continue
			}
			from, to := rows[m.From], rows[1-m.From]
			line := []rune(strings.Join(strings.Fields(m.Text), " "))
			if len(line) > 60 {
				line = append(line[:60], '…')
			}
			out = append(out, crewTeamFeedItem{ActorID: from.OwnerID, ID: "message:" + m.ID, Kind: "message", At: m.SentAt, Person: labels[from.OwnerID], Title: firstNonEmptyTrimmed(from.Name, from.Tab) + " → " + firstNonEmptyTrimmed(to.Name, to.Tab), Line: string(line), Bridge: &crewTeamBridgeEvent{ThreadID: c.ID, CanOpen: caller == from.OwnerID || caller == to.OwnerID}})
		}
	}
	return out
}

// The only new server-side state is each person's monotone seen watermark.
type crewTeamSeenMark struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func crewTeamSeenMarks(project string) (map[string]crewTeamSeenMark, error) {
	marks := map[string]crewTeamSeenMark{}
	dir, err := crewTeamDir(project)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "activity-seen.json"))
	if os.IsNotExist(err) {
		return marks, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &marks)
	if marks == nil {
		marks = map[string]crewTeamSeenMark{}
	}
	return marks, err
}
func crewTeamAfterSeen(row crewTeamFeedItem, mark crewTeamSeenMark) bool {
	return row.At.After(mark.At) || row.At.Equal(mark.At) && row.ID > mark.ID
}
