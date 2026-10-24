package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The canonical action family reuses native project services. Origin IDs remain
// storage/routing identities, never a second set of workspace permissions.
func externalWorkspaceDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("workspace", "Your private and shared Crews. list/get inspect current access; create starts Only me. Owners see every owner’s named tabs; only a tab’s owner controls it. message_chat sends an attributed agent message to another owner’s tab. ask and chat changes use your own conversations; update/export/import use existing Builder authority. Accounts, files and published actions keep the website's permissions.", true, false, map[string]any{
		"action":       map[string]any{"type": "string", "enum": []any{"list", "get", "costs", "create", "update", "export", "import", "ask", "reply_input", "chats", "read_chat", "open_chat", "rename_chat", "close_chat", "new_chat", "delete_chat", "stop", "message_chat", "set_chat_model", "chat_tools", "chat_tool", "agents", "open_agent", "read_saved_chat", "storage_preview", "storage_status", "storage_backup", "storage_move"}},
		"workspace_id": externalString("Crew workspace ID from workspace action=list."),
		"name":         externalString("Project or new chat name."), "message": externalString("Message for ask."),
		"query_id":     externalString("Exact Ask turn returned by ask/read_chat. Keep it when polling or answering a pending question."),
		"request_id":   externalString("Pending question from your own Ask turn. Answer with reply_input; clients supporting MCP elicitation may show a form."),
		"answers":      map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": externalString("Question id."), "selected_labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "other_text": externalString("Custom answer when the question allows it.")}, "required": []string{"id"}}, "maxItems": 12},
		"session_id":   externalString("An earlier chat session from action=chats, for read/delete."),
		"agent_id":     externalString("Stable published agent ID from agents or an import receipt, for open_agent."),
		"operation_id": externalString("Storage operation for storage_status/storage_backup/storage_move. Storage operations need administrator/users:manage access. storage_move moves ready projects to Crew with verified backups; omit workspace_id for all ready projects. Explicitly select a registered project with unreadable files to move it with a partial backup; skipped entries are reported."),
		"kind":         externalString("read_saved_chat: raw (default) or summary. Builders can read an agent's private imported chat with its current website access."),
		"offset":       externalInteger(0, 1<<30), "limit": externalInteger(0, 65536),
		"conversation_key": externalString("Exact conversation key from this Crew’s tab list."),
		"chat_id":          externalString("Tab id from chats. Other owners’ tabs allow read_chat and message_chat; ask and control actions use your own tabs."),
		"inbox_id":         externalString("For message_chat: existing agent reply address, when continuing a conversation."),
		"query":            externalString("Optional list filter."), "submission_id": externalString("Stable submission ID for a retry of the same ask."),
		"wait_seconds": externalInteger(0, 25), "after": externalInteger(0, 1<<30),
		"days":             externalInteger(1, 90),
		"before":           externalString("For costs: page before this UTC day (YYYY-MM-DD)."),
		"engine":           externalString("For set_chat_model: a provider from this Crew. Omit to use the Crew model."),
		"model_id":         externalString("For set_chat_model: the model for that provider."),
		"connection_id":    externalString("For a chat model: your personal or available company account ID from provider_connections in the chats list. Only this chat uses it; empty engine restores the Crew default."),
		"reasoning_effort": externalString("For set_chat_model: optional reasoning level."),
		"tool_name":        externalString("For chat_tool: exact native name from chat_tools."),
		"arguments":        map[string]any{"type": "object"},
		"changes":          map[string]any{"type": "object"}, "spec": map[string]any{"type": "object", "description": "Creation/import settings: name, icon, role, purpose, mode (dev/cowork), llm_config, selected_skills, templates, files, functions and schedules."},
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
	origin, id := "", strings.TrimSpace(raw)
	if head, tail, ok := strings.Cut(id, ":"); ok && isProjectProfileID(head) {
		origin, id = head, tail
	}
	var result crewProjectBinding
	var manifest productProjectManifest
	found := false
	for _, product := range projectProducts {
		if origin != "" && product.ProfileID != origin || !workspaceOriginAllowed(claims, product.ProfileID) {
			continue
		}
		if token := claims.AccessToken; token != nil && !workspaceDashboardTokenAllows(claims, product.ProfileID, id) {
			continue
		}
		profile, err := api.agentProfiles.Resolve(product.ProfileID, 0, claims.UserID)
		if err != nil {
			continue
		}
		binding, err := resolveCrewProjectBinding(ctx, claims.UserID, profile, id, "")
		if err != nil {
			continue
		}
		doc, err := readCrewProjectManifests(ctx, profile.ID, binding.Binding.WorkspacePath)
		if err != nil || doc.ID != id {
			continue
		}

		if found {
			return crewProjectBinding{}, productProjectManifest{}, false
		}
		result, manifest, found = binding, doc, true
	}
	return result, manifest, found
}

func (api *StreamingAPI) externalWorkspaceCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
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

	if action == "list" {
		api.listExternalWorkspaces(w, r, args, false)
		return
	}
	if action == "create" || action == "import" {
		spec := crewSpec{Name: strings.TrimSpace(externalArg(args, "name"))}
		if action == "import" || args["spec"] != nil {
			if err := remarshalExternalArgs(args["spec"], &spec); err != nil {
				externalError(w, 400, "invalid_arguments", "Send the Crew's creation settings or exported spec.")
				return
			}
		}
		if name := strings.TrimSpace(externalArg(args, "name")); name != "" {
			spec.Name = name
		}
		origin := crewProfileID
		if origin == "" {
			for _, product := range projectProducts {
				if workspaceOriginAllowed(claims, product.ProfileID) && userAccessForClaims(claims).CanCreateIn(product.ProfileID) {
					origin = product.ProfileID
					break
				}
			}
		}
		if !isProjectProfileID(origin) || !workspaceOriginAllowed(claims, origin) || !userAccessForClaims(claims).CanCreateIn(origin) || !userAccessForClaims(claims).CanEdit {
			externalError(w, 403, "forbidden", "This account cannot create that project. An administrator can change its create permissions.")
			return
		}
		if !workspaceCreateTokenAllowed(claims, origin) {
			externalError(w, 403, "insufficient_scope", "Creating a project needs your existing Builder access.")
			return
		}
		id, setupErr := api.createWorkspaceFromSpec(r.Context(), claims, origin, spec, false)
		if setupErr != nil && id == "" {
			externalError(w, 400, "creation_failed", externalCrewCreateError(id, setupErr))
			return
		}
		_, _, ok := api.resolveExternalWorkspace(r.Context(), claims, origin+":"+id)
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

	if action == "update" {
		if changes, ok := args["changes"].(map[string]any); ok {
			if value, present := changes["memory_enabled"]; present {
				enabled, valid := value.(bool)
				if !valid || len(changes) != 1 {
					externalError(w, 400, "invalid_arguments", "Change memory_enabled with a boolean in its own update.")
					return
				}
				if !binding.OwnedByCaller || !userAccessForClaims(claims).CanEdit || claims.AccessToken != nil && !claims.AccessToken.Allows("crews:write") {
					externalError(w, 403, "forbidden", "Only a Builder can change project memory.")
					return
				}

				if err := setWorkspaceMemory(r.Context(), manifest.Product, binding.Binding.WorkspacePath, enabled); err != nil {
					externalFailure(w, err)
					return
				}
				externalJSON(w, map[string]any{"workspace_id": manifest.Product + ":" + manifest.ID, "memory_enabled": enabled})
				return
			}
		}
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
		externalJSON(w, map[string]any{"workspace_id": manifest.Product + ":" + manifest.ID, "published_agents": published, "id": manifest.ID, "profile_id": manifest.Product, "name": manifest.displayTitle(), "mode": map[bool]string{true: "builder", false: "run"}[binding.OwnedByCaller], "share": share, "ui_panels": panels, "identity": manifest.Identity, "purpose": manifest.Description, "memory_enabled": workspaceMemoryEnabled(r.Context(), manifest.Product, binding.Binding.WorkspacePath)})
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

	member := map[string]string{"costs": "get_crew_costs", "ask": "ask_crew", "update": "update_crew", "export": "export_crew", "import": "import_crew"}[action]
	if chatAction := map[string]string{"chats": "list", "read_chat": "read", "open_chat": "side_open", "rename_chat": "side_rename", "close_chat": "side_close", "new_chat": "new", "delete_chat": "delete", "stop": "stop", "message_chat": "message", "set_chat_model": "set_model", "chat_tools": "tools", "chat_tool": "tool"}[action]; chatAction != "" {
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
	for _, product := range projectProducts {
		if !workspaceOriginAllowed(claims, product.ProfileID) {
			continue
		}
		profile, err := api.agentProfiles.Resolve(product.ProfileID, 0, claims.UserID)
		if err != nil {
			continue
		}
		owners := append([]string{claims.UserID}, crewProjectOwnerCandidates(claims.UserID)...)
		for _, owner := range owners {
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
				item := map[string]any{"workspace_id": key, "id": doc.ID, "profile_id": product.ProfileID, "name": doc.displayTitle(), "mode": map[bool]string{true: "builder", false: "run"}[binding.OwnedByCaller]}
				if navigation {
					root := binding.Binding.WorkspacePath
					item["workspace_path"] = canonicalChatHistoryWorkspacePath(claims.UserID, root)

				}
				items = append(items, item)
			}
		}
	}
	externalJSON(w, map[string]any{"workspaces": items})
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

func workspaceExternalID(profileID, id string) string {

	return id
}

func workspaceTokenProjectID(id string) string {
	return strings.TrimPrefix(id, "work:")
}

func workspaceCreateTokenAllowed(claims *UserClaims, origin string) bool {
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
