package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// crewTeamAgentChat is one chat of the Team agent, as every current Team member sees it (#3059): the Team agent is a
// teammate, so its chats are the Team's. Each person who asks it gets their own chat in the Team's online home
// (sharedThreadKey); only that person writes in it (through the usual Ask) and the others read it. The home Main is
// the Team agent's own chat (its schedules, Watchers and setup work); it has no person.
type crewTeamAgentChat struct {
	SessionID string `json:"session_id"`
	PersonID  string `json:"person_id,omitempty"` // empty for the Team agent's own chat
	Person    string `json:"person"`
	You       bool   `json:"you,omitempty"`
	Activity  string `json:"activity"` // working or idle
	crewTeamMessagePreview
	workspace string
}

// crewTeamAgentChatAskers maps each per-person chat key of the home's Team agent to the person who asked in it. The
// asker is only recorded on the AgentBridge conversation that delivered the question, never in the chat key.
func crewTeamAgentChatAskers(ctx context.Context, home string) map[string]string {
	out := map[string]string{}
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return out
	}
	main := crewOwnerMainChatKey(home)
	for _, c := range s.Conversations {
		for _, e := range c.Endpoints {
			if e.ID == home && e.OfflineCopyTeam == "" && e.PublishedAgent == main && e.PublishedAsker != "" && isSharedThreadKey(e.ChatKey) {
				out[e.ChatKey] = e.PublishedAsker
			}
		}
	}
	return out
}

// crewTeamAgentChatRecords are the Team agent's chats a viewer may read: only for a current member of a Team that is
// on, its own chat and those of people still on the Team. This is the one access rule for the list and the reader.
func crewTeamAgentChatRecords(ctx context.Context, t crewTeam, viewer string) (crewTeam, []crewTeamAgentChat) {
	if !t.On || !t.member(viewer) || !crewTeamAccountActive(viewer) {
		return crewTeam{}, nil
	}
	home, ok := crewTeamAgentHome(t)
	if !ok {
		return crewTeam{}, nil
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(home.OwnerID))
	if err != nil {
		return crewTeam{}, nil
	}
	main := crewOwnerMainChatKey(t.OnlineHome)
	askers := crewTeamAgentChatAskers(ctx, t.OnlineHome)
	out := []crewTeamAgentChat{}
	for _, record := range doc.Entries {
		if record.ProfileID != crewProfileID || record.SessionID == "" || crewResourceProjectID(record.ConversationKey) != t.OnlineHome {
			continue
		}
		row := crewTeamAgentChat{SessionID: record.SessionID, Person: "Team schedules and setup", workspace: record.WorkspacePath}
		if record.ConversationKey != main {
			who := askers[record.ConversationKey]
			if !isSharedThreadKey(record.ConversationKey) || !t.member(who) {
				continue
			}
			row.PersonID, row.Person, row.You = who, crewPersonShortName(who), who == viewer
		}
		out = append(out, row)
	}
	return home, out
}

// crewTeamAgentChats lists the Team agent's chats for a current member, working first then newest. A chat with no
// message yet is not listed.
func crewTeamAgentChats(ctx context.Context, t crewTeam, viewer string) []crewTeamAgentChat {
	out := []crewTeamAgentChat{}
	home, rows := crewTeamAgentChatRecords(ctx, t, viewer)
	if len(rows) == 0 {
		return out
	}
	sessions := map[string]bool{}
	for _, row := range rows {
		sessions[row.SessionID] = true
	}
	byKey := crewTeamMessagePreviewsFor(ctx, home.OwnerID, t.OnlineHome, func(string) bool { return true })
	doc, _ := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(home.OwnerID))
	previews := map[string]crewTeamMessagePreview{}
	for _, record := range doc.Entries {
		if preview, ok := byKey[record.ConversationKey]; ok && sessions[record.SessionID] {
			previews[record.SessionID] = preview
		}
	}
	for _, row := range rows {
		preview := previews[row.SessionID]
		if preview.LastMessage == "" {
			continue
		}
		row.crewTeamMessagePreview, row.Activity = preview, "idle"
		if crewServerChatRunning(row.SessionID) {
			row.Activity = "working"
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Activity == "working") != (out[j].Activity == "working") {
			return out[i].Activity == "working"
		}
		return crewTeamMessageTimeAfter(out[i].LastMessageAt, out[j].LastMessageAt)
	})
	return out
}

// crewTeamAgentChatLine is one message of a Team agent chat as the reader shows it: who said it and the text.
type crewTeamAgentChatLine struct {
	From string `json:"from"` // person or agent
	Text string `json:"text"`
}

const crewTeamAgentChatReadLimit = 200

// crewTeamAgentChatLines are a chat's visible messages (newest last, at most the read limit): only the text the person
// typed and the agent wrote. Tool calls, tool output and system lines stay out, so a schedule's shell output in the
// Team agent's own chat can't show a secret value to the Team.
func crewTeamAgentChatLines(history []llmtypes.MessageContent) []crewTeamAgentChatLine {
	out := []crewTeamAgentChatLine{}
	for _, message := range history {
		from, role := "", strings.ToLower(string(message.Role))
		switch role {
		case "human", "user":
			from = "person"
		case "ai", "assistant":
			from = "agent"
		default:
			continue
		}
		parts := []string{}
		for _, part := range message.Parts {
			if text := chatHistoryPartText(part); text != "" {
				parts = append(parts, text)
			}
		}
		text := strings.TrimSpace(cleanChatHistoryQuery(strings.Join(parts, "\n")))
		if text == "" || shouldSkipChatHistoryPreviewText(text) || isClaudeLocalCommandMessage(role, text) {
			continue
		}
		out = append(out, crewTeamAgentChatLine{From: from, Text: text})
	}
	if len(out) > crewTeamAgentChatReadLimit {
		out = out[len(out)-crewTeamAgentChatReadLimit:]
	}
	return out
}

// handleCrewTeamAgentChats: GET /crew-teams/{project}/team-agent/chats lists the Team agent's chats for a current
// member; GET .../chats/{session} returns one of them to read. The same rule decides both (crewTeamAgentChatRecords).
func (api *StreamingAPI) handleCrewTeamAgentChats(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	session := mux.Vars(r)["session"]
	if session == "" {
		chats := crewTeamAgentChats(r.Context(), t, claims.UserID)
		log.Printf("[TEAM_AGENT_CHATS] project=%s viewer=%s event=list chats=%d", t.ProjectID, claims.UserID, len(chats))
		externalJSON(w, map[string]any{"chats": chats})
		return
	}
	home, rows := crewTeamAgentChatRecords(r.Context(), t, claims.UserID)
	for _, row := range rows {
		if row.SessionID != session {
			continue
		}
		lines := []crewTeamAgentChatLine{}
		if data, err := ReadChatHistoryConversation(home.OwnerID, session, row.workspace); err == nil {
			var conv struct {
				History []llmtypes.MessageContent `json:"conversation_history"`
			}
			if json.Unmarshal(data, &conv) == nil {
				lines = crewTeamAgentChatLines(conv.History)
			}
		}
		log.Printf("[TEAM_AGENT_CHATS] project=%s viewer=%s session=%s event=read outcome=ok messages=%d own=%t", t.ProjectID, claims.UserID, session, len(lines), row.You)
		externalJSON(w, map[string]any{"person": row.Person, "you": row.You, "messages": lines, "note": "Your Team can see this chat."})
		return
	}
	log.Printf("[TEAM_AGENT_CHATS] project=%s viewer=%s session=%s event=read outcome=refused reason=not_a_team_agent_chat_for_viewer", t.ProjectID, claims.UserID, session)
	externalError(w, http.StatusNotFound, "chat_not_found", "That chat isn't one of the Team agent's chats, or you're not on this Team.")
}
