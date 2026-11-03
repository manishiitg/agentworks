package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// macCrewTabs is this Crew's side tabs as its owner's own screen lists them, the owner's tabs only.
func (api *StreamingAPI) macCrewTabs(ctx context.Context, claims *UserClaims, project string) ([]AgentProfileSideChat, error) {
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/agent-profiles/work/side-chats?project_id="+url.QueryEscape(project), nil)
	r = mux.SetURLVars(r, map[string]string{"id": "work"})
	capture := &internalResponseCapture{}
	api.handleListAgentProfileSideChats(capture, r)
	var all []AgentProfileSideChat
	if capture.status >= 300 || json.Unmarshal(capture.body.Bytes(), &all) != nil {
		return nil, fmt.Errorf("this Crew's tabs could not be read on your Mac")
	}
	own := all[:0]
	for _, tab := range all {
		_, id, ok := strings.Cut(tab.ConversationKey, codeChatSideMarker)
		if ok && crewMacTabID(id) && tab.CanWrite && (tab.TabOwnerID == "" || tab.TabOwnerID == claims.UserID) && strings.HasPrefix(tab.ConversationKey, project+codeChatSideMarker) {
			own = append(own, tab)
		}
	}
	return own, nil
}

func (api *StreamingAPI) desktopCrewChatBinding(ctx context.Context, project string) (*UserClaims, *crewroot.SyncBinding, desktopConnection, error) {
	if !IsLocalMode() {
		return nil, nil, desktopConnection{}, fmt.Errorf("local app required")
	}
	// The local single-user account is derived here, never from a server
	// claim. The remote account is independently checked by the server.
	claims := &UserClaims{UserID: GetDefaultUserID(), Username: GetDefaultUserID()}
	if _, err := api.crewSyncRoots(ctx, claims, project); err != nil {
		return nil, nil, desktopConnection{}, err
	}
	// Cloud off stops file sync only; the owner keeps using the Mac Crew from the phone (PLAT-856-509).
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" || b.Editing != "local" || b.PendingAction != "" {
		return nil, nil, desktopConnection{}, fmt.Errorf("local editing is unavailable")
	}
	connection, err := desktopConnectionFor(claims.UserID, b.ConnectionID)
	if err != nil || connection.Server != b.PeerServer || connection.User.ID == "" {
		return nil, nil, desktopConnection{}, fmt.Errorf("paired server sign-in is unavailable")
	}
	return claims, b, connection, nil
}

func (api *StreamingAPI) runDesktopCrewChat(ctx context.Context, project, pair string, call crewChatCall) crewChatResult {
	fail := func(code int, message string) crewChatResult {
		body, _ := json.Marshal(map[string]string{"error": message})
		return crewChatResult{ID: call.ID, Status: code, Body: body}
	}
	claims, b, _, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || b.PairID != pair {
		return fail(403, "This Mac is no longer editing the paired Crew.")
	}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		return fail(404, "The local Crew is unavailable.")
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/agent-profiles/work/conversation", nil)
	r = mux.SetURLVars(r, map[string]string{"id": "work"})
	conversation, err := api.resolveAgentProfileConversation(r, profile, project, "")
	if err != nil {
		return fail(409, err.Error())
	}
	if call.Operation == "file" {
		return api.desktopCrewFile(ctx, claims, project, call)
	}
	if call.Operation == "workspace" {
		return api.desktopCrewWorkspace(ctx, claims, project, call)
	}
	// The owner's tabs on the phone and the web (PLAT-856-509): a tab is this Crew's own side chat. Only its key, name
	// and place on the strip leave the Mac; conversation_id addresses its events.
	if call.Operation == "tabs" {
		tabs, err := api.macCrewTabs(ctx, claims, project)
		if err != nil {
			return fail(409, err.Error())
		}
		type tabRow struct {
			ConversationKey string `json:"conversation_key"`
			ConversationID  string `json:"conversation_id"`
			Name            string `json:"name"`
			Position        int    `json:"position,omitempty"`
			Pinned          bool   `json:"pinned"`
		}
		rows := []tabRow{}
		for _, tab := range tabs {
			rows = append(rows, tabRow{ConversationKey: tab.ConversationKey, ConversationID: tab.ConversationID, Name: tab.Name, Position: tab.Position, Pinned: tab.Pinned})
		}
		data, _ := json.Marshal(rows)
		return crewChatResult{ID: call.ID, Status: 200, Body: data}
	}
	if call.Tab != "" && !crewMacTabID(call.Tab) {
		return fail(400, "This tab is unavailable.")
	}
	// A tab, or a session address that is not Main's: the conversation must be one of this Crew's own tabs. Only a
	// resolve with a new tab id opens a new tab, as + New agent does on the Mac.
	if call.Tab != "" || (call.Operation != "resolve" && call.Operation != "handoff" && call.ConversationID != conversation.ConversationID) {
		tabs, err := api.macCrewTabs(ctx, claims, project)
		if err != nil {
			return fail(409, err.Error())
		}
		key, name := "", ""
		for _, tab := range tabs {
			if (call.Tab != "" && tab.ConversationKey == project+codeChatSideMarker+call.Tab) || (call.Tab == "" && call.ConversationID != "" && tab.ConversationID == call.ConversationID) {
				key = tab.ConversationKey
			}
		}
		creating := key == "" && call.Operation == "resolve" && call.Tab != ""
		if creating {
			if name = crewMacTabName(call.Name); name == "" {
				return fail(400, "Give the new agent a name.")
			}
			key = project + codeChatSideMarker + call.Tab
		}
		if key == "" {
			return fail(409, "The chat changed on your Mac. Reopen this Crew to continue it.")
		}
		if conversation, err = api.resolveAgentProfileConversation(r, profile, key, name); err != nil {
			return fail(409, err.Error())
		}
		if creating {
			log.Printf("[CREW_CHAT_RELAY] local project=%s operation=tab_create tab=%s conversation=%s request=%s", project, call.Tab, conversation.ConversationID, call.ID)
		}
	}
	if call.Operation != "resolve" && call.Operation != "handoff" && call.ConversationID != conversation.ConversationID {
		return fail(409, "The main chat changed on your Mac. Reopen this Crew to continue it.")
	}
	// Neither a session, a workspace path, nor a provider/account choice is
	// accepted from the remote request. All come from this local main chat.
	query := crewChatEventQuery(call.Query)
	query.Set("workspace_path", conversation.WorkspacePath)
	var body []byte
	var handler http.HandlerFunc
	method, path := http.MethodGet, "/api/sessions/"+conversation.SessionID
	switch call.Operation {
	case "handoff":
		// Only the owner's own Move online on this Mac moves a relay pair, never a message from the website.
		if b.Mode == "relay" {
			return fail(409, "This Crew lives on your Mac. Move it online from the Mac app.")
		}
		roots, e := api.crewSyncRoots(ctx, claims, project)
		if e != nil {
			return fail(403, e.Error())
		}
		if api.crewNativeTurnRunning(roots) {
			return fail(409, "Let the current reply finish on your Mac, then send again.")
		}
		capture := api.runInternalCrewHandoff(ctx, claims, project, b.ConnectionID, "server")
		status := capture.status
		if status == 0 {
			status = 200
		}
		return crewChatResult{ID: call.ID, Status: status, Body: capture.body.Bytes()}
	case "resolve":
		data, _ := json.Marshal(AgentProfileConversationResponse{TabOwnerID: claims.UserID, CanWrite: true, IsMainChat: conversation.IsMainChat, ConversationID: conversation.ConversationID, ConversationKey: conversation.ConversationKey, SessionID: conversation.SessionID, Provider: conversation.Provider, ModelID: conversation.ModelID, ReasoningEffort: conversation.ReasoningEffort})
		log.Printf("[CREW_CHAT_RELAY] local project=%s operation=resolve session=%s conversation=%s request=%s", project, conversation.SessionID, conversation.ConversationID, call.ID)
		return crewChatResult{ID: call.ID, Status: 200, Body: data}
	case "send":
		if strings.TrimSpace(call.Message) == "" {
			return fail(400, "Write a message first.")
		}
		body, _ = json.Marshal(AgentProfileChatRequest{Message: call.Message, DeliveryMode: call.DeliveryMode, ConversationKey: conversation.ConversationKey})
		method, path, handler = http.MethodPost, "/api/agent-profiles/work/query", api.handleAgentProfileChatQuery
	case "events":
		path += "/events"
		handler = api.handleGetSessionEvents
	case "status":
		path += "/status"
		handler = api.handleGetSessionStatus
	case "queue":
		path += "/queued-messages"
		handler = api.handleQueuedConversationMessages
	case "cancel":
		method, path, handler = http.MethodPost, "/api/session/cancel-turn", api.handleCancelCurrentTurn
	case "answer":
		method, path, body, handler = http.MethodPost, path+"/coding-agent-question/answer", call.Answer, api.handleCodingAgentQuestionAnswer
	default:
		return fail(400, "This action is unavailable through the website.")
	}
	r, _ = http.NewRequestWithContext(ctx, method, "http://localhost"+path+"?"+query.Encode(), bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Session-ID", conversation.SessionID)
	r.Header.Set("Idempotency-Key", call.SubmissionID)
	r = mux.SetURLVars(r, map[string]string{"id": "work", "session_id": conversation.SessionID})
	capture := &internalResponseCapture{}
	handler(capture, r)
	status := capture.status
	if status == 0 {
		status = 200
	}
	data := capture.body.Bytes()
	if len(data) > crewChatMaxReply {
		return fail(413, "This reply is too large. Open a smaller part of the conversation on your Mac.")
	}
	if !json.Valid(data) {
		data, _ = json.Marshal(map[string]string{"error": string(data)})
	}
	if call.Operation == "events" && status == http.StatusOK {
		data, err = projectCrewMacEvents(data, conversation.SessionID, crewChatSessionID(b.PeerProjectID, conversation.ConversationID))
		if err != nil {
			return fail(409, "The Mac's conversation changed. Reopen this Crew to load its messages.")
		}
	}
	if call.Operation == "send" || call.Operation == "cancel" || call.Operation == "answer" {
		log.Printf("[CREW_CHAT_RELAY] local project=%s operation=%s session=%s conversation=%s request=%s status=%d", project, call.Operation, conversation.SessionID, conversation.ConversationID, call.ID, status)
	}
	return crewChatResult{ID: call.ID, Status: status, Body: data}
}

// The browser owns a virtual paired session. Project only events proven to
// belong to the locally resolved main conversation; never relabel another
// session or an arbitrary execution. Content and durable event IDs stay intact.
func projectCrewMacEvents(raw []byte, local, paired string) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	var owner string
	if json.Unmarshal(envelope["session_id"], &owner) != nil || owner != local || local == "" {
		return nil, fmt.Errorf("event envelope belongs to another conversation")
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["events"], &rows); err != nil {
		return nil, err
	}
	identities := map[string]string{
		local: paired, "main:" + local: "main:" + paired,
		"session:" + local:       "session:" + paired,
		local + ":main:" + local: paired + ":main:" + paired,
	}
	var project func(map[string]json.RawMessage, int, string) bool
	project = func(record map[string]json.RawMessage, depth int, opaqueExecution string) bool {
		for _, key := range []string{"session_id", "execution_id", "parent_execution_id", "terminal_id", "terminal_owner_id"} {
			value, exists := record[key]
			if !exists {
				continue
			}
			var id string
			if json.Unmarshal(value, &id) != nil {
				return false
			}
			if id == "" {
				continue
			}
			replacement, ok := identities[id]
			if !ok && key == "execution_id" && id == opaqueExecution {
				continue
			}
			if !ok || key == "session_id" && id != local {
				return false
			}
			record[key], _ = json.Marshal(replacement)
		}
		if depth > 0 {
			var nested map[string]json.RawMessage
			if json.Unmarshal(record["data"], &nested) == nil && nested != nil {
				if !project(nested, depth-1, opaqueExecution) {
					return false
				}
				record["data"], _ = json.Marshal(nested)
			}
		}
		return true
	}
	kept := make([]map[string]json.RawMessage, 0, len(rows))
	readID := func(row map[string]json.RawMessage, key string) string {
		var value string
		_ = json.Unmarshal(row[key], &value)
		return value
	}
	for _, row := range rows {
		if readID(row, "session_id") != local {
			continue
		}
		opaqueExecution := ""
		// File-change cards use a per-turn ID. Its exact main-session anchors
		// prove ownership; preserve that opaque ID, never infer a new route.
		if readID(row, "parent_execution_id") == "main:"+local &&
			readID(row, "terminal_owner_id") == "main:"+local &&
			readID(row, "terminal_id") == local+":main:"+local {
			opaqueExecution = readID(row, "execution_id")
		}
		if project(row, 2, opaqueExecution) {
			kept = append(kept, row)
		}
	}
	if len(kept) != len(rows) {
		log.Printf("[CREW_CHAT_RELAY] phase=event_projection local_session=%s paired_session=%s kept=%d dropped=%d", local, paired, len(kept), len(rows)-len(kept))
	}
	envelope["session_id"], _ = json.Marshal(paired)
	envelope["events"], _ = json.Marshal(kept)
	return json.Marshal(envelope)
}
