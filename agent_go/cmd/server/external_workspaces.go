package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// The canonical action family reuses native project services. Origin IDs remain
// storage/routing identities, never a second set of workspace permissions.
func externalWorkspaceDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("workspace", "Your private and shared Crews. list/get inspect current access; create starts Only me. Owners see every owner’s named tabs; only a tab’s owner controls it. message_chat sends an attributed agent message to another owner’s tab. ask can message a selected shared agent in its existing conversation; chat controls use your own tabs; update/export/import use existing Builder authority. agentworks returns your own AgentWorks agent (made on first use) and its main chat: it looks after the Goals you run, which are linked to it automatically; a Crew is linked to it only when you ask, because a Crew link lets it change that Crew. Accounts, files and published actions keep the website's permissions.", true, false, map[string]any{
		"action":       map[string]any{"type": "string", "enum": []any{"list", "get", "agentworks", "create", "update", "export", "import", "ask", "reply_input", "chats", "read_chat", "open_chat", "rename_chat", "close_chat", "new_chat", "delete_chat", "stop", "message_chat", "chat_tools", "chat_tool", "agents", "open_agent", "read_saved_chat", "storage_preview", "storage_status", "storage_backup", "storage_move", "sync"}},
		"sync_action":  map[string]any{"type": "string", "enum": []string{"status", "update", "server", "local"}, "description": "sync: inspect the paired project, update its server files, or move editing to server/local. Pair once in the Mac app. Moves your own main conversation with the project; logins stay on each computer."},
		"workspace_id": externalString("Crew workspace ID from workspace action=list."),
		"name":         externalString("Project or new chat name."), "message": externalString("Message for ask."),
		"query_id":        externalString("Exact Ask turn returned by ask/read_chat. Keep it when polling or answering a pending question."),
		"request_id":      externalString("Pending question from your own Ask turn. Answer with reply_input; clients supporting MCP elicitation may show a form."),
		"answers":         map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": externalString("Question id."), "selected_labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "other_text": externalString("Custom answer when the question allows it.")}, "required": []any{"id"}}, "maxItems": 12},
		"session_id":      externalString("An earlier chat session from action=chats, for read/delete."),
		"agent_id":        externalString("Stable published agent ID from agents or an import receipt, for open_agent."),
		"operation_id":    externalString("Storage operation for storage_status/storage_backup/storage_move. Storage operations need administrator/users:manage access. storage_move moves ready projects to Crew with verified backups; omit workspace_id for all ready projects. Explicitly select a registered project with unreadable files to move it with a partial backup; skipped entries are reported."),
		"kind":            externalString("create: goal, crew or relay. read_saved_chat: raw (default) or summary."),
		"idempotency_key": projectCreateDefinition().InputSchema["properties"].(map[string]any)["idempotency_key"],
		"offset":          externalInteger(0, 1<<30), "limit": externalInteger(0, 65536),
		"conversation_key": externalString("Exact conversation key from this Crew’s tab list."),
		"chat_id":          externalString("Tab id from chats. Selected shared agents accept attributed ask/message_chat and read_chat from sharing onward. Other co-owner tabs allow read_chat/message_chat. Tab controls belong to their owner."),
		"inbox_id":         externalString("For message_chat: existing agent reply address, when continuing a conversation."),
		"query":            externalString("Optional list filter."), "submission_id": externalString("Stable submission ID for a retry of the same ask."),
		"wait_seconds": externalInteger(0, 25), "after": externalInteger(0, 1<<30),
		"before_seq": externalInteger(1, 1<<30),
		"tool_name":  externalString("For chat_tool: exact native name from chat_tools."),
		"arguments":  map[string]any{"type": "object"},
		"changes":    map[string]any{"type": "object"}, "spec": map[string]any{"type": "object", "description": "Creation/import settings: name, icon, purpose, mode (dev/cowork), llm_config, selected_skills, templates, files, functions and schedules."},
	}, "action")
}

func externalWorkspaceAllowed(claims *UserClaims) bool {
	if claims == nil || claims.UserID == "" || userAccessForClaims(claims).Disabled {
		return false
	}
	if crewStorageAllowed(claims) {
		return true
	}
	if token := claims.AccessToken; token != nil && (token.BoundAgentID != "" || token.BoundAgentName != "" || !(token.Allows("crews:read") || token.Allows("crews:run") || token.Allows("crews:write"))) {
		return false
	}
	return workspaceOriginAllowed(claims, crewProfileID)
}

func (api *StreamingAPI) resolveExternalWorkspace(ctx context.Context, claims *UserClaims, raw string) (crewProjectBinding, productProjectManifest, bool) {
	if api == nil || api.agentProfiles == nil || claims == nil {
		return crewProjectBinding{}, productProjectManifest{}, false
	}
	// Listed workspace IDs read "work:<id>"; the bare Crew ID works too.
	id := workspaceTokenProjectID(strings.TrimSpace(raw))
	if !workspaceOriginAllowed(claims, crewProfileID) {
		return crewProjectBinding{}, productProjectManifest{}, false
	}
	if token := claims.AccessToken; token != nil && !workspaceDashboardTokenAllows(claims, crewProfileID, id) {
		return crewProjectBinding{}, productProjectManifest{}, false
	}
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		return crewProjectBinding{}, productProjectManifest{}, false
	}
	binding, err := resolveCrewProjectBinding(ctx, claims.UserID, profile, id, "")
	if err != nil {
		return crewProjectBinding{}, productProjectManifest{}, false
	}
	doc, err := readCrewProjectManifests(ctx, profile.ID, binding.Binding.WorkspacePath)
	if err != nil || doc.ID != id {
		return crewProjectBinding{}, productProjectManifest{}, false
	}
	return binding, doc, true
}

func (api *StreamingAPI) externalWorkspaceCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if externalArg(args, "action") == "create" {
		input := map[string]any{}
		for _, key := range []string{"kind", "spec", "idempotency_key"} {
			if value, ok := args[key]; ok {
				input[key] = value
			}
		}
		api.externalCreateProject(w, r, input)
		return
	}
	if !externalWorkspaceAllowed(claims) {
		externalError(w, 403, "forbidden", "This account cannot use these projects.")
		return
	}
	action := externalArg(args, "action")
	if t := claims.AccessToken; t != nil && t.ProjectKind != "" && action != "list" && action != "get" && action != "chats" && action != "read_chat" {
		externalError(w, 403, "insufficient_scope", "Project connections use published functions; Builder and project chat changes are excluded.")
		return
	}
	if action == "storage_preview" || action == "storage_status" || action == "storage_backup" || action == "storage_move" {
		api.externalCrewStorage(w, r, args)
		return
	}
	if action == "read_saved_chat" {
		api.externalPublicationHistory(w, r, args)
		return
	}
	if action == "agentworks" {
		// The same ensure as a first visit: it acts for the signed-in person only and never widens this connection.
		crew, err := api.ensureAgentWorks(context.Background(), claims.UserID)
		if err != nil {
			externalError(w, 409, "agentworks_unavailable", "Your AgentWorks could not be opened: "+err.Error())
			return
		}
		externalJSON(w, map[string]any{"workspace_id": crewProfileID + ":" + crew.CrewID, "crew_id": crew.CrewID, "chat_id": "main", "name": agentWorksName, "created": crew.Created,
			"next": "Talk to it with workspace action=ask and this workspace_id; for its schedules pass project=crew_id. In the app it is AgentWorks in the Crew list."})
		return
	}

	if action == "sync" {
		api.pairedCrewSyncAction(w, r, externalArg(args, "workspace_id"), externalArg(args, "sync_action"))
		return
	}
	if action == "list" {
		api.listExternalWorkspaces(w, r, args, false)
		return
	}
	if action == "import" {
		spec := crewSpec{Name: strings.TrimSpace(externalArg(args, "name"))}
		if args["spec"] != nil {
			if err := remarshalExternalArgs(args["spec"], &spec); err != nil {
				externalError(w, 400, "invalid_arguments", "Send the Crew's creation settings or exported spec.")
				return
			}
		}
		if name := strings.TrimSpace(externalArg(args, "name")); name != "" {
			spec.Name = name
		}
		origin := crewProfileID
		if !workspaceOriginAllowed(claims, origin) || !userAccessForClaims(claims).CanCreateIn(origin) || !userAccessForClaims(claims).CanEdit {
			externalError(w, 403, "forbidden", "This account cannot create that project. An administrator can change its create permissions.")
			return
		}
		if !workspaceCreateTokenAllowed(claims) {
			externalError(w, 403, "insufficient_scope", "Creating a project needs your existing Builder access.")
			return
		}
		id, setupErr := api.createWorkspaceFromSpec(r.Context(), claims, origin, spec, false)
		if setupErr != nil && id == "" {
			externalError(w, 400, "creation_failed", externalCrewCreateError(id, setupErr))
			return
		}
		_, _, ok := api.resolveExternalWorkspace(r.Context(), claims, id)
		if !ok {
			externalError(w, 502, "workspace_unavailable", "The project was created but could not be opened; use action=list.")
			return
		}

		out := map[string]any{"workspace_id": origin + ":" + id, "id": id, "profile_id": origin, "name": spec.Name, "share": map[string]any{"mode": "private"}}
		if setupErr != nil {
			out["warnings"] = []string{projectCreationWarning(setupErr)}
		}
		externalJSON(w, out)
		return
	}
	binding, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, externalArg(args, "workspace_id"))
	if !ok {
		externalError(w, 404, "not_found", "Project unavailable; use workspace action=list to choose one you can use.")
		return
	}
	if action == "agents" || action == "open_agent" {
		api.externalPublishedAgents(w, r, args, binding, manifest)
		return
	}

	if action == "get" {
		target, err := api.dashboardTarget(r.Context(), claims, binding.Binding.WorkspacePath)
		if err != nil {
			externalFailure(w, err)
			return
		}
		share, _ := api.readRunShare(r.Context(), claims, target)
		share.CanShare = api.dashboardShareOwner(r.Context(), claims, target)
		panels := []string{"dashboard"}
		if binding.OwnedByCaller {
			panels = api.workspaceEffectivePanels(r.Context(), claims.UserID, manifest.Product, binding.Binding.WorkspacePath)
		}
		published, err := publicationSourceSummaries(r.Context(), agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath))
		if err != nil {
			externalFailure(w, err)
			return
		}
		externalJSON(w, map[string]any{"workspace_id": manifest.Product + ":" + manifest.ID, "published_agents": published, "id": manifest.ID, "profile_id": manifest.Product, "name": manifest.displayTitle(), "mode": map[bool]string{true: "builder", false: "run"}[binding.OwnedByCaller], "share": share, "ui_panels": panels, "identity": manifest.Identity, "purpose": manifest.Description})
		return
	}
	memberArgs := map[string]any{}
	for key, value := range args {
		if key != "action" && key != "workspace_id" && key != "profile_id" {
			memberArgs[key] = value
		}
	}
	if action == "update" {
		if changes, ok := args["changes"].(map[string]any); ok {
			for key, value := range changes {
				memberArgs[key] = value
			}
			delete(memberArgs, "changes")
		}
	}
	memberArgs["crew_id"] = manifest.ID

	if action == "ask" || action == "reply_input" || action == "read_chat" && externalArg(args, "session_id") == "" {
		api.externalWorkspaceConversation(w, r, action, args, binding, manifest)
		return
	}

	member := map[string]string{"ask": "ask_crew", "update": "update_crew", "export": "export_crew", "import": "import_crew"}[action]
	if chatAction := map[string]string{"chats": "list", "read_chat": "read", "open_chat": "side_open", "rename_chat": "side_rename", "close_chat": "side_close", "new_chat": "new", "delete_chat": "delete", "stop": "stop", "message_chat": "message", "chat_tools": "tools", "chat_tool": "tool"}[action]; chatAction != "" {
		memberArgs["action"] = chatAction
		member = "manage_crew_chats"
	}
	if member == "" {
		externalError(w, 400, "invalid_arguments", fmt.Sprintf("Unsupported workspace action %q", action))
		return
	}
	tool, exists := externalCatalogTool(member)
	if !exists || !externalTokenAllows(claims, tool) {
		externalError(w, 403, "forbidden", "This connection does not allow that action.")
		return
	}
	if member == "manage_crew_chats" {
		api.externalCrewChatsCall(w, r, memberArgs)
		return
	}
	api.externalCrewCall(w, r, member, memberArgs)
}

// Navigation adds app-only routing metadata to the same admitted workspace list.
// Origin, canonical root and audience remain decided by the existing resolvers.
func (api *StreamingAPI) listExternalWorkspaces(w http.ResponseWriter, r *http.Request, args map[string]any, navigation bool) {
	claims := GetUserFromContext(r.Context())
	items := []map[string]any{}
	seen := map[string]bool{}
	product := crewProduct
	if !workspaceOriginAllowed(claims, product.ProfileID) {
		externalJSON(w, map[string]any{"workspaces": items})
		return
	}
	profile, err := api.agentProfiles.Resolve(product.ProfileID, 0, claims.UserID)
	if err != nil {
		externalJSON(w, map[string]any{"workspaces": items})
		return
	}
	owners := append([]string{claims.UserID}, crewProjectOwnerCandidates(claims.UserID)...)
	for _, owner := range owners {
		// Navigation keeps only work the caller builds; another owner's Crew qualifies only when the caller
		// co-owns it. Skipping the other owners here (registry only) avoids reading and resolving every Crew
		// shared with the caller on each sidebar refresh; the kept rows are still resolved below.
		if navigation && owner != claims.UserID && !ownerHasCrewCoOwnedBy(r.Context(), profile, owner, claims.UserID) {
			continue
		}
		rows := listProjectsForOwner(r.Context(), claims, profile, owner, owner != claims.UserID)
		for _, row := range rows {
			if q := strings.ToLower(strings.TrimSpace(externalArg(args, "query"))); q != "" && !strings.Contains(strings.ToLower(row.Title), q) {
				continue
			}
			key := product.ProfileID + ":" + row.ID
			if seen[key] {
				continue
			}
			binding, doc, ok := api.resolveExternalWorkspace(r.Context(), claims, key)
			if !ok {
				continue
			}
			// The Builder sidebar lists work this person builds. Run-only
			// shares are discovered through Dashboards and ordinary MCP lists.
			if navigation && !binding.OwnedByCaller {
				continue
			}
			seen[key] = true
			item := map[string]any{"workspace_id": key, "id": doc.ID, "profile_id": product.ProfileID, "name": doc.displayTitle(), "icon": doc.Identity.Icon, "mode": map[bool]string{true: "builder", false: "run"}[binding.OwnedByCaller]}
			if navigation {
				root := binding.Binding.WorkspacePath
				item["workspace_path"] = canonicalChatHistoryWorkspacePath(claims.UserID, root)
			}
			items = append(items, item)
		}
	}
	externalJSON(w, map[string]any{"workspaces": items})
}

// ownerHasCrewCoOwnedBy reports whether any of owner's projects of profile lists callerID as a co-owner. A listing
// failure answers true so the full path decides.
func ownerHasCrewCoOwnedBy(ctx context.Context, profile agentprofiles.Profile, owner, callerID string) bool {
	paths, _, err := listProjectManifestPaths(ctx, defaultProductProjectStore(), owner, profile)
	if err != nil {
		return true
	}
	for _, candidate := range paths {
		if crewIsCoOwner(strings.TrimSuffix(candidate, "/product.json"), callerID) {
			return true
		}
	}
	return false
}

func (api *StreamingAPI) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	var args map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&args); err != nil {
		externalError(w, 400, "invalid_arguments", "Send a workspace action.")
		return
	}
	if externalArg(args, "action") == "list" && externalArg(args, "detail") == "navigation" {
		claims := GetUserFromContext(r.Context())
		if claims == nil || strings.TrimSpace(claims.UserID) == "" || claims.AccessToken != nil || claims.Scope != "" ||
			claims.ExecutionPrincipal != nil || claims.ExternalBuilderOperationID != "" || claims.BotRouteGrant != "" ||
			claims.Provider == "bot_owner" || claims.Provider == "bot_route" || claims.Provider == slackDMProvider || !externalWorkspaceAllowed(claims) {
			externalError(w, http.StatusForbidden, "forbidden", "Sign in to browse projects.")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		api.listExternalWorkspaces(w, r, args, true)
		return
	}
	api.externalWorkspaceCall(w, r, args)
}

func workspaceTokenProjectID(id string) string {
	return strings.TrimPrefix(id, "work:")
}

func workspaceCreateTokenAllowed(claims *UserClaims) bool {
	if claims == nil {
		return false
	}
	if token := claims.AccessToken; token != nil {
		return token.AllCrews && token.Allows("crews:write") && token.BoundAgentID == "" && token.BoundAgentName == ""
	}
	return true
}

func workspaceOriginAllowed(claims *UserClaims, profileID string) bool {
	return productEnabled(profileID) && userAllowedProduct(claims, profileID)
}
