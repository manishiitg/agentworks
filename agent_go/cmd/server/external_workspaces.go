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
	add("workspace", "Your private and shared Code/Crew projects. list/get inspect current access; create starts Only me. ask and chat actions use your own conversations; update/export/import use existing Builder authority. Old tools remain supported. Accounts, files and published actions keep the website's permissions.", true, false, map[string]any{
		"action":       map[string]any{"type": "string", "enum": []any{"list", "get", "create", "update", "export", "import", "ask", "chats", "read_chat", "open_chat", "rename_chat", "close_chat", "new_chat", "delete_chat", "stop", "agents", "open_agent", "read_saved_chat", "publish", "publish_preview", "publish_status", "storage_preview", "storage_status", "storage_backup", "storage_move"}},
		"workspace_id": externalString("Stable work:<id> or code:<id> from workspace action=list; an unambiguous old ID also works."),
		"profile_id":   externalString("Creation origin: work or code. Omit to use a product you can create in."),
		"name":         externalString("Project or new chat name."), "message": externalString("Message for ask."),
		"session_id":   externalString("An earlier chat session from action=chats, for read/delete."),
		"agent_id":     externalString("Stable published agent ID from agents or a publish receipt, for open_agent or republish."),
		"operation_id": externalString("Saved publication receipt for publish_status, or storage operation for storage_status/storage_backup/storage_move. Storage operations need administrator/users:manage access. storage_move moves ready projects to Crew with verified backups and old-path links; omit workspace_id for all ready projects. Explicitly select a registered project with unreadable files to move it with a partial backup; skipped entries are reported."),
		"kind":         externalString("read_saved_chat: raw (default) or summary. Builders can read an agent's private imported chat with its current website access."),
		"offset":       externalInteger(0, 1<<30), "limit": externalInteger(0, 65536),
		"source_workspace_id": externalString("Your Local project to publish; its own website chat supplies private history."),
		"conversation_key":    externalString("Your exact Local conversation key. Publishing never guesses another chat."),
		"chat_id":             externalString("A chat name/id you can access; omit to use your caller conversation."),
		"query":               externalString("Optional list filter."), "submission_id": externalString("Stable submission ID for a retry of the same ask."),
		"wait_seconds": externalInteger(0, 25), "after": externalInteger(0, 1<<30),
		"changes": map[string]any{"type": "object"}, "spec": map[string]any{"type": "object", "description": "Creation/import settings: name, icon, role, purpose, mode (dev/cowork/local on code origin), llm_config, selected_skills, templates, files, functions and schedules."},
	}, "action")
}

func externalWorkspaceAllowed(claims *UserClaims) bool {
	if claims == nil || claims.UserID == "" || userAccessForClaims(claims).Disabled {
		return false
	}
	if crewStorageAllowed(claims) {
		return true
	}
	if token := claims.AccessToken; token != nil && (token.BoundAgentID != "" || token.BoundAgentName != "" || !(token.Allows("crews:read") || token.Allows("crews:run") || token.Allows("crews:write") || token.Allows("code:run"))) {
		return false
	}
	return workspaceOriginAllowed(claims, "work") || workspaceOriginAllowed(claims, "code")
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
		if profile.ID == "code" && codeProjectMode(ctx, binding.Binding.WorkspacePath) == "local" && binding.OwnerID != sanitizeUserIDForPath(claims.UserID) {
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
	if action == "publish" || action == "publish_preview" || action == "publish_status" {
		api.externalWorkspacePublish(w, r, args)
		return
	}

	if action == "list" {
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
					seen[key] = true
					items = append(items, map[string]any{"workspace_id": key, "id": doc.ID, "profile_id": product.ProfileID, "name": doc.displayTitle(), "mode": map[bool]string{true: "builder", false: "run"}[binding.OwnedByCaller]})
				}
			}
		}
		externalJSON(w, map[string]any{"workspaces": items})
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
		origin := externalArg(args, "profile_id")
		if origin == "" && (spec.Mode == "cowork" || spec.Mode == "local") {
			origin = "code"
		}
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
				if crewEditLockRefuse(w, binding, claims, "") {
					return
				}
				if codeProjectMode(r.Context(), binding.Binding.WorkspacePath) == "local" {
					externalError(w, 400, "unsupported", "Memory is not available in Local mode.")
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
	if manifest.Product == "code" {
		memberArgs["crew_id"] = "code:" + manifest.ID
	}
	if action == "ask" || action == "read_chat" && externalArg(args, "session_id") == "" {
		api.externalWorkspaceConversation(w, r, action, args, binding, manifest)
		return
	}

	member := map[string]string{"ask": "ask_crew", "update": "update_crew", "export": "export_crew", "import": "import_crew"}[action]
	if chatAction := map[string]string{"chats": "list", "read_chat": "read", "open_chat": "side_open", "rename_chat": "side_rename", "close_chat": "side_close", "new_chat": "new", "delete_chat": "delete", "stop": "stop"}[action]; chatAction != "" {
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

func (api *StreamingAPI) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	var args map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&args); err != nil {
		externalError(w, 400, "invalid_arguments", "Send a workspace action.")
		return
	}
	api.externalWorkspaceCall(w, r, args)
}

func workspaceExternalID(profileID, id string) string {
	if profileID == "code" {
		return "code:" + id
	}
	return id
}

func workspaceTokenProjectID(id string) string {
	return strings.TrimPrefix(strings.TrimPrefix(id, "work:"), "code:")
}

func workspaceCreateTokenAllowed(claims *UserClaims, origin string) bool {
	if claims == nil {
		return false
	}
	if token := claims.AccessToken; token != nil {
		if origin == "code" {
			return token.Allows("code:run") && len(token.CrewIDs) == 0 && token.BoundAgentID == "" && token.BoundAgentName == ""
		}
		return token.AllCrews && token.Allows("crews:write") && token.BoundAgentID == "" && token.BoundAgentName == ""
	}
	return true
}

// These existing services resolve the target and its origin before admitting
// an operation. Crew-origin creation remains separately gated by Crew.
func sharedWorkspaceMember(name string) bool {
	switch name {
	case "get_crew", "list_crew_files", "search_crew_files", "read_crew_file", "write_crew_file", "list_crew_functions", "call_crew_function", "get_crew_function_call", "list_crew_function_calls", "reply_crew_function_call", "update_crew", "export_crew":
		return true
	}
	return false
}

func workspaceOriginAllowed(claims *UserClaims, profileID string) bool {
	return productEnabled(profileID) && userAllowedProduct(claims, profileID)
}
