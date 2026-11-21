package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// macTeammateChats lists, in the Mac app, the agents teammates share with this Crew's Team: the same rows the left
// menu shows (teamSideChats), read from the Team's server through the Crew's pairing. The key carries the server's
// key after ":team:", as in the left menu, so the person's groups apply. Nothing when the Crew is not paired with a
// server or the server cannot be reached.
func macTeammateChats(ctx context.Context, userID, projectID string) []codeChat {
	binding, err := crewroot.ReadSync(projectID)
	if err != nil || binding == nil || binding.Side != "local" || binding.ConnectionID == "" || binding.PeerProjectID == "" {
		return nil
	}
	connections, err := readDesktopConnections(userID)
	if err != nil {
		return nil
	}
	var connection *desktopConnection
	for i := range connections {
		if connections[i].ID == binding.ConnectionID {
			connection = &connections[i]
		}
	}
	if connection == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var rows []AgentProfileSideChat
	route := "/api/agent-profiles/" + url.PathEscape(crewProfileID) + "/side-chats?project_id=" + url.QueryEscape(binding.PeerProjectID)
	if crewTeamRemoteJSON(ctx, userID, *connection, http.MethodGet, route, nil, &rows) != nil {
		return nil
	}
	var out []codeChat
	for _, row := range rows {
		if row.CanWrite || row.OnYourMac || row.TabOwnerID == "" || row.SessionID == "" || row.IsMain || !strings.Contains(row.ConversationKey, codeChatSideMarker) {
			continue
		}
		id := row.ConversationKey[strings.LastIndex(row.ConversationKey, codeChatSideMarker)+len(codeChatSideMarker):]
		out = append(out, codeChat{
			Teammate:  true,
			OwnerID:   row.TabOwnerID,
			OwnerName: row.TabOwnerName,
			Key:       projectID + ":team:" + row.ConversationKey,
			ID:        "team:" + id,
			Name:      firstNonEmptyTrimmed(row.Name, "Teammate's agent"),
			Purpose:   row.Purpose,
		})
	}
	return out
}

// addMacTeammates adds, in the Mac app, teammates' agents with the person's own group for each, for listing and
// grouping. Other chat tools never see them: a teammate's agent is reached through AgentBridge.
func (p *codeChatProject) addMacTeammates(ctx context.Context, userID string) {
	if !IsLocalMode() {
		return
	}
	teammates := macTeammateChats(ctx, userID, p.ProjectID)
	if len(teammates) == 0 {
		return
	}
	groups, _ := defaultProductConversationRegistryStore().agentGroups(ctx, userID, p.ProfileID, p.ProjectID)
	for i := range teammates {
		teammates[i].Group = groups[teammates[i].Key]
	}
	p.Chats = append(p.Chats, teammates...)
}
