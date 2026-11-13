package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-926: cached Mac metadata must not bypass a current Only-me/Hidden
// choice; tool output and index rewrites must not become message previews.
func TestCrewTeamLastMessageRequiresLiveSharedChat(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"other": {"live": true, "hidden": true, "private": false, "unknown": true}})}
	preview := crewTeamMessagePreview{LastMessage: "Visible update", LastMessageAt: time.Now().UTC().Format(time.RFC3339Nano)}
	person := crewTeamAgentsPerson{UserID: "other", Name: "Colleague"}
	for id, agent := range team.Agents {
		switch agent.Tab {
		case "hidden":
			agent.Rule.ChatShare = crewChatShareAgent
		case "unknown":
			agent.Rule.ChatShare = "unknown"
		}
		team.Agents[id] = agent
		person.Agents = append(person.Agents, crewTeamAgent{ID: id, Tab: agent.Tab, ChatKey: agent.ChatKey, Name: agent.Tab, Place: "mac", InTeam: agent.InTeam, Activity: "running", lastMessage: preview})
	}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	live := crewTeamLiveFor(t.Context(), team, []crewTeamAgentsPerson{person}, "owner")
	seen := false
	for _, row := range live.Agents {
		if row.Tab == "live" {
			seen = row.LastMessage == preview.LastMessage && row.LastMessageAt == preview.LastMessageAt
		} else if row.LastMessage != "" || row.LastMessageAt != "" {
			t.Fatal("private/hidden/unknown chat metadata leaked")
		}
	}
	if !seen {
		t.Fatal("current Live shared chat preview was not shown")
	}
	raw, _ := json.Marshal(live)
	if strings.Contains(string(raw), `"name":"private"`) {
		t.Fatal("Only-me agent leaked")
	}
	now := time.Now().UTC()
	history := []llmtypes.MessageContent{{Role: llmtypes.ChatMessageType("user"), Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hello\n  there API_KEY=fixture-key-value"}}}}
	updated := crewTeamMessagePreviewUpdate(ChatHistorySession{}, chatHistoryPreviewMessages(history), history, now)
	if updated.LastMessageAt == "" || strings.Contains(updated.LastMessage, "fixture-key-value") || strings.Contains(updated.LastMessage, "\n") {
		t.Fatal("message timestamp/redaction/one-line rule failed")
	}
	previous := ChatHistorySession{PreviewMessages: chatHistoryPreviewMessages(history), LastMessageAt: updated.LastMessageAt}
	history = append(history, llmtypes.MessageContent{Role: llmtypes.ChatMessageType("tool"), Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "private tool output"}}})
	unchanged := crewTeamMessagePreviewUpdate(previous, chatHistoryPreviewMessages(history), history, now.Add(time.Hour))
	if unchanged.LastMessageAt != updated.LastMessageAt || strings.Contains(unchanged.LastMessage, "tool output") {
		t.Fatal("tool-only rewrite changed last visible message")
	}
	old := previous
	old.LastMessageAt = ""
	if got := crewTeamMessagePreviewUpdate(old, chatHistoryPreviewMessages(history), history, now); got.LastMessageAt != "" {
		t.Fatal("old preview invented a timestamp")
	}
	if len([]rune(crewTeamMessageLine(strings.Repeat("界", 100)))) > 80 {
		t.Fatal("preview exceeded Unicode character limit")
	}
}
