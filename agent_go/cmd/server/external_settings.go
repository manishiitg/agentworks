package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Workflow and Relay settings over MCP: the fields the app's settings panels
// save, saved through the same manifest handler and its owner/editor check.
// Every choice the panels only offer from a list (model accounts, MCP servers,
// skills, shared secrets) is checked against what the caller may use, since an
// MCP client can send anything. Secret values are write-only and, as in the
// app, owner-only; they are never returned.

func isExternalSettingsTool(name string) bool {
	return name == "get_settings" || name == "update_settings"
}

func externalSettingsDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	names := map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}}
	addRemove := func(what string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "description": what,
			"properties": map[string]any{"add": names, "remove": names}}
	}
	add("get_settings", "Read a workflow's or Relay's settings: models per role, MCP servers and tools, skills, secrets (names and whether a value is stored; never values), browser mode and notifications, plus a version for update_settings.", false, true, nil)
	add("update_settings", "Change a workflow's or Relay's settings. Send only what changes. Requires owner or editor access; setting or removing secret values requires owner. MCP servers come from your own connections, shared Vault connections the server allows you, or the catalog; one that still needs a sign-in is attached and listed under pending_sign_in. Returns the new settings.", true, true, map[string]any{
		"expected_version": externalString("Version from get_settings; the update is refused if the settings changed since."),
		"models":           map[string]any{"type": "object", "description": "The models object from get_settings with your changes. Replaces it whole; every account (connection_id) must be one you may use."},
		"mcp_servers":      addRemove("MCP servers to attach or detach, by name."),
		"tools":            addRemove("Tools to allow or disallow, as server:tool. The server must be attached."),
		"skills":           addRemove("Installed skills to use or stop using."),
		"secrets": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"set":         map[string]any{"type": "object", "maxProperties": 20, "additionalProperties": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536}, "description": "NAME: value. Stores the value (write-only, owner only) and selects the secret."},
			"remove":      names,
			"select":      names,
			"unselect":    names,
			"use_shared":  names,
			"stop_shared": names,
		}, "description": "set/remove store or delete this workflow's secret values (owner only); select/unselect choose stored secrets; use_shared/stop_shared choose shared (global) secrets by name."},
		"browser_mode": map[string]any{"type": "string", "enum": []any{"auto", "headless", "none", "cdp"}},
		"notifications": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"run_instructions": map[string]any{"type": "string", "maxLength": 4000}, "pulse_instructions": map[string]any{"type": "string", "maxLength": 4000},
			"run_channels": names, "pulse_channels": names,
		}},
	})
}

type externalSettingsSecret struct {
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
}

func externalSettingsVersion(caps WorkflowCapabilities) string {
	encoded, _ := json.Marshal(caps)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

func (api *StreamingAPI) externalSettingsView(ctx context.Context, userID string, manifest *WorkflowManifest, workspacePath string, access WorkflowAccessLevel) map[string]any {
	caps := manifest.Capabilities
	kind := "workflow"
	if manifest.Kind == "relay" {
		kind = "relay"
	}
	stored := []externalSettingsSecret{}
	if rows, err := api.ensureSharedWorkflowSecrets(ctx, workspacePath, userID); err == nil {
		for _, row := range rows {
			stored = append(stored, externalSettingsSecret{Name: row.Name, Selected: slices.Contains(caps.SelectedSecrets, row.Name)})
		}
	}
	shared := []string{}
	if caps.SelectedGlobalSecretNames != nil {
		shared = append(shared, (*caps.SelectedGlobalSecretNames)...)
	}
	available := []string{}
	for _, secret := range visibleGlobalSecrets(ctx, userID) {
		available = append(available, secret.Name)
	}
	notifications := map[string]any{}
	if n := caps.Notifications; n != nil {
		notifications = map[string]any{"run_instructions": n.RunSummaryInstructions, "pulse_instructions": n.PulseSummaryInstructions,
			"run_channels": n.RunSummaryChannels, "pulse_channels": n.PulseSummaryChannels}
	}
	return map[string]any{
		"kind": kind, "version": externalSettingsVersion(caps), "my_access": access,
		"models": caps.LLMConfig, "mcp_servers": nonNilStrings(caps.SelectedServers), "tools": nonNilStrings(caps.SelectedTools),
		"skills": nonNilStrings(caps.SelectedSkills),
		"secrets": map[string]any{"stored": stored, "shared_selected": shared, "shared_available": available,
			"note": "Values are never returned. Only owners may set or remove values."},
		"browser_mode": caps.BrowserMode, "notifications": notifications,
	}
}

func externalSettingsList(args map[string]any, field, key string) []string {
	group, _ := args[field].(map[string]any)
	raw, _ := group[key].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s := strings.TrimSpace(fmt.Sprint(item)); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func settingsWithout(values, drop []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !slices.ContainsFunc(drop, func(d string) bool { return strings.EqualFold(d, value) }) {
			out = append(out, value)
		}
	}
	return out
}

func settingsWith(values []string, add ...string) []string {
	out := append([]string{}, values...)
	for _, value := range add {
		if !slices.ContainsFunc(out, func(v string) bool { return strings.EqualFold(v, value) }) {
			out = append(out, value)
		}
	}
	return out
}

func (api *StreamingAPI) externalSettingsCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any, workflow DiscoveredWorkflow, access WorkflowAccessLevel) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	manifest, exists, err := ReadWorkflowManifest(ctx, workflow.WorkspacePath)
	if err != nil || !exists || manifest == nil {
		externalError(w, 502, "workspace_unavailable", "Workflow settings could not be read.")
		return
	}
	if name == "get_settings" {
		externalJSON(w, api.externalSettingsView(ctx, claims.UserID, manifest, workflow.WorkspacePath, access))
		return
	}
	if expected := externalArg(args, "expected_version"); expected != "" && expected != externalSettingsVersion(manifest.Capabilities) {
		externalError(w, 409, "version_conflict", "Settings changed since get_settings; read them again and reapply your change.")
		return
	}
	fail := func(format string, a ...any) { externalError(w, 400, "invalid_settings", fmt.Sprintf(format, a...)) }
	caps := manifest.Capabilities
	caps.SelectedServers = append([]string{}, caps.SelectedServers...)
	caps.SelectedTools = append([]string{}, caps.SelectedTools...)
	caps.SelectedSkills = append([]string{}, caps.SelectedSkills...)
	caps.SelectedSecrets = append([]string{}, caps.SelectedSecrets...)
	changed := []string{}
	var pending []PendingConnection

	// Models: every named account must be one this person may use here.
	if raw, ok := args["models"]; ok {
		encoded, _ := json.Marshal(raw)
		var config workflowtypes.PresetLLMConfig
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			fail("models: %v", err)
			return
		}
		pins := []*workflowtypes.AgentLLMConfig{config.BuilderLLM, config.PulseLLM}
		if config.TieredConfig != nil {
			pins = append(pins, config.TieredConfig.Tier1, config.TieredConfig.Tier2, config.TieredConfig.Tier3)
		}
		if config.ConnectionID != "" {
			pins = append(pins, &workflowtypes.AgentLLMConfig{Provider: config.Provider, ConnectionID: config.ConnectionID})
		}
		scope := providerAccountScope{Principal: claims.UserID, WorkspacePath: workflow.WorkspacePath, Product: productWorkflows}
		for _, pin := range pins {
			if pin == nil || pin.ConnectionID == "" {
				continue
			}
			if _, err := api.admitProviderAccount(ctx, scope, pin.Provider, pin.ConnectionID); err != nil {
				fail("models: account %s (%s) cannot be used: %v", pin.ConnectionID, pin.Provider, err)
				return
			}
		}
		caps.LLMConfig = &config
		changed = append(changed, "models")
	}

	if adds := externalSettingsList(args, "mcp_servers", "add"); len(adds) > 0 {
		if api.productSchedules == nil {
			externalError(w, 503, "mcp_unavailable", "MCP configuration is unavailable.")
			return
		}
		canonical, waiting, err := api.productSchedules.validateCrewCreationServers(ctx, claims.UserID, adds)
		if err != nil {
			fail("mcp_servers: %v", err)
			return
		}
		caps.SelectedServers = settingsWith(caps.SelectedServers, canonical...)
		for _, p := range waiting {
			p.Reason = "attached, but you have not signed in to it yet; sign in from the app's Integrations panel"
			pending = append(pending, p)
		}
		changed = append(changed, "mcp_servers")
	}
	if drops := externalSettingsList(args, "mcp_servers", "remove"); len(drops) > 0 {
		caps.SelectedServers = settingsWithout(caps.SelectedServers, drops)
		kept := caps.SelectedTools[:0]
		for _, tool := range caps.SelectedTools {
			server, _, _ := strings.Cut(tool, ":")
			if !slices.ContainsFunc(drops, func(d string) bool { return strings.EqualFold(d, server) }) {
				kept = append(kept, tool)
			}
		}
		caps.SelectedTools = kept
		changed = settingsWith(changed, "mcp_servers")
	}
	if adds := externalSettingsList(args, "tools", "add"); len(adds) > 0 {
		for _, tool := range adds {
			server, toolName, ok := strings.Cut(tool, ":")
			if !ok || server == "" || toolName == "" {
				fail("tools: %q must be server:tool", tool)
				return
			}
			if !slices.ContainsFunc(caps.SelectedServers, func(s string) bool { return strings.EqualFold(s, server) }) {
				fail("tools: server %q is not attached; add it under mcp_servers first", server)
				return
			}
		}
		caps.SelectedTools = settingsWith(caps.SelectedTools, adds...)
		changed = append(changed, "tools")
	}
	if drops := externalSettingsList(args, "tools", "remove"); len(drops) > 0 {
		caps.SelectedTools = settingsWithout(caps.SelectedTools, drops)
		changed = settingsWith(changed, "tools")
	}

	if adds := externalSettingsList(args, "skills", "add"); len(adds) > 0 {
		read := skills.NewInstalledSkillReader(getWorkspaceAPIURL(), workflow.WorkspacePath)
		for _, skill := range adds {
			if _, err := read(skill, "SKILL.md"); err != nil {
				fail("skills: %q is not installed in this workflow", skill)
				return
			}
		}
		caps.SelectedSkills = settingsWith(caps.SelectedSkills, adds...)
		changed = append(changed, "skills")
	}
	if drops := externalSettingsList(args, "skills", "remove"); len(drops) > 0 {
		caps.SelectedSkills = settingsWithout(caps.SelectedSkills, drops)
		changed = settingsWith(changed, "skills")
	}

	// Secrets: validate everything before storing any value.
	secretArgs, _ := args["secrets"].(map[string]any)
	values := map[string]string{}
	if set, ok := secretArgs["set"].(map[string]any); ok {
		for key, value := range set {
			values[strings.TrimSpace(key)], _ = value.(string)
		}
	}
	removeValues := externalSettingsList(args, "secrets", "remove")
	if len(values) > 0 || len(removeValues) > 0 {
		if access != WorkflowAccessOwner {
			externalError(w, 403, "forbidden", "Only an owner can set or remove secret values.")
			return
		}
		named := append([]string{}, removeValues...)
		for key := range values {
			named = append(named, key)
		}
		if _, err := validateCrewCreationNames("secret", named); err != nil {
			fail("secrets: %v", err)
			return
		}
	}
	storedNames := map[string]bool{}
	if rows, err := api.ensureSharedWorkflowSecrets(ctx, workflow.WorkspacePath, claims.UserID); err == nil {
		for _, row := range rows {
			storedNames[row.Name] = true
		}
	}
	selects := externalSettingsList(args, "secrets", "select")
	for _, secret := range selects {
		if _, setting := values[secret]; !storedNames[secret] && !setting {
			fail("secrets: %q has no stored value; set it first", secret)
			return
		}
	}
	shared := externalSettingsList(args, "secrets", "use_shared")
	if len(shared) > 0 {
		if api.productSchedules == nil {
			externalError(w, 503, "secrets_unavailable", "Shared secrets are unavailable.")
			return
		}
		if _, err := api.productSchedules.validateCrewCreationGlobalSecrets(ctx, claims.UserID, shared); err != nil {
			fail("secrets: %v", err)
			return
		}
	}
	for key, value := range values {
		if err := api.upsertSharedWorkflowSecret(ctx, workflow.WorkspacePath, key, value); err != nil {
			externalError(w, 502, "secret_store_failed", fmt.Sprintf("Could not store secret %s.", key))
			return
		}
		selects = settingsWith(selects, key)
	}
	for _, key := range removeValues {
		if err := api.deleteSharedWorkflowSecret(ctx, workflow.WorkspacePath, key, claims.UserID); err != nil {
			externalError(w, 502, "secret_store_failed", fmt.Sprintf("Could not remove secret %s.", key))
			return
		}
	}
	if len(selects) > 0 {
		caps.SelectedSecrets = settingsWith(caps.SelectedSecrets, selects...)
	}
	if drops := append(externalSettingsList(args, "secrets", "unselect"), removeValues...); len(drops) > 0 {
		caps.SelectedSecrets = settingsWithout(caps.SelectedSecrets, drops)
	}
	if stop := externalSettingsList(args, "secrets", "stop_shared"); len(shared) > 0 || len(stop) > 0 {
		current := []string{}
		if caps.SelectedGlobalSecretNames != nil {
			current = *caps.SelectedGlobalSecretNames
		}
		next := settingsWithout(settingsWith(current, shared...), stop)
		caps.SelectedGlobalSecretNames = &next
	}
	if secretArgs != nil {
		changed = append(changed, "secrets")
	}

	if mode := externalArg(args, "browser_mode"); mode != "" {
		caps.BrowserMode = mode
		changed = append(changed, "browser_mode")
	}

	req := UpdateWorkflowManifestRequest{WorkspacePath: workflow.WorkspacePath, Capabilities: &caps}
	if n, ok := args["notifications"].(map[string]any); ok {
		if v, ok := n["run_instructions"].(string); ok {
			req.RunNotificationInstructions = &v
		}
		if v, ok := n["pulse_instructions"].(string); ok {
			req.PulseNotificationInstructions = &v
		}
		if _, ok := n["run_channels"]; ok {
			v := externalSettingsList(args, "notifications", "run_channels")
			req.RunNotificationChannels = &v
		}
		if _, ok := n["pulse_channels"]; ok {
			v := externalSettingsList(args, "notifications", "pulse_channels")
			req.PulseNotificationChannels = &v
		}
		changed = append(changed, "notifications")
	}
	if len(changed) == 0 {
		fail("nothing to change; send at least one setting")
		return
	}

	// Save through the app's own handler and its write check.
	body, _ := json.Marshal(req)
	save := httptest.NewRequest(http.MethodPut, "/api/workflows/manifest", bytes.NewReader(body)).WithContext(ctx)
	save.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	requireWorkflowWriteAccess(api.handleUpdateWorkflowManifest)(recorder, save)
	if recorder.Code != http.StatusOK {
		externalError(w, recorder.Code, "settings_rejected", strings.TrimSpace(recorder.Body.String()))
		return
	}
	log.Printf("[EXTERNAL_SETTINGS] user=%s workflow=%s changed=%s secrets_set=%d secrets_removed=%d", claims.UserID, manifest.ID, strings.Join(changed, ","), len(values), len(removeValues))

	updated, _, err := ReadWorkflowManifest(ctx, workflow.WorkspacePath)
	if err != nil || updated == nil {
		updated = manifest
		updated.Capabilities = caps
	}
	view := api.externalSettingsView(ctx, claims.UserID, updated, workflow.WorkspacePath, access)
	view["changed"] = changed
	if len(pending) > 0 {
		view["pending_sign_in"] = pending
	}
	externalJSON(w, view)
}
