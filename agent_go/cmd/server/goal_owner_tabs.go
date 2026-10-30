package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

const goalChatProfile = "agentworks"

// Goals and Relays use the same durable tab identities as Crews. The runtime remains the
// workflow Builder: these records never turn a chat into a workflow execution.
type goalOwnerTab struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	OwnerName string `json:"owner_name"`
	SessionID string `json:"session_id"`
	IsMain    bool   `json:"is_main"`
	CanWrite  bool   `json:"can_write"`
	Working   bool   `json:"working"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Position  int    `json:"position,omitempty"`
	Pinned    bool   `json:"pinned"`
}

func goalTabMembers(m *WorkflowManifest) []string {
	if m == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	members := append([]string{}, m.effectiveOwners()...)
	for _, id := range members {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func goalHasMember(m *WorkflowManifest, user string) bool {
	for _, id := range goalTabMembers(m) {
		if id == user {
			return true
		}
	}
	return false
}

// A registry row identifies a tab; its saved transcript supplies its latest
// activity and first-message title, just as the history screen does (PLAT-871-28).
func goalTabPresentation(record ProductConversationRecord, identity ProductAgentIdentity, history ChatHistorySession) (name, updatedAt string) {
	name = firstNonEmptyTrimmed(identity.Name, record.Title, "Builder")
	if name == "Builder" || name == "Chat" {
		if history.Title == "Builder" || history.Title == "Chat" {
			history.Title = ""
		}
		name = conversationTitleFrom(&history, name)
	}
	updatedAt = record.UpdatedAt
	stored, _ := time.Parse(time.RFC3339Nano, updatedAt)
	latest, err := time.Parse(time.RFC3339Nano, history.UpdatedAt)
	if err == nil && latest.After(stored) {
		updatedAt = history.UpdatedAt
	}
	return name, updatedAt
}

func (api *StreamingAPI) goalOwnerTabs(ctx context.Context, scope workflowBuilderScope) ([]goalOwnerTab, error) {
	scope, status := authorizeWorkflowBuilderScope(ctx, scope, true)
	if status != 0 {
		return nil, fmt.Errorf("Builder access denied")
	}
	_, m := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), scope.Workspace)
	if m == nil {
		return nil, fmt.Errorf("Choose a Goal or Relay")
	}
	user := GetUserIDFromContext(ctx)
	people := goalTabMembers(m)
	if !containsID(people, user) {
		people = []string{user}
	}
	store := defaultProductConversationRegistryStore()
	out := []goalOwnerTab{}
	for _, owner := range people {
		records, err := store.projectChats(ctx, owner, goalChatProfile, scope.PresetID)
		if err != nil {
			return nil, err
		}
		identities, err := store.agentIdentities(ctx, owner, goalChatProfile, scope.PresetID)
		if err != nil {
			return nil, err
		}
		history := chatHistorySessionsByID(owner, scope.Workspace)
		for _, record := range records {
			if record.WorkspacePath != scope.Workspace {
				continue
			}
			identity := identities[record.ConversationKey]
			name, updatedAt := goalTabPresentation(record, identity, history[record.SessionID])
			out = append(out, goalOwnerTab{ID: record.ConversationKey, Name: name, OwnerID: owner,
				OwnerName: crewOwnerDisplayName(owner), SessionID: record.SessionID,
				IsMain:   record.ConversationKey == crewOwnerDefaultChatKey(scope.PresetID, owner),
				Position: identity.Position, Pinned: !identity.Unpinned,
				CanWrite: owner == user, Working: api.conversationTurnOccupied(record.SessionID), UpdatedAt: updatedAt})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CanWrite != out[j].CanWrite {
			return out[i].CanWrite
		}
		if out[i].IsMain != out[j].IsMain {
			return out[i].IsMain
		}
		if out[i].Position != out[j].Position {
			if out[i].Position == 0 {
				return false
			}
			if out[j].Position == 0 {
				return true
			}
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func storeGoalOwnerTab(ctx context.Context, scope workflowBuilderScope, name string, main bool, preferredSession string) (goalOwnerTab, error) {
	scope, status := authorizeWorkflowBuilderScope(ctx, scope, true)
	if status != 0 {
		return goalOwnerTab{}, fmt.Errorf("Builder access denied")
	}
	_, m := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), scope.Workspace)
	if m == nil {
		return goalOwnerTab{}, fmt.Errorf("Choose a Goal or Relay")
	}
	user := GetUserIDFromContext(ctx)
	name = strings.TrimSpace(name)
	if name == "" {
		if !main {
			return goalOwnerTab{}, fmt.Errorf("name this agent before opening it")
		}
		name = "Builder"
	}
	if len([]rune(name)) > productAgentNameMaxRunes {
		return goalOwnerTab{}, fmt.Errorf("choose a shorter tab name")
	}
	key := scope.PresetID + codeChatSideMarker + uuid.NewString()
	if main {
		key = crewOwnerDefaultChatKey(scope.PresetID, user)
	}
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(user)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	doc, err := store.loadDocument(ctx, path)
	if err != nil {
		return goalOwnerTab{}, err
	}
	entry := productConversationRegistryEntryKey(goalChatProfile, key)
	record, found := doc.Entries[entry]
	if !found {
		count := 0
		for _, r := range doc.Entries {
			if r.ProfileID == goalChatProfile && r.ResourceID == scope.PresetID {
				count++
			}
		}
		if !main && count >= externalCrewMaxSideChats+1 {
			return goalOwnerTab{}, fmt.Errorf("close a tab before adding another")
		}
		if preferredSession == "" {
			preferredSession = uuid.NewString()
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		record = ProductConversationRecord{ConversationID: uuid.NewString(), ConversationKey: key, ProfileID: goalChatProfile,
			ProfileVersion: 1, ResourceID: scope.PresetID, WorkspacePath: scope.Workspace, SessionID: preferredSession,
			Title: name, CreatedAt: now, UpdatedAt: now}
		doc.Entries[entry] = record
		if doc.Agents == nil {
			doc.Agents = map[string]ProductAgentIdentity{}
		}
		doc.Agents[entry] = ProductAgentIdentity{Name: name, UpdatedBy: user, UpdatedAt: now}
		if err := store.writeDocument(ctx, path, doc); err != nil {
			return goalOwnerTab{}, err
		}
	}
	identity := doc.Agents[entry]
	return goalOwnerTab{ID: key, Name: firstNonEmptyTrimmed(identity.Name, record.Title, "Builder"), OwnerID: user,
		OwnerName: crewOwnerDisplayName(user), SessionID: record.SessionID, IsMain: main, CanWrite: true, Position: identity.Position, Pinned: !identity.Unpinned, UpdatedAt: record.UpdatedAt}, nil
}

func (api *StreamingAPI) createGoalOwnerTab(ctx context.Context, scope workflowBuilderScope, name string, main bool, preferredSession string) (goalOwnerTab, error) {
	tab, err := storeGoalOwnerTab(ctx, scope, name, main, preferredSession)
	if err == nil && api.eventStore != nil {
		_ = api.eventStore.SetSessionPersistenceClass(tab.SessionID, storeevents.SessionPersistenceInteractiveChat)
		api.eventStore.SetSessionOwner(tab.SessionID, tab.OwnerID)
	}
	return tab, err
}

func (api *StreamingAPI) goalSessionRecord(ctx context.Context, session string) (ProductConversationRecord, string, bool) {
	owner := ""
	if active, ok := api.getActiveSession(session); ok {
		owner = active.UserID
	}
	if owner == "" && api.eventStore != nil {
		owner = api.eventStore.GetSessionOwner(session)
		if owner == "" {
			owner, _ = api.eventStore.DurableChatOwner(session)
		}
	}
	if owner == "" {
		return ProductConversationRecord{}, "", false
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(owner))
	if err != nil {
		return ProductConversationRecord{}, "", false
	}
	for _, record := range doc.Entries {
		if record.ProfileID == goalChatProfile && record.SessionID == session {
			return record, owner, true
		}
	}
	return ProductConversationRecord{}, "", false
}

func (api *StreamingAPI) goalTabForSession(ctx context.Context, session string) (goalOwnerTab, string, bool) {
	record, owner, found := api.goalSessionRecord(ctx, session)
	if !found {
		return goalOwnerTab{}, "", false
	}
	if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil && !claims.AccessToken.Allows("builder:chat") {
		return goalOwnerTab{}, "", false
	}

	access, m := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), record.WorkspacePath)
	if m == nil || m.ID != record.ResourceID || (!goalHasMember(m, owner) && owner != GetUserIDFromContext(ctx)) ||
		(!goalHasMember(m, GetUserIDFromContext(ctx)) && owner != GetUserIDFromContext(ctx)) || (access != WorkflowAccessOwner && access != WorkflowAccessWrite) || !userAllowedWorkflowID(GetUserFromContext(ctx), m.ID) {
		return goalOwnerTab{}, "", false
	}
	return goalOwnerTab{ID: record.ConversationKey, Name: firstNonEmptyTrimmed(record.Title, "Builder"), OwnerID: owner,
		OwnerName: crewOwnerDisplayName(owner), SessionID: session, IsMain: record.ConversationKey == crewOwnerDefaultChatKey(m.ID, owner), CanWrite: owner == GetUserIDFromContext(ctx)}, record.WorkspacePath, true
}

func (api *StreamingAPI) goalOwnerTabSummary(ctx context.Context, scope workflowBuilderScope, fresh bool) (*workflowBuilderSessionResponse, error) {
	user := GetUserIDFromContext(ctx)
	var main goalOwnerTab
	tabs, err := api.goalOwnerTabs(ctx, scope)
	if err != nil {
		return nil, err
	}
	for _, tab := range tabs {
		if tab.OwnerID == user && tab.IsMain {
			main = tab
			break
		}
	}
	if main.ID == "" {
		// Existing conversation history is retained. Only this person's current
		// interactive Builder becomes the shared main tab; past chats stay private.
		roles, readErr := api.workflowBuilderSidebarSessions(ctx, scope.Workspace)
		if readErr != nil {
			return nil, readErr
		}
		preferred := ""
		var updated time.Time
		for id, role := range roles {
			if (role.Kind == "history" || role.IsBuilder) && (preferred == "" || role.updated.After(updated)) {
				preferred = id
				updated = role.updated
			}
		}
		main, err = api.createGoalOwnerTab(ctx, scope, "Builder", true, preferred)
		if err != nil {
			return nil, err
		}
		tabs, err = api.goalOwnerTabs(ctx, scope)
		if err != nil {
			return nil, err
		}
	}
	if fresh {
		tab, openErr := api.createGoalOwnerTab(ctx, scope, "Chat", false, "")
		if openErr != nil {
			return nil, openErr
		}
		main = tab
		tabs, err = api.goalOwnerTabs(ctx, scope)
		if err != nil {
			return nil, err
		}
	}
	roles, err := api.workflowBuilderSidebarSessions(ctx, scope.Workspace)
	if err != nil {
		return nil, err
	}
	for _, tab := range tabs {
		if role := roles[tab.SessionID]; role.Kind == "run" || workflowBuilderHistoryKind(tab.SessionID, "", "", "") == "run" {
			role.IsBuilder, role.Kind = false, "run"
			roles[tab.SessionID] = role
			continue
		}
		roles[tab.SessionID] = workflowSidebarSession{IsBuilder: true, Kind: "builder"}
	}
	status := "idle"
	source := "none"
	if active, ok := api.getActiveSession(main.SessionID); ok {
		status = active.Status
		main.UpdatedAt = active.LastActivity.Format(time.RFC3339Nano)
		source = "live"
	}
	return &workflowBuilderSessionResponse{Success: true, IsBuilder: true, Source: source, SessionID: main.SessionID, PhaseID: "workflow-builder",
		Status: status, PresetQueryID: scope.PresetID, WorkspacePath: scope.Workspace, WorkflowName: workflowNameFromWorkspacePath(scope.Workspace),
		UpdatedAt: main.UpdatedAt, ActivityUpdatedAt: main.UpdatedAt, Events: []json.RawMessage{}, LastProcessedIndex: -1, Sessions: roles, Tabs: tabs}, nil
}

func (api *StreamingAPI) handleGoalOwnerTabs(w http.ResponseWriter, r *http.Request) {
	var args struct {
		workflowBuilderScope
		Action    string   `json:"action"`
		ChatID    string   `json:"chat_id"`
		SessionID string   `json:"session_id"`
		Name      string   `json:"name"`
		Pinned    *bool    `json:"pinned"`
		ChatIDs   []string `json:"chat_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&args); err != nil {
		externalError(w, 400, "invalid_arguments", "Choose a chat action.")
		return
	}
	scope, status := authorizeWorkflowBuilderScope(r.Context(), args.workflowBuilderScope, true)
	if status != 0 {
		externalError(w, status, "forbidden", "Builder access required.")
		return
	}
	if args.Action == "list" || args.Action == "" {
		summary, err := api.goalOwnerTabSummary(r.Context(), scope, false)
		if err != nil {
			externalError(w, 503, "chats_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"chats": summary.Tabs})
		return
	}
	if args.Action == "open" {
		tab, err := api.createGoalOwnerTab(r.Context(), scope, args.Name, false, "")
		if err != nil {
			externalError(w, 400, "chat_failed", err.Error())
			return
		}
		publishSessionsChanged()
		externalJSON(w, map[string]any{"chat": tab})
		return
	}
	tabs, err := api.goalOwnerTabs(r.Context(), scope)
	if err != nil {
		externalError(w, 400, "chat_failed", err.Error())
		return
	}
	if args.Action == "order" {
		for _, id := range args.ChatIDs {
			found := false
			for _, tab := range tabs {
				if tab.ID != id {
					continue
				}
				found = true
				if !tab.CanWrite {
					externalError(w, 403, "tab_owner", tab.OwnerName+" controls this tab.")
					return
				}
				if tab.IsMain {
					externalError(w, 400, "main_chat", "Keep your Builder tab first.")
					return
				}
			}
			if !found {
				externalError(w, 404, "chat_unavailable", "Choose tabs from this project.")
				return
			}
		}
		if err := defaultProductConversationRegistryStore().setSideChatOrder(r.Context(), GetUserIDFromContext(r.Context()), goalChatProfile, scope.PresetID, args.ChatIDs); err != nil {
			externalError(w, 400, "chat_failed", err.Error())
			return
		}
		publishSessionsChanged()
		externalJSON(w, map[string]any{"success": true})
		return
	}
	var tab goalOwnerTab
	id := firstNonEmptyTrimmed(args.ChatID, args.SessionID)
	for _, t := range tabs {
		if id != "" && (t.ID == id || t.SessionID == id || t.ID == scope.PresetID+codeChatSideMarker+id || (id == "main" && t.IsMain && t.CanWrite)) {
			tab = t
			break
		}
	}
	if tab.ID == "" {
		externalError(w, 404, "chat_unavailable", "Choose a tab from this project.")
		return
	}
	if args.Action == "read" {
		events, _, _, err := api.goalTabEvents(r.Context(), tab, scope.Workspace)
		if err != nil {
			externalError(w, 503, "history_unavailable", "Could not read this chat.")
			return
		}
		externalJSON(w, map[string]any{"chat": tab, "events": events})
		return
	}
	if !tab.CanWrite {
		externalError(w, 403, "tab_owner", tab.OwnerName+" controls this tab. You can view it or use your own tab.")
		return
	}
	switch args.Action {
	case "pin":
		if args.Pinned == nil {
			externalError(w, 400, "invalid_arguments", "Choose whether to pin this agent.")
			return
		}
		if tab.IsMain {
			externalError(w, 400, "main_chat", "Keep your Builder tab on the tab bar.")
			return
		}
		err = defaultProductConversationRegistryStore().setSideChatPinned(r.Context(), tab.OwnerID, goalChatProfile, scope.PresetID, tab.ID, *args.Pinned)
	case "rename":
		name := strings.TrimSpace(args.Name)
		if name == "" {
			externalError(w, 400, "invalid_arguments", "Enter a tab name.")
			return
		}
		_, err = defaultProductConversationRegistryStore().setAgentIdentity(r.Context(), tab.OwnerID, goalChatProfile, tab.ID, &name, nil, tab.OwnerID)
	case "close":
		if tab.IsMain {
			externalError(w, 400, "main_chat", "Keep your Builder tab; close a named tab instead.")
			return
		}
		if api.sessionStillWorkingAfterIdleRelease(tab.SessionID) {
			externalError(w, 409, "chat_working", "This agent is still working. Stop it before closing.")
			return
		}
		store := defaultProductConversationRegistryStore()
		path := productConversationRegistryPath(tab.OwnerID)
		lock := productConversationRegistryMutex(path)
		lock.Lock()
		var doc productConversationRegistryDocument
		doc, err = store.loadDocument(r.Context(), path)
		if err == nil {
			key := productConversationRegistryEntryKey(goalChatProfile, tab.ID)
			delete(doc.Entries, key)
			delete(doc.Agents, key)
			err = store.writeDocument(r.Context(), path, doc)
		}
		lock.Unlock()
	default:
		externalError(w, 400, "invalid_arguments", "Use list, open, read, rename, pin, order or close.")
		return
	}
	if err != nil {
		externalError(w, 400, "chat_failed", err.Error())
		return
	}
	publishSessionsChanged()
	externalJSON(w, map[string]any{"success": true})
}

// One native Builder tool and the MCP builder tabs action call the website
// handler, so no second tab roster or permission policy exists.
func (api *StreamingAPI) registerGoalOwnerTabTools(reg definitionToolRegistrar, user, root string) error {
	return reg.RegisterCustomTool("manage_builder_chats", "Manage this Goal or Relay's named Builder tabs. list/read: see the project tabs. open: create your own named tab. rename/pin/order/close: only your own tabs; keep your main Builder. To talk in your tab over MCP use builder action=chat with its session_id. Agent messages use send_message with target and chat_id from list_agents. Plans and runs remain shared.", goalOwnerTabSchema(false), func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = internalBotRequestContext(ctx, user)
		clean := map[string]interface{}{"workspace_path": root, "action": args["action"], "chat_id": args["chat_id"], "session_id": args["session_id"], "name": args["name"], "pinned": args["pinned"], "chat_ids": args["chat_ids"]}
		body, err := json.Marshal(clean)
		if err != nil {
			return "", err
		}
		req := httptest.NewRequest(http.MethodPost, "/api/workflow/chats", bytes.NewReader(body)).WithContext(ctx)
		w := httptest.NewRecorder()
		api.handleGoalOwnerTabs(w, req)
		if w.Code >= 400 {
			return "", fmt.Errorf("%s", w.Body.String())
		}
		return w.Body.String(), nil
	}, "workspace_advanced")
}
func goalOwnerTabSchema(external bool) map[string]interface{} {
	action := "action"
	if external {
		action = "tab_action"
	}
	return map[string]interface{}{"type": "object", "required": []string{action}, "properties": map[string]interface{}{
		action:       map[string]interface{}{"type": "string", "enum": []any{"list", "open", "read", "rename", "pin", "order", "close"}},
		"pinned":     map[string]interface{}{"type": "boolean", "description": "Keep this agent on the tab bar (pin)."},
		"chat_ids":   map[string]interface{}{"type": "array", "items": externalString("Your named tab ID."), "description": "Your named tabs in first-to-last order; the main Builder stays first."},
		"session_id": externalString("Session ID from list, when not using chat_id."),
		"chat_id":    externalString("Tab ID from list or run_destinations; main selects your main tab."), "name": externalString("Short tab name for open or rename."),
	}}
}
func (api *StreamingAPI) goalTabEvents(ctx context.Context, tab goalOwnerTab, root string) ([]json.RawMessage, string, uint64, error) {
	if api.eventStore != nil {
		events := api.eventStore.GetAllEventsRaw(tab.SessionID)
		if len(events) > 0 {
			return marshalBuilderEvents(events), "", 0, nil
		}
	}
	path, found, err := findWorkflowBuilderConversationPathForSession(ctx, tab.OwnerID, tab.SessionID, root)
	if err != nil || !found {
		return []json.RawMessage{}, "", 0, err
	}
	raw, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil || !exists {
		return []json.RawMessage{}, "", 0, err
	}
	var log builderConversationLog
	if err := json.Unmarshal([]byte(raw), &log); err != nil {
		return nil, "", 0, err
	}
	if log.UserID != "" && log.UserID != tab.OwnerID {
		return nil, "", 0, fmt.Errorf("chat owner changed")
	}
	return builderConversationToRawEvents(log), path, log.Revision, nil
}
