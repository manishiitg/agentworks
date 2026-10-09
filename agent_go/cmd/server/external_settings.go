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
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Workflow, Relay and Crew settings over MCP: the fields the app's settings
// panels save. Workflows and Relays save through the app's manifest handler
// and its owner/editor check; Crews rewrite the Crew's runtime manifest like
// the Crew panels, owner only (as update_crew). Every choice the panels only
// offer from a list (model accounts, MCP servers, skills, shared secrets) is
// checked against what the caller may use, since an MCP client can send
// anything. Secret values are write-only and owner-only; never returned.

func isExternalSettingsTool(name string) bool {
	return name == "get_settings" || name == "update_settings"
}

func externalSettingsDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	names := map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}}
	addRemove := func(what string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "description": what,
			"properties": map[string]any{"add": names, "remove": names}}
	}
	target := func() map[string]any {
		return map[string]any{
			"workflow_id": externalString("Workflow or Relay ID from list_workflows. Pass this or crew_id."),
			"crew_id":     externalString("Crew ID from list_crews. Pass this or workflow_id."),
		}
	}
	add("get_settings", "Read a workflow's, Relay's or Crew's settings: models per role, MCP servers and tools, skills, secrets (names and whether a value is stored; never values), browser mode, notifications (workflows and Relays), plus a version for update_settings.", false, false, target())
	props := target()
	for key, value := range map[string]any{
		"expected_version": externalString("Version from settings action=get; the update is refused if the settings changed since."),
		"models":           map[string]any{"type": "object", "description": "The models object from get_settings with your changes. Replaces it whole; every account (connection_id) must be one you may use."},
		"mcp_servers":      addRemove("MCP servers to attach or detach, by name."),
		"tools":            addRemove("Tools to allow or disallow, as server:tool. The server must be attached."),
		"skills":           addRemove("Installed or built-in skills to use or stop using."),
		"secrets": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"set":         map[string]any{"type": "object", "maxProperties": 20, "additionalProperties": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536}, "description": "NAME: value. Stores the value (write-only, owner only) and selects the secret."},
			"remove":      names,
			"select":      names,
			"unselect":    names,
			"use_shared":  names,
			"stop_shared": names,
		}, "description": "set/remove store or delete secret values (owner only); select/unselect choose stored secrets; use_shared/stop_shared choose shared (global) secrets by name."},
		"browser_mode": map[string]any{"type": "string", "enum": []any{"auto", "headless", "none", "cdp"}},
		"after_manual_run": map[string]any{"type": "object", "additionalProperties": false, "description": "Workflows only: what runs after a full run you start yourself (schedules have their own after_run in manage_schedules). Backup and publish need to be set up first (in the app's Builder: /backup, /publish).", "properties": map[string]any{
			"backup": map[string]any{"type": "boolean"}, "publish": map[string]any{"type": "boolean"}, "notify": map[string]any{"type": "boolean"},
		}},
		"pulse": map[string]any{"type": "object", "additionalProperties": false, "description": "Workflows only. Pulse owns the goal when enabled (it needs soul/soul.md; until then it waits for a goal). autonomy_level 0-5 is the standing permission ladder; pace sets how often Pulse checks and fixes.", "properties": map[string]any{
			"enabled":        map[string]any{"type": "boolean"},
			"autonomy_level": map[string]any{"type": "integer", "minimum": 0, "maximum": 5},
			"pace":           map[string]any{"type": "string", "enum": []any{"calm", "steady", "aggressive"}},
		}},
		"notifications": map[string]any{"type": "object", "additionalProperties": false, "description": "Workflows and Relays only.", "properties": map[string]any{
			"run_instructions": map[string]any{"type": "string", "maxLength": 4000}, "pulse_instructions": map[string]any{"type": "string", "maxLength": 4000},
			"run_channels": names, "pulse_channels": names,
		}},
	} {
		props[key] = value
	}
	add("update_settings", "Change a workflow's, Relay's or Crew's settings. Send only what changes. Workflows and Relays need owner or editor access, Crews their owner; setting or removing secret values needs owner. MCP servers come from your own connections, shared Vault connections the server allows you, or the catalog; one that still needs a sign-in is attached and listed under pending_sign_in. Returns the new settings.", true, false, props)
}

// externalSettingsState is the part of a workflow's or Crew's capabilities
// these tools read and change.
type externalSettingsState struct {
	LLMConfig     *workflowtypes.PresetLLMConfig `json:"llm_config,omitempty"`
	Servers       []string                       `json:"selected_servers,omitempty"`
	Tools         []string                       `json:"selected_tools,omitempty"`
	Skills        []string                       `json:"selected_skills,omitempty"`
	Secrets       []string                       `json:"selected_secrets,omitempty"`
	GlobalSecrets *[]string                      `json:"selected_global_secret_names,omitempty"`
	BrowserMode   string                         `json:"browser_mode,omitempty"`
}

func (s externalSettingsState) version() string {
	encoded, _ := json.Marshal(s)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

type externalSettingsTarget struct {
	kind       string // workflow, relay or crew
	path       string
	access     string
	owner      bool
	product    string
	skillCheck func(context.Context, []string) error
}

type externalSettingsSecret struct {
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
}

func (api *StreamingAPI) externalSettingsView(ctx context.Context, userID string, t externalSettingsTarget, s externalSettingsState, notifications map[string]any) map[string]any {
	stored := []externalSettingsSecret{}
	if rows, err := api.ensureSharedWorkflowSecrets(ctx, t.path, userID); err == nil {
		for _, row := range rows {
			stored = append(stored, externalSettingsSecret{Name: row.Name, Selected: slices.Contains(s.Secrets, row.Name)})
		}
	}
	shared := []string{}
	if s.GlobalSecrets != nil {
		shared = append(shared, (*s.GlobalSecrets)...)
	}
	available := []string{}
	for _, secret := range visibleGlobalSecrets(ctx, userID) {
		available = append(available, secret.Name)
	}
	view := map[string]any{
		"kind": t.kind, "version": s.version(), "my_access": t.access,
		"models": s.LLMConfig, "mcp_servers": nonNilStrings(s.Servers), "tools": nonNilStrings(s.Tools),
		"skills": nonNilStrings(s.Skills),
		"secrets": map[string]any{"stored": stored, "shared_selected": shared, "shared_available": available,
			"note": "Values are never returned. Only owners may set or remove values."},
		"browser_mode": s.BrowserMode,
	}
	if notifications != nil {
		view["notifications"] = notifications
	}
	return view
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

type externalSettingsError struct {
	status  int
	code    string
	message string
}

func settingsInvalid(format string, a ...any) *externalSettingsError {
	return &externalSettingsError{400, "invalid_settings", fmt.Sprintf(format, a...)}
}

// applyExternalSettings validates the requested changes against what the
// caller may use, stores secret values, and applies everything to s. It
// returns which settings changed and the servers still waiting for sign-in.
func (api *StreamingAPI) applyExternalSettings(ctx context.Context, userID string, t externalSettingsTarget, s *externalSettingsState, args map[string]any) ([]string, []PendingConnection, *externalSettingsError) {
	changed := []string{}
	var pending []PendingConnection

	// Models: every named account must be one this person may use here.
	if raw, ok := args["models"]; ok {
		encoded, _ := json.Marshal(raw)
		var config workflowtypes.PresetLLMConfig
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return nil, nil, settingsInvalid("models: %v", err)
		}
		pins := []*workflowtypes.AgentLLMConfig{config.BuilderLLM, config.PulseLLM}
		if config.TieredConfig != nil {
			pins = append(pins, config.TieredConfig.Tier1, config.TieredConfig.Tier2, config.TieredConfig.Tier3)
		}
		if config.ConnectionID != "" {
			pins = append(pins, &workflowtypes.AgentLLMConfig{Provider: config.Provider, ConnectionID: config.ConnectionID})
		}
		scope := providerAccountScope{Principal: userID, WorkspacePath: t.path, Product: t.product}
		for _, pin := range pins {
			if pin == nil || pin.ConnectionID == "" {
				continue
			}
			if _, err := api.admitProviderAccount(ctx, scope, pin.Provider, pin.ConnectionID); err != nil {
				return nil, nil, settingsInvalid("models: account %s (%s) cannot be used: %v", pin.ConnectionID, pin.Provider, err)
			}
		}
		s.LLMConfig = &config
		changed = append(changed, "models")
	}

	if adds := externalSettingsList(args, "mcp_servers", "add"); len(adds) > 0 {
		if api.productSchedules == nil {
			return nil, nil, &externalSettingsError{503, "mcp_unavailable", "MCP configuration is unavailable."}
		}
		canonical, waiting, err := api.productSchedules.validateCrewCreationServers(ctx, userID, adds)
		if err != nil {
			return nil, nil, settingsInvalid("mcp_servers: %v", err)
		}
		s.Servers = settingsWith(s.Servers, canonical...)
		for _, p := range waiting {
			p.Reason = "attached, but you have not signed in to it yet; sign in from the app's Integrations panel"
			pending = append(pending, p)
		}
		changed = append(changed, "mcp_servers")
	}
	if drops := externalSettingsList(args, "mcp_servers", "remove"); len(drops) > 0 {
		s.Servers = settingsWithout(s.Servers, drops)
		kept := []string{}
		for _, tool := range s.Tools {
			server, _, _ := strings.Cut(tool, ":")
			if !slices.ContainsFunc(drops, func(d string) bool { return strings.EqualFold(d, server) }) {
				kept = append(kept, tool)
			}
		}
		s.Tools = kept
		changed = settingsWith(changed, "mcp_servers")
	}
	if adds := externalSettingsList(args, "tools", "add"); len(adds) > 0 {
		for _, tool := range adds {
			server, toolName, ok := strings.Cut(tool, ":")
			if !ok || server == "" || toolName == "" {
				return nil, nil, settingsInvalid("tools: %q must be server:tool", tool)
			}
			if !slices.ContainsFunc(s.Servers, func(name string) bool { return strings.EqualFold(name, server) }) {
				return nil, nil, settingsInvalid("tools: server %q is not attached; add it under mcp_servers first", server)
			}
		}
		s.Tools = settingsWith(s.Tools, adds...)
		changed = append(changed, "tools")
	}
	if drops := externalSettingsList(args, "tools", "remove"); len(drops) > 0 {
		s.Tools = settingsWithout(s.Tools, drops)
		changed = settingsWith(changed, "tools")
	}

	if adds := externalSettingsList(args, "skills", "add"); len(adds) > 0 {
		if err := t.skillCheck(ctx, adds); err != nil {
			return nil, nil, settingsInvalid("skills: %v", err)
		}
		s.Skills = settingsWith(s.Skills, adds...)
		changed = append(changed, "skills")
	}
	if drops := externalSettingsList(args, "skills", "remove"); len(drops) > 0 {
		s.Skills = settingsWithout(s.Skills, drops)
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
		if !t.owner {
			return nil, nil, &externalSettingsError{403, "forbidden", "Only an owner can set or remove secret values."}
		}
		named := append([]string{}, removeValues...)
		for key := range values {
			named = append(named, key)
		}
		if _, err := validateCrewCreationNames("secret", named); err != nil {
			return nil, nil, settingsInvalid("secrets: %v", err)
		}
	}
	storedNames := map[string]bool{}
	if rows, err := api.ensureSharedWorkflowSecrets(ctx, t.path, userID); err == nil {
		for _, row := range rows {
			storedNames[row.Name] = true
		}
	}
	selects := externalSettingsList(args, "secrets", "select")
	for _, secret := range selects {
		if _, setting := values[secret]; !storedNames[secret] && !setting {
			return nil, nil, settingsInvalid("secrets: %q has no stored value; set it first", secret)
		}
	}
	shared := externalSettingsList(args, "secrets", "use_shared")
	if len(shared) > 0 {
		if api.productSchedules == nil {
			return nil, nil, &externalSettingsError{503, "secrets_unavailable", "Shared secrets are unavailable."}
		}
		if _, err := api.productSchedules.validateCrewCreationGlobalSecrets(ctx, userID, shared); err != nil {
			return nil, nil, settingsInvalid("secrets: %v", err)
		}
	}
	for key, value := range values {
		if err := api.upsertSharedWorkflowSecret(ctx, t.path, key, value); err != nil {
			return nil, nil, &externalSettingsError{502, "secret_store_failed", fmt.Sprintf("Could not store secret %s.", key)}
		}
		selects = settingsWith(selects, key)
	}
	for _, key := range removeValues {
		if err := api.deleteSharedWorkflowSecret(ctx, t.path, key, userID); err != nil {
			return nil, nil, &externalSettingsError{502, "secret_store_failed", fmt.Sprintf("Could not remove secret %s.", key)}
		}
	}
	if len(selects) > 0 {
		s.Secrets = settingsWith(s.Secrets, selects...)
	}
	if drops := append(externalSettingsList(args, "secrets", "unselect"), removeValues...); len(drops) > 0 {
		s.Secrets = settingsWithout(s.Secrets, drops)
	}
	if stop := externalSettingsList(args, "secrets", "stop_shared"); len(shared) > 0 || len(stop) > 0 {
		current := []string{}
		if s.GlobalSecrets != nil {
			current = *s.GlobalSecrets
		}
		next := settingsWithout(settingsWith(current, shared...), stop)
		s.GlobalSecrets = &next
	}
	if secretArgs != nil {
		changed = append(changed, "secrets")
	}

	if mode := externalArg(args, "browser_mode"); mode != "" {
		if err := enforceDeploymentBrowserCapability(&WorkflowCapabilities{BrowserMode: mode}); err != nil {
			return nil, nil, settingsInvalid("browser_mode: %v", err)
		}
		s.BrowserMode = mode
		changed = append(changed, "browser_mode")
	}
	if len(values) > 0 || len(removeValues) > 0 {
		log.Printf("[EXTERNAL_SETTINGS] user=%s path=%s secrets_set=%d secrets_removed=%d", userID, t.path, len(values), len(removeValues))
	}
	return changed, pending, nil
}

func externalSettingsWorkflowSkillCheck(path string) func(context.Context, []string) error {
	return func(_ context.Context, names []string) error {
		read := skills.NewInstalledSkillReader(getWorkspaceAPIURL(), path)
		for _, name := range names {
			if skills.IsBuiltinSkill(name) {
				continue
			}
			if _, err := read(name, "SKILL.md"); err != nil {
				return fmt.Errorf("%q is not installed in this workflow", name)
			}
		}
		return nil
	}
}

func (api *StreamingAPI) externalSettingsCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any, workflow DiscoveredWorkflow, access WorkflowAccessLevel) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	if t := claims.AccessToken; t != nil && !t.Allows("workflows:read") && !t.Allows("runs:execute") {
		externalError(w, 403, "insufficient_scope", "This connection cannot read workflows.")
		return
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workflow.WorkspacePath)
	if err != nil || !exists || manifest == nil {
		externalError(w, 502, "workspace_unavailable", "Workflow settings could not be read.")
		return
	}
	target := externalSettingsTarget{kind: "workflow", path: workflow.WorkspacePath, access: string(access),
		owner: access == WorkflowAccessOwner, product: productWorkflows, skillCheck: externalSettingsWorkflowSkillCheck(workflow.WorkspacePath)}
	if manifest.Kind == "relay" {
		target.kind = "relay"
	}
	caps := manifest.Capabilities
	state := externalSettingsState{LLMConfig: caps.LLMConfig, Servers: append([]string{}, caps.SelectedServers...),
		Tools: append([]string{}, caps.SelectedTools...), Skills: append([]string{}, caps.SelectedSkills...),
		Secrets: append([]string{}, caps.SelectedSecrets...), GlobalSecrets: caps.SelectedGlobalSecretNames, BrowserMode: caps.BrowserMode}
	notifications := func(c WorkflowCapabilities) map[string]any {
		if n := c.Notifications; n != nil {
			return map[string]any{"run_instructions": n.RunSummaryInstructions, "pulse_instructions": n.PulseSummaryInstructions,
				"run_channels": n.RunSummaryChannels, "pulse_channels": n.PulseSummaryChannels}
		}
		return map[string]any{}
	}
	pulseView := func(m *WorkflowManifest) map[string]any {
		view := map[string]any{"enabled": m.PulseEnabled(), "has_goal": workflowHasSoul(ctx, workflow.WorkspacePath)}
		if m.Pulse != nil {
			view["pace"] = m.Pulse.Pace
			if m.Pulse.Autonomy != nil && m.Pulse.Autonomy.Level != nil {
				view["autonomy_level"] = *m.Pulse.Autonomy.Level
			}
		}
		return view
	}
	if name == "get_settings" {
		view := api.externalSettingsView(ctx, claims.UserID, target, state, notifications(caps))
		view["pulse"] = pulseView(manifest)
		view["after_manual_run"] = manifest.EffectiveManualAfterRun()
		// Backup, publish and notify status, as the app's views show them.
		for key, handler := range map[string]http.HandlerFunc{"backup": api.handleGetWorkflowBackup, "publish": api.handleGetWorkflowPublish, "notify": api.handleGetWorkflowNotifications} {
			if status, body := scheduleHandler(r, handler, http.MethodGet, "/api/workflow/"+key, nil, url.Values{"workspace_path": {workflow.WorkspacePath}}, nil); status == http.StatusOK && json.Valid(body) {
				view[key] = json.RawMessage(body)
			}
		}
		externalJSON(w, view)
		return
	}
	if expected := externalArg(args, "expected_version"); expected != "" && expected != state.version() {
		externalError(w, 409, "version_conflict", "Settings changed since you read them; read them again (settings action=get) and reapply your change.")
		return
	}
	changed, pending, failure := api.applyExternalSettings(ctx, claims.UserID, target, &state, args)
	if failure != nil {
		externalError(w, failure.status, failure.code, failure.message)
		return
	}
	caps.LLMConfig, caps.SelectedServers, caps.SelectedTools, caps.SelectedSkills = state.LLMConfig, state.Servers, state.Tools, state.Skills
	caps.SelectedSecrets, caps.SelectedGlobalSecretNames, caps.BrowserMode = state.Secrets, state.GlobalSecrets, state.BrowserMode
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
	if after, ok := args["after_manual_run"].(map[string]any); ok {
		current := manifest.EffectiveManualAfterRun()
		for key, target := range map[string]*bool{"backup": &current.Backup, "publish": &current.Publish, "notify": &current.Notify} {
			if v, ok := after[key].(bool); ok {
				*target = v
			}
		}
		req.AfterManualRun = &current
		changed = append(changed, "after_manual_run")
	}
	if p, ok := args["pulse"].(map[string]any); ok {
		if v, ok := p["enabled"].(bool); ok {
			req.PulseEnabled = &v
		}
		if v, ok := p["autonomy_level"].(float64); ok {
			level := int(v)
			req.PulseAutonomyLevel = &level
		}
		if v, ok := p["pace"].(string); ok {
			req.PulsePace = &v
		}
		changed = append(changed, "pulse")
	}
	if len(changed) == 0 {
		externalError(w, 400, "invalid_settings", "Nothing to change; send at least one setting.")
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
	log.Printf("[EXTERNAL_SETTINGS] user=%s workflow=%s changed=%s", claims.UserID, manifest.ID, strings.Join(changed, ","))

	if updated, _, err := ReadWorkflowManifest(ctx, workflow.WorkspacePath); err == nil && updated != nil {
		caps = updated.Capabilities
		manifest = updated
	}
	view := api.externalSettingsView(ctx, claims.UserID, target, externalSettingsState{LLMConfig: caps.LLMConfig, Servers: caps.SelectedServers,
		Tools: caps.SelectedTools, Skills: caps.SelectedSkills, Secrets: caps.SelectedSecrets, GlobalSecrets: caps.SelectedGlobalSecretNames,
		BrowserMode: caps.BrowserMode}, notifications(caps))
	view["changed"] = changed
	view["pulse"] = pulseView(manifest)
	if len(pending) > 0 {
		view["pending_sign_in"] = pending
	}
	externalJSON(w, view)
}

// externalCrewSettingsCall reads or changes a Crew's settings in its runtime
// manifest, the file the Crew's Models and Integrations panels write.
func (api *StreamingAPI) externalCrewSettingsCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	crewID := externalArg(args, "crew_id")
	if t := claims.AccessToken; t != nil {
		scope := "crews:read"
		if name == "update_settings" {
			scope = "crews:write"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+name+" on this Crew.")
			return
		}
	}
	crew, _, _, ok := api.externalCrewResolve(ctx, claims, crewID)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	root := crew.Binding.WorkspacePath
	raw, manifestPath, err := ensureProjectRuntimeManifest(ctx, "work", root)
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Crew settings could not be read.")
		return
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		externalError(w, 502, "workspace_unavailable", "Crew settings are not valid JSON.")
		return
	}
	capabilities, _ := manifest["capabilities"].(map[string]any)
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	var state externalSettingsState
	if encoded, err := json.Marshal(capabilities); err == nil {
		_ = json.Unmarshal(encoded, &state)
	}
	access := "member"
	if crew.OwnedByCaller {
		access = "owner"
	}
	target := externalSettingsTarget{kind: "crew", path: root, access: access, owner: crew.OwnedByCaller, product: productCrews,
		skillCheck: func(ctx context.Context, names []string) error {
			return checkCrewSkillsAvailable(ctx, root, names, nil)
		}}
	if name == "get_settings" {
		externalJSON(w, api.externalSettingsView(ctx, claims.UserID, target, state, nil))
		return
	}
	if !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew's owner can change its settings.")
		return
	}
	if _, ok := args["notifications"]; ok {
		externalError(w, 400, "invalid_settings", "notifications apply to workflows and Relays only.")
		return
	}
	if _, ok := args["pulse"]; ok {
		externalError(w, 400, "invalid_settings", "pulse applies to workflows only.")
		return
	}
	if _, ok := args["after_manual_run"]; ok {
		externalError(w, 400, "invalid_settings", "after_manual_run applies to workflows only.")
		return
	}
	if expected := externalArg(args, "expected_version"); expected != "" && expected != state.version() {
		externalError(w, 409, "version_conflict", "Settings changed since you read them; read them again (settings action=get) and reapply your change.")
		return
	}
	changed, pending, failure := api.applyExternalSettings(ctx, claims.UserID, target, &state, args)
	if failure != nil {
		externalError(w, failure.status, failure.code, failure.message)
		return
	}
	if len(changed) == 0 {
		externalError(w, 400, "invalid_settings", "Nothing to change; send at least one setting.")
		return
	}
	// Write only the keys that changed, so fields the Crew panels own and
	// this tool does not know keep their exact stored form.
	for _, field := range changed {
		switch field {
		case "models":
			capabilities["llm_config"] = state.LLMConfig
		case "mcp_servers", "tools":
			capabilities["selected_servers"], capabilities["selected_tools"] = nonNilStrings(state.Servers), nonNilStrings(state.Tools)
		case "skills":
			capabilities["selected_skills"] = nonNilStrings(state.Skills)
		case "secrets":
			capabilities["selected_secrets"] = nonNilStrings(state.Secrets)
			if state.GlobalSecrets != nil {
				capabilities["selected_global_secret_names"] = nonNilStrings(*state.GlobalSecrets)
			}
		case "browser_mode":
			capabilities["browser_mode"] = state.BrowserMode
		}
	}
	manifest["capabilities"] = capabilities
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		externalError(w, 500, "encode_failed", err.Error())
		return
	}
	if err := writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n"); err != nil {
		externalError(w, 502, "workspace_unavailable", "Crew settings could not be saved.")
		return
	}
	log.Printf("[EXTERNAL_SETTINGS] user=%s crew=%s changed=%s", claims.UserID, crewID, strings.Join(changed, ","))
	view := api.externalSettingsView(ctx, claims.UserID, target, state, nil)
	view["changed"] = changed
	if len(pending) > 0 {
		view["pending_sign_in"] = pending
	}
	externalJSON(w, view)
}
