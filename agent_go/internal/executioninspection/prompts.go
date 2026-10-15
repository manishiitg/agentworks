package executioninspection

import (
	"fmt"
	"sort"
	"strings"

	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

type PromptEvent struct {
	Event       orchevents.BackgroundAgentTranscriptEvent
	ExecutionID string
}

type PromptVersion struct {
	Text         string `json:"text"`
	Label        string `json:"label"`
	SessionLabel string `json:"session_label"`
	SessionCount int    `json:"session_count"`
}

type PromptHistory struct {
	Versions     []PromptVersion `json:"versions"`
	CurrentIndex int             `json:"current_index"`
}

// PromptVersions counts exact recorded handles, never task IDs, prompt changes,
// timestamps or today's mutable attempt files. Late markers join by owner+turn.
func PromptVersions(recorded []PromptEvent) PromptHistory {
	events := append([]PromptEvent(nil), recorded...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].Event.Timestamp.Before(events[j].Event.Timestamp) })
	sessions := map[string]map[string]bool{}
	key := func(e PromptEvent) string { return e.ExecutionID + "\x00" + e.Event.TurnID }
	for _, e := range events {
		if e.Event.Type == "runtime_session" && e.Event.TurnID != "" && e.Event.RuntimeSessionID != "" {
			if sessions[key(e)] == nil {
				sessions[key(e)] = map[string]bool{}
			}
			sessions[key(e)][e.Event.RuntimeSessionID] = true
		}
	}
	history := PromptHistory{Versions: []PromptVersion{}, CurrentIndex: -1}
	byText := map[string]int{}
	known := []map[string]bool{}
	unknown := []bool{}
	newSession := []bool{}
	previousSessions := map[string]bool{}
	for _, e := range events {
		if e.Event.Role != "system" || strings.TrimSpace(e.Event.Text) == "" {
			continue
		}
		ids := sessions[key(e)]
		if e.Event.TurnID == "" {
			ids = nil
		}
		if e.Event.RuntimeSessionID != "" {
			ids = map[string]bool{e.Event.RuntimeSessionID: true}
		}
		index, exists := byText[e.Event.Text]
		if !exists {
			index = len(history.Versions)
			byText[e.Event.Text] = index
			history.Versions = append(history.Versions, PromptVersion{Text: e.Event.Text})
			known = append(known, map[string]bool{})
			unknown = append(unknown, false)
			disjoint := len(ids) > 0 && len(previousSessions) > 0
			for id := range ids {
				if previousSessions[id] {
					disjoint = false
				}
			}
			newSession = append(newSession, disjoint)
		}
		if len(ids) == 0 {
			unknown[index] = true
		}
		for id := range ids {
			known[index][id] = true
		}
		history.CurrentIndex = index
		// Missing evidence breaks the causal chain; never infer a restart through it.
		previousSessions = ids
	}
	for i := range history.Versions {
		v := &history.Versions[i]
		v.Label = fmt.Sprintf("Version %d of %d", i+1, len(history.Versions))
		if newSession[i] {
			v.Label += " · new session"
		}
		v.SessionCount = len(known[i])
		switch {
		case v.SessionCount == 0:
			v.SessionLabel = "Session not recorded"
		case v.SessionCount == 1:
			v.SessionLabel = "Used in 1 session"
		default:
			v.SessionLabel = fmt.Sprintf("Used in %d sessions", v.SessionCount)
		}
		if unknown[i] && v.SessionCount > 0 {
			v.SessionLabel += " · some sessions not recorded"
		}
	}
	return history
}
