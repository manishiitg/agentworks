package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// These groups reuse the website/chat registrars. Their external transport
// supplies identity and the resolved Crew root, never an arbitrary workspace.
var externalCrewCapabilities = map[string]map[string]string{
	"crew_skills":     {"list": "list_skills", "search": "search_skills", "install": "install_skill", "import": "import_skill", "uninstall": "uninstall_skill"},
	"crew_mcp":        {"list": "list_mcp_servers", "search": "search_mcp_catalog", "install": "install_mcp_server", "remove": "remove_mcp_server", "discover": "trigger_mcp_discovery"},
	"crew_folders":    {"list": "list_work_folders", "attach": "attach_work_folder", "detach": "detach_work_folder"},
	"crew_references": {"list": "list_accessible_workflows", "attach": "attach_workflow_reference", "detach": "detach_workflow_reference"},
}

type crewCapabilityTool struct {
	description string
	schema      map[string]any
	execute     func(context.Context, map[string]any) (string, error)
}
type crewCapabilityRegistrar map[string]crewCapabilityTool

func (r crewCapabilityRegistrar) RegisterCustomTool(name, description string, schema map[string]any, execute func(context.Context, map[string]any) (string, error), _ string) error {
	raw, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	var normalized map[string]any
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return err
	}
	normalized["additionalProperties"] = false
	if normalized["required"] == nil {
		delete(normalized, "required")
	}
	// MCP setup accepts encrypted account credentials, while excluding arbitrary process config.
	if name == "install_mcp_server" {
		props := normalized["properties"].(map[string]any)
		for key := range props {
			if key != "name" && key != "url" && key != "label" && key != "catalog" && key != "vault_id" && key != "credential" && key != "transport" && key != "reconnect" && key != "api_key" && key != "api_key_header" && key != "api_key_format" && key != "client_id" && key != "client_secret" {
				delete(props, key)
			}
		}
	}
	if name == "trigger_mcp_discovery" {
		normalized["required"] = []any{"name"}
	}
	r[name] = crewCapabilityTool{description: description, schema: normalized, execute: execute}
	return nil
}
func (r crewCapabilityRegistrar) RegisterCustomToolWithTimeout(name, description string, schema map[string]any, execute func(context.Context, map[string]any) (string, error), _ time.Duration, category string) error {
	return r.RegisterCustomTool(name, description, schema, execute, category)
}
func (api *StreamingAPI) crewCapabilityTools(group, user, root string, sessionIDs ...string) (crewCapabilityRegistrar, error) {
	reg := crewCapabilityRegistrar{}
	switch group {
	case "crew_skills":
		return reg, api.registerMultiAgentSkillToolsIn(reg, nil, "work", root+"/skills")
	case "crew_mcp":
		return reg, api.registerMultiAgentMCPServerTools(reg, func(name string) bool {
			for _, member := range externalCrewCapabilities[group] {
				if member == name {
					return false
				}
			}
			return true
		})
	case "crew_folders":
		return reg, api.registerWorkFolderTools(reg, user, "")
	case "crew_references":
		sessionID := ""
		if len(sessionIDs) > 0 {
			sessionID = sessionIDs[0]
		}
		return reg, api.registerWorkWorkflowReferenceTools(reg, user, sessionID, root, false)
	}
	return nil, fmt.Errorf("unknown Crew capability")
}
func externalCrewCapabilityDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) error {
	api := &StreamingAPI{}
	groups := []string{"crew_skills", "crew_mcp", "crew_folders", "crew_references"}
	descriptions := map[string]string{
		"crew_skills":     "Discover, search, install, import or uninstall skills in this Crew. Use settings to select installed skills. GitHub imports accept the same optional private-repository token as the website tool.",
		"crew_mcp":        "List connections/catalog, search, install/attach using the exact name returned by list, or catalog; for a new verified remote MCP pass name and url. Complete API key or OAuth app setup in chat using the install action's encrypted credential fields, return the real sign-in link, then verify discovery. Never repeat credentials. Removing detaches only this Crew, preserving other projects and logins. Use settings to select connections.",
		"crew_folders":    "List, attach or detach this account's additional server folders within administrator-assigned roots. This changes all of the account's Crews, so it requires consent for all Crews without a file guard.",
		"crew_references": "List accessible workflows and Crews with attached flags, or attach/detach an exact returned workspace_path. Workflow references are read-only; Crew references are shared read-write. Target access and connection bounds apply.",
	}
	for _, group := range groups {
		reg, err := api.crewCapabilityTools(group, "", "Chats/Work/projects/catalog")
		if err != nil {
			return err
		}
		props := map[string]any{"crew_id": externalString("Crew ID returned by crew list.")}
		actions := []string{}
		for action := range externalCrewCapabilities[group] {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		for _, action := range actions {
			member := externalCrewCapabilities[group][action]
			tool, ok := reg[member]
			if !ok {
				return fmt.Errorf("missing Crew capability %s", member)
			}
			for key, value := range tool.schema["properties"].(map[string]any) {
				props[key] = value
			}
		}
		actionValues := make([]any, len(actions))
		for i, action := range actions {
			actionValues[i] = action
		}
		props["action"] = map[string]any{"type": "string", "enum": actionValues}
		add(group, descriptions[group]+" Reads require crews:read. Changes require crews:write, a current editable account and Crew ownership. Executes directly without a Builder model turn.", true, false, props, "crew_id", "action")
	}
	return nil
}
func crewCapabilityRead(action string) bool {
	return action == "list" || action == "search" || action == "discover"
}

// Bulk package installation/removal must be allowed over the entire subtree;
// a grant on skills/ alone cannot bypass a denied child during extraction.
func crewCapabilitySubtreeAllowed(g *wf.FolderGuard, root string, write bool) bool {
	if !g.Allows(root, write) {
		return false
	}
	if g == nil {
		return true
	}
	exclusions := append([]string{}, g.BlockedPaths...)
	if write {
		exclusions = append(exclusions, g.BlockedWritePaths...)
		exclusions = append(exclusions, g.ReadOnlyPaths...)
	}
	for _, p := range exclusions {
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(root)+"/") {
			return false
		}
	}
	return true
}
func (api *StreamingAPI) externalCrewCapabilityCall(w http.ResponseWriter, r *http.Request, group string, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	action, id := externalArg(args, "action"), externalArg(args, "crew_id")
	member := externalCrewCapabilities[group][action]
	write := !crewCapabilityRead(action)
	token := claims.AccessToken
	if token != nil {
		scope := "crews:read"
		if write {
			scope = "crews:write"
		}
		if !token.Allows(scope) || !token.AllowsCrew(id) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow this action on this Crew.")
			return
		}
	}
	if write {
		if status, message := externalCrewAuthorGate(claims); status != 0 {
			externalError(w, status, "forbidden", message)
			return
		}
	}
	crew, manifest, _, ok := api.externalCrewResolve(r.Context(), claims, id)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed.")
		return
	}
	if write && !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew owner can change this configuration.")
		return
	}
	if group == "crew_folders" && (!crew.OwnedByCaller || token != nil && (!token.AllCrews || token.FileGuard != nil)) {
		externalError(w, 403, "insufficient_scope", "Account-wide folder access requires the Crew owner and consent for all Crews without a file guard.")
		return
	}
	root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	feature := map[string]string{"crew_skills": "skills", "crew_mcp": "mcp", "crew_folders": "attached-folders", "crew_references": "workflow-references"}[group]
	if !agentprofiles.HasFeature(profile, feature) || !containsExactString(profile.ToolPolicy.Enabled, member) {
		externalError(w, 404, "tool_unavailable", "This Crew profile does not enable this capability.")
		return
	}
	if group == "crew_skills" && action != "search" && action != "list" && !crewCapabilitySubtreeAllowed(externalFileGuard(r.Context(), true), "skills", write) {
		externalError(w, 403, "protected_path", "The connection's file guard does not allow this skills subtree.")
		return
	}
	if group == "crew_skills" && action == "uninstall" && token != nil && token.FileGuard != nil {
		externalError(w, 403, "protected_path", "Uninstalling across native skill locations requires unrestricted Crew file consent.")
		return
	}
	// Native-provider skill folders are included in the shared inventory; every
	// returned path must also satisfy the external file guard.
	if group == "crew_skills" && action == "list" && token != nil && token.FileGuard != nil {
		inventory, err := skills.DiscoverSkillsIn(getWorkspaceAPIURL(), root)
		if err != nil {
			externalFailure(w, err)
			return
		}
		permitted := inventory.Skills[:0]
		for _, skill := range inventory.Skills {
			p := strings.TrimPrefix(skill.FilePath, root+"/")
			if token.FileGuard.Allows(p, false) {
				permitted = append(permitted, skill)
			}
		}
		inventory.Skills = permitted
		usage := map[string][]string{}
		for _, skill := range permitted {
			if used, ok := inventory.Usage[skill.FolderName]; ok {
				usage[skill.FolderName] = used
			}
		}
		inventory.Usage = usage
		externalJSON(w, inventory)
		return
	}
	if group == "crew_references" && action == "attach" {
		if !api.externalCrewReferenceAllowed(r.Context(), claims, externalArg(args, "workspace_path"), true) {
			externalError(w, 403, "forbidden", "Reference is outside current access or this connection's bounds.")
			return
		}
	}
	if group == "crew_mcp" && action == "discover" {
		// Only a Vault app this Crew uses (its "Use all my apps automatically" setting, or its chosen apps). The
		// retired place attachments (PLAT-856-217) are no longer consulted.
		selected, chosenOnly, err := projectAppSelection(r.Context(), root)
		id := strings.TrimPrefix(strings.TrimSpace(externalArg(args, "name")), "vault_")
		if err != nil || id == "" || !projectVaultAppInUse(selected, chosenOnly, id) {
			externalError(w, 403, "forbidden", "Discovery is limited to the Vault apps this Crew uses.")
			return
		}
	}
	reg, err := api.crewCapabilityTools(group, claims.UserID, root, manifest.SessionID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	tool, ok := reg[member]
	if !ok {
		externalError(w, 400, "invalid_arguments", "Unknown capability action.")
		return
	}
	params := map[string]any{}
	for key, value := range args {
		if key != "crew_id" && key != "action" {
			params[key] = value
		}
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("crew-capability.json", tool.schema); err != nil {
		externalFailure(w, err)
		return
	}
	validator, err := compiler.Compile("crew-capability.json")
	if err != nil {
		externalFailure(w, err)
		return
	}
	if err = validator.Validate(params); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	ctx := context.WithValue(r.Context(), externalCrewMCPPlaceKey{}, root)
	result, err := tool.execute(ctx, params)
	if err != nil {
		externalError(w, 400, "capability_rejected", err.Error())
		return
	}
	var value any
	if json.Unmarshal([]byte(result), &value) != nil {
		value = result
	}
	if group == "crew_mcp" && action == "list" {
		if object, ok := value.(map[string]any); ok {
			for _, key := range []string{"vault", "vault_groups", "vault_secrets", "vault_users", "vault_error"} {
				delete(object, key)
			}
		}
	}
	if group == "crew_references" && action == "list" {
		value = externalCrewReferenceInventory(claims, value)
	}
	externalJSON(w, map[string]any{"crew_id": id, "action": action, "result": value})
}
func containsExactString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func (api *StreamingAPI) externalCrewReferenceAllowed(ctx context.Context, claims *UserClaims, p string, write bool) bool {
	token := claims.AccessToken
	if isCrewProjectPath(p) {
		crews, err := api.externalCrewsVisible(ctx, claims, "")
		if err != nil {
			return false
		}
		for _, entry := range crews {
			id, _ := entry["id"].(string)
			if id == "" {
				id, _ = entry["crew_id"].(string)
			}
			crew, _, _, ok := api.externalCrewResolve(ctx, claims, id)
			if !ok {
				continue
			}
			root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
			if canonicalChatHistoryWorkspacePath(claims.UserID, p) == canonicalChatHistoryWorkspacePath(claims.UserID, root) {
				// Crew references grant shared file writes in subsequent turns. A
				// read-only or path-narrowed token cannot grant that broader access.
				return token == nil || token.Allows("crews:read") && (!write || token.Allows("crews:write")) && token.FileGuard == nil
			}
		}
		return false
	}
	workflows, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return false
	}
	for _, workflow := range filterWorkflowManifestsForUser(claims, workflows) {
		if strings.TrimSuffix(workflow.WorkspacePath, "/") == strings.TrimSuffix(p, "/") {
			return token == nil || token.Allows("workflows:read") && token.Allows("files:read") && token.AllowsWorkflow(workflow.Manifest.ID) && token.FileGuard == nil
		}
	}
	return false
}

// The shared discovery executor has already checked current user/project access.
// Filter its result by token ceilings without rediscovering every project for
// each row (which would multiply remote workspace requests).
func externalCrewReferenceInventory(claims *UserClaims, value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	token := claims.AccessToken
	if token == nil {
		return object
	}
	for _, kind := range []string{"workflows", "crews"} {
		entries, _ := object[kind].([]any)
		kept := []any{}
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := entry["id"].(string)
			allowed := token.FileGuard == nil
			if kind == "crews" {
				allowed = allowed && token.Allows("crews:read") && token.AllowsCrew(id)
			} else {
				allowed = allowed && token.Allows("workflows:read") && token.Allows("files:read") && token.AllowsWorkflow(id)
			}
			if allowed {
				kept = append(kept, entry)
			}
		}
		object[kind] = kept
	}
	return object
}
