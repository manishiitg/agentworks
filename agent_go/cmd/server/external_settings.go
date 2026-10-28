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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Workflow, Flow and Crew settings over MCP: the fields the app's settings
// panels save. Workflows and Flows save through the app's manifest handler
// and its owner/editor check; Crews rewrite the Crew's runtime manifest like
// the Crew panels, owner only (as update_crew). Every choice the panels only
// offer from a list (model accounts, MCP servers, skills, shared secrets) is
// checked against what the caller may use, since an MCP client can send
// anything. Secret values are write-only and owner-only; never returned.

func isExternalSettingsTool(name string) bool {
	return name == "get_settings" || name == "update_settings" || name == "get_secret_sources" || name == "get_project_models" || name == "set_project_models"
}

func externalSettingsDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	names := map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}}
	addRemove := func(what string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "description": what,
			"properties": map[string]any{"add": names, "remove": names}}
	}
	target := func() map[string]any {
		return map[string]any{
			"workflow_id": externalString("Workflow or Flow ID from workflow action=list. Pass this or crew_id."),
			"crew_id":     externalString("Crew ID from workspace action=list. Pass this or workflow_id."),
		}
	}
	add("get_settings", "Read a workflow's, Flow's or Crew's settings: models per role, MCP servers and tools, skills, secrets (names and whether a value is stored; never values), browser mode, notifications (workflows and Flows), plus a version for settings action=update. Builders also receive the names-only project Vault panel inventory under vault.", false, false, target())
	sourceTarget := target()
	add("get_secret_sources", "Explain this product's saved secret choices by name: its own copy, the company Vault copy and which takes priority. Values, token validity and account identities are not checked. Does not change selections or stored copies. Pass workflow_id or crew_id.", false, false, sourceTarget)
	props := target()
	for key, value := range map[string]any{
		"chat_instructions":  map[string]any{"type": "string", "maxLength": workflowtypes.ChatInstructionsLimit, "description": "Optional short instructions for the app chat: behavior, tone and what to do first. Visible to people who can use the app; no secrets. Empty clears. Owner only; never grants tools or permissions."},
		"expected_version":   externalString("Version from settings action=get; the update is refused if the settings changed since."),
		"models":             map[string]any{"type": "object", "description": "The models object from settings action=get with your changes. Replaces it whole; every account (connection_id) must be one you may use."},
		"mcp_servers":        map[string]any{"type": "object", "additionalProperties": false, "description": "Attach/detach apps (MCP servers and Vault apps), or replace the complete selection.", "properties": map[string]any{"add": names, "remove": names, "replace": names}},
		"use_all_vault_apps": map[string]any{"type": "boolean", "description": "Use all my apps automatically. On: every Vault app the owner may use is available. Off: only the Vault apps listed in mcp_servers."},
		"tools":              addRemove("Tools to allow or disallow, as server:tool. The server must be attached."),
		"skills":             addRemove("Installed or built-in skills to use or stop using."),
		"secrets": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"set":         map[string]any{"type": "object", "maxProperties": 20, "additionalProperties": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536}, "description": "NAME: value. Stores the value (write-only, owner only) and selects the secret. To add it to Vault for other projects, then call manage_vault_secret_access operation=share (a Vault administrator)."},
			"remove":      names,
			"select":      names,
			"unselect":    names,
			"use_shared":  names,
			"stop_shared": names,
		}, "description": "set/remove store or delete secret values (owner only); select/unselect choose stored secrets; use_shared/stop_shared choose shared (global) secrets by name."},
		"knowledgebase_sources": map[string]any{"type": "array", "maxItems": 20, "description": "Replace linked workflow knowledge sources. Existing sharing and write grants still apply. Empty removes the links.", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []any{"workflow_id", "alias", "access"}, "properties": map[string]any{"workflow_id": externalString("Source workflow ID."), "alias": externalString("Short name for this source."), "access": map[string]any{"type": "string", "enum": []any{"read", "write"}}}}},
		"kb_write_grants":       names,
		"browser_mode":          map[string]any{"type": "string", "enum": browser.ConfigurationModes()},
		"backup":                map[string]any{"type": "object", "additionalProperties": false, "description": "Goals only: after_runs runs configured Backup after every full run; off by default. Does not create or enable a destination.", "properties": map[string]any{"after_runs": map[string]any{"type": "boolean"}}},
		"publish":               map[string]any{"type": "object", "additionalProperties": false, "description": "Goals only: after_runs refreshes configured publishing after every full run; off by default. Does not create or enable a destination.", "properties": map[string]any{"after_runs": map[string]any{"type": "boolean"}}},
		"notifications": map[string]any{"type": "object", "additionalProperties": false, "description": "Workflows and Flows only.", "properties": map[string]any{
			"run_instructions":    map[string]any{"type": "string", "maxLength": 4000},
			"run_channels":        names,
			"run_recipients":      names,
			"gmail_connection_id": map[string]any{"type": "string", "description": "An enabled Gmail account already added to this product. Empty clears the selection."},
			"exclude_channels":    names,
			"break_alerts":        map[string]any{"type": "boolean", "description": "Tell me when something breaks: show a run status in Needs you. On unless turned off. AgentWorks reports progress; configured team delivery is separate."},
			"wake_tab": map[string]any{"type": "object", "additionalProperties": false, "description": "A Crew chat to message straight away when a run of this workflow fails, stops partway, or never actually ran. Pass the Crew id and the chat id from workspace action=chats (main for its main chat); that chat needs an enabled recurring check. Send both empty to clear.", "properties": map[string]any{
				"crew_id": map[string]any{"type": "string"}, "chat_id": map[string]any{"type": "string"},
			}},
		}},
	} {
		props[key] = value
	}
	if browser.CDPEnabled() {
		props["browser_cdp_port"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 65535, "description": "Local Chrome debugging port; defaults to 9222."}
	}
	add("update_settings", "Change a workflow's, Flow's or Crew's settings. Send only what changes. Workflows and Flows need owner or editor access, Crews their owner; setting or removing secret values needs owner. MCP servers come from your own connections, shared Vault connections the server allows you, or the catalog; one that still needs a sign-in is attached and listed under pending_sign_in. Returns the new settings.", true, false, props)
}

// externalSettingsState is the part of a workflow's or Crew's capabilities
// these tools read and change.
type externalSettingsState struct {
	ChatInstructions string                         `json:"chat_instructions,omitempty"`
	LLMConfig        *workflowtypes.PresetLLMConfig `json:"llm_config,omitempty"`
	Servers          []string                       `json:"selected_servers,omitempty"`
	// ChosenVaultAppsOnly is "Use all my apps automatically" off (PLAT-856-394).
	ChosenVaultAppsOnly bool      `json:"chosen_vault_apps_only,omitempty"`
	Tools               []string  `json:"selected_tools,omitempty"`
	Skills              []string  `json:"selected_skills,omitempty"`
	Secrets             []string  `json:"selected_secrets,omitempty"`
	GlobalSecrets       *[]string `json:"selected_global_secret_names,omitempty"`
	BrowserMode         string    `json:"browser_mode,omitempty"`
	BrowserCDPPort      int       `json:"browser_cdp_port,omitempty"`
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
		"kind": t.kind, "version": s.version(), "my_access": t.access, "chat_instructions": s.ChatInstructions,
		"models": s.LLMConfig, "mcp_servers": nonNilStrings(savedAppSelection(s.Servers)), "use_all_vault_apps": !s.ChosenVaultAppsOnly, "tools": nonNilStrings(s.Tools),
		"skills": nonNilStrings(s.Skills),
		"secrets": map[string]any{"stored": stored, "shared_selected": shared, "shared_available": available,
			"note": "Values are never returned. Only owners may set or remove values."},
		"browser_mode": s.BrowserMode, "browser_cdp_port": s.BrowserCDPPort, "allowed_browser_modes": browser.VisibleModes(),
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
	if raw, ok := args["chat_instructions"]; ok {
		if !t.owner {
			return nil, nil, &externalSettingsError{403, "forbidden", "Only the project's owner can change Chat instructions."}
		}
		text, ok := raw.(string)
		if !ok {
			return nil, nil, settingsInvalid("Chat instructions must be text")
		}
		value, err := workflowtypes.NormalizeChatInstructions(text)
		if err != nil {
			return nil, nil, settingsInvalid("%v", err)
		}
		s.ChatInstructions = value
		changed = append(changed, "chat_instructions")
	}

	// Models: every named account must be one this person may use here.
	if raw, ok := args["models"]; ok {
		encoded, _ := json.Marshal(raw)
		var config workflowtypes.PresetLLMConfig
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return nil, nil, settingsInvalid("models: %v", err)
		}
		pins := []*workflowtypes.AgentLLMConfig{config.BuilderLLM, config.BackgroundLLM}
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

	if raw, ok := args["mcp_servers"].(map[string]any); ok && raw["replace"] != nil {
		if raw["add"] != nil || raw["remove"] != nil {
			return nil, nil, settingsInvalid("Use replace by itself for the complete MCP selection")
		}
		names := externalSettingsList(args, "mcp_servers", "replace")
		next := []string{}
		for _, name := range names {
			if slices.Contains(s.Servers, name) {
				next = settingsWith(next, name)
				continue
			}
			if strings.HasPrefix(name, "vault_") {
				access, err := vaultAccessFor(ctx, api.projectSecretsOwner(userID, t.path))
				if err != nil {
					return nil, nil, settingsInvalid("Project Vault access could not be checked")
				}
				if !slices.ContainsFunc(access.Servers, func(row vaultRuntimeServer) bool { return vaultServerName(row.ID) == name }) {
					connection, e := vaultSavedConnection(ctx, strings.TrimPrefix(name, "vault_"))
					if api.projectSecretsOwner(userID, t.path) != userID || e != nil || !vaultOwnerOf(ctx, userID, connection.VaultID) {
						return nil, nil, settingsInvalid("This project cannot use that Vault app. Ask in chat to share the app with the project's owner first")
					}
				}
				next = settingsWith(next, name)
				continue
			}
			if api.projectActions == nil {
				return nil, nil, settingsInvalid("MCP configuration is unavailable")
			}
			canonical, waiting, err := api.projectActions.validateCrewCreationServers(ctx, userID, []string{name})
			if err != nil {
				return nil, nil, settingsInvalid("mcp_servers: %v", err)
			}
			next = settingsWith(next, canonical...)
			pending = append(pending, waiting...)
		}
		s.Servers = next
		s.Tools = slices.DeleteFunc(s.Tools, func(tool string) bool {
			server, _, ok := strings.Cut(tool, ":")
			return ok && !slices.Contains(s.Servers, server)
		})
		changed = append(changed, "mcp_servers")
	}
	if adds := externalSettingsList(args, "mcp_servers", "add"); len(adds) > 0 {
		if api.projectActions == nil {
			return nil, nil, &externalSettingsError{503, "mcp_unavailable", "MCP configuration is unavailable."}
		}
		canonical, waiting, err := api.projectActions.validateCrewCreationServers(ctx, userID, adds)
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
	if all, ok := args["use_all_vault_apps"].(bool); ok {
		s.ChosenVaultAppsOnly = !all
		changed = settingsWith(changed, "mcp_servers")
	}
	s.Servers = savedAppSelection(s.Servers)
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
			return nil, nil, &externalSettingsError{403, "forbidden", "Only the project's owner can set or remove secret values. Ask the owner to give you the value, or to run `agentworks secret set NAME` for this project in their terminal."}
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
		if api.projectActions == nil {
			return nil, nil, &externalSettingsError{503, "secrets_unavailable", "Shared secrets are unavailable."}
		}
		if _, err := api.projectActions.validateCrewCreationGlobalSecrets(ctx, userID, shared); err != nil {
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
	if _, present := args["browser_cdp_port"]; present {
		port, err := externalBuilderInt(args, "browser_cdp_port", 9222, 1, 65535)
		if err != nil || !browser.CDPEnabled() {
			return nil, nil, settingsInvalid("Choose a local Chrome port between 1 and 65535")
		}
		s.BrowserCDPPort = port
		changed = append(changed, "browser_cdp_port")
	}

	if _, ok := args["free_text_ask"]; ok {
		return nil, nil, settingsInvalid("free_text_ask was removed: Ask is always open to everyone the project is shared with. Control access by sharing.")
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
			if skills.IsBuiltinSkillIn(path, name) {
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
	modelsOnly := modelSettingsMember(name) != name
	name = modelSettingsMember(name)
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	if t := claims.AccessToken; t != nil && !t.Allows("workflows:read") && !t.Allows("runs:execute") {
		externalError(w, 403, "insufficient_scope", "This connection cannot read workflows.")
		return
	}
	if name == "get_secret_sources" {
		externalJSON(w, api.secretSources(ctx, claims.UserID, workflow.WorkspacePath))
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
	state := externalSettingsState{ChatInstructions: manifest.ChatInstructions, LLMConfig: caps.LLMConfig, Servers: savedAppSelection(caps.SelectedServers), ChosenVaultAppsOnly: caps.ChosenVaultAppsOnly,
		Tools: append([]string{}, caps.SelectedTools...), Skills: append([]string{}, caps.SelectedSkills...),
		Secrets: append([]string{}, caps.SelectedSecrets...), GlobalSecrets: caps.SelectedGlobalSecretNames, BrowserMode: caps.BrowserMode, BrowserCDPPort: caps.BrowserCDPPort}
	notifications := func(c WorkflowCapabilities) map[string]any {
		if n := c.Notifications; n != nil {
			return map[string]any{"run_instructions": n.RunSummaryInstructions, "run_channels": n.RunSummaryChannels, "run_recipients": n.RunSummaryRecipients, "gmail_connection_id": n.GmailConnectionID, "exclude_channels": n.ExcludeChannels, "break_alerts": n.BreakAlertsEnabled(), "wake_tab": n.WakeTab}
		}
		return map[string]any{"break_alerts": true}
	}

	if name == "get_settings" {
		view := api.externalSettingsView(ctx, claims.UserID, target, state, notifications(caps))
		addWorkflowSettingsView(view, manifest)
		if target.kind == "workflow" && (access == WorkflowAccessOwner || access == WorkflowAccessWrite) {
			if vault, err := api.projectVaultInventory(ctx, claims.UserID, target.path); err == nil {
				view["vault"] = vault
			}
		}
		// Backup, publish and notify status, as the app's views show them.
		for key, handler := range map[string]http.HandlerFunc{"backup": api.handleGetWorkflowBackup, "publish": api.handleGetWorkflowPublish, "notify": api.handleGetWorkflowNotifications} {
			if status, body := scheduleHandler(r, handler, http.MethodGet, "/api/workflow/"+key, nil, url.Values{"workspace_path": {workflow.WorkspacePath}}, nil); status == http.StatusOK && json.Valid(body) {
				view[key] = json.RawMessage(body)
			}
		}
		externalSettingsReply(w, view, modelsOnly)
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
	caps.LLMConfig, caps.SelectedServers, caps.SelectedTools, caps.SelectedSkills = state.LLMConfig, savedAppSelection(state.Servers), state.Tools, state.Skills
	caps.ChosenVaultAppsOnly = state.ChosenVaultAppsOnly
	caps.SelectedSecrets, caps.SelectedGlobalSecretNames, caps.BrowserMode = state.Secrets, state.GlobalSecrets, state.BrowserMode
	caps.BrowserCDPPort = state.BrowserCDPPort
	req := UpdateWorkflowManifestRequest{WorkspacePath: workflow.WorkspacePath, Capabilities: &caps}
	if raw, present := args["knowledgebase_sources"]; present {
		data, err := json.Marshal(raw)
		var sources []workflowtypes.KnowledgebaseSource
		if err != nil || json.Unmarshal(data, &sources) != nil || sources == nil {
			externalError(w, 400, "invalid_settings", "Knowledge sources must be a list. Send [] to remove all links.")
			return
		}
		req.KnowledgebaseSources = &sources
		changed = append(changed, "knowledgebase_sources")
	}
	if _, present := args["kb_write_grants"]; present {
		values := externalSettingsList(map[string]any{"grants": map[string]any{"values": args["kb_write_grants"]}}, "grants", "values")
		req.KBWriteGrants = &values
		changed = append(changed, "kb_write_grants")
	}
	if _, ok := args["chat_instructions"]; ok {
		req.ChatInstructions = &state.ChatInstructions
	}
	if n, ok := args["notifications"].(map[string]any); ok {
		if v, ok := n["run_instructions"].(string); ok {
			req.RunNotificationInstructions = &v
		}
		if _, ok := n["run_channels"]; ok {
			v := externalSettingsList(args, "notifications", "run_channels")
			req.RunNotificationChannels = &v
		}
		if _, present := n["run_recipients"]; present {
			values := externalSettingsList(args, "notifications", "run_recipients")
			req.RunNotificationRecipients = &values
		}
		if value, present := n["gmail_connection_id"].(string); present {
			req.NotificationGmailConnectionID = &value
		}
		if _, present := n["exclude_channels"]; present {
			if caps.Notifications == nil {
				caps.Notifications = &WorkflowNotificationConfig{}
			}
			caps.Notifications.ExcludeChannels = normalizeNotificationChannels(externalSettingsList(args, "notifications", "exclude_channels"))
		}
		if v, ok := n["break_alerts"].(bool); ok {
			req.BreakAlerts = &v
		}
		if w, ok := n["wake_tab"].(map[string]any); ok {
			crew, _ := w["crew_id"].(string)
			chat, _ := w["chat_id"].(string)
			req.WakeTab = &WorkflowWakeTab{CrewID: crew, ChatID: chat}
		}
		changed = append(changed, "notifications")
	}
	for key, target := range map[string]**WorkflowAfterRunsUpdate{"backup": &req.Backup, "publish": &req.Publish} {
		if raw, provided := args[key]; provided {
			option, ok := raw.(map[string]any)
			if !ok {
				externalError(w, 400, "invalid_settings", key+" must contain after_runs as true or false.")
				return
			}
			value, ok := option["after_runs"].(bool)
			if !ok {
				externalError(w, 400, "invalid_settings", key+".after_runs must be true or false.")
				return
			}
			*target = &WorkflowAfterRunsUpdate{AfterRuns: &value}
			changed = append(changed, key)
		}
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
	view := api.externalSettingsView(ctx, claims.UserID, target, externalSettingsState{ChatInstructions: manifest.ChatInstructions, LLMConfig: caps.LLMConfig, Servers: savedAppSelection(caps.SelectedServers), ChosenVaultAppsOnly: caps.ChosenVaultAppsOnly,
		Tools: caps.SelectedTools, Skills: caps.SelectedSkills, Secrets: caps.SelectedSecrets, GlobalSecrets: caps.SelectedGlobalSecretNames,
		BrowserMode: caps.BrowserMode, BrowserCDPPort: caps.BrowserCDPPort}, notifications(caps))
	addWorkflowSettingsView(view, manifest)
	view["backup"] = map[string]any{"config": manifest.Backup}
	view["publish"] = map[string]any{"config": manifest.Publish}
	notes := []string{}
	if manifest.Backup != nil && manifest.Backup.AfterRuns && !manifest.Backup.Enabled {
		notes = append(notes, "Backup after every run is saved. Set up a backup destination in the Backup panel or ask me to set it up.")
	}
	if manifest.Publish != nil && manifest.Publish.AfterRuns && !manifest.Publish.Enabled {
		notes = append(notes, "Publish after every run is saved. Set up publishing in the Publish panel or ask me to set it up.")
	}
	if len(notes) > 0 {
		view["notes"] = notes
	}
	view["changed"] = changed
	if len(pending) > 0 {
		view["pending_sign_in"] = pending
	}
	externalSettingsReply(w, view, modelsOnly)
}

// externalCrewSettingsCall reads or changes a Crew's settings in its runtime
// manifest, the file the Crew's Models and Integrations panels write.
func (api *StreamingAPI) externalCrewSettingsCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	modelsOnly := modelSettingsMember(name) != name
	name = modelSettingsMember(name)
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	crewID := externalArg(args, "crew_id")
	if t := claims.AccessToken; t != nil {
		scope := "crews:read"
		if name == "update_settings" {
			scope = "crews:write"
		}
		if !t.Allows(scope) || !t.AllowsCrew(workspaceTokenProjectID(crewID)) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+name+" on this Crew.")
			return
		}
	}
	crew, project, _, ok := api.externalCrewResolve(ctx, claims, crewID)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	root := crew.Binding.WorkspacePath
	if name == "get_secret_sources" {
		externalJSON(w, api.secretSources(ctx, claims.UserID, root))
		return
	}
	raw, manifestPath, err := ensureProjectRuntimeManifest(ctx, project.Product, root)
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
	state.ChatInstructions = project.ChatInstructions
	access := "member"
	if crew.OwnedByCaller {
		access = "owner"
	}
	target := externalSettingsTarget{kind: "crew", path: root, access: access, owner: crew.OwnedByCaller, product: productCrews,
		skillCheck: func(ctx context.Context, names []string) error {
			return checkCrewSkillsAvailable(ctx, root, names, nil)
		}}
	if name == "get_settings" {
		view := api.externalSettingsView(ctx, claims.UserID, target, state, nil)
		if crew.OwnedByCaller {
			if vault, err := api.projectVaultInventory(ctx, claims.UserID, root); err == nil {
				view["vault"] = vault
			}
		}
		externalSettingsReply(w, view, modelsOnly)
		return
	}
	if !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew's owner can change its settings.")
		return
	}
	for _, key := range []string{"knowledgebase_sources", "kb_write_grants"} {
		if _, present := args[key]; present {
			externalError(w, 400, "invalid_settings", "Workflow knowledge links and grants apply to Goals and Flows. Use this Crew's existing Brain and folder tools.")
			return
		}
	}
	if _, ok := args["notifications"]; ok {
		externalError(w, 400, "invalid_settings", "notifications apply to workflows and Flows only.")
		return
	}

	for _, key := range []string{"backup", "publish"} {
		if _, ok := args[key]; ok {
			externalError(w, 400, "invalid_settings", "After every run applies to Goals. Use this Crew's existing Backup or Publish actions instead.")
			return
		}
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
	if _, ok := args["chat_instructions"]; ok {
		if err := updateCrewChatInstructions(ctx, root, state.ChatInstructions); err != nil {
			externalError(w, 502, "workspace_unavailable", "Chat instructions could not be saved.")
			return
		}
	}
	// Write only the keys that changed, so fields the Crew panels own and
	// this tool does not know keep their exact stored form.
	for _, field := range changed {
		switch field {
		case "models":
			capabilities["llm_config"] = state.LLMConfig
		case "mcp_servers", "tools":
			capabilities["selected_servers"], capabilities["selected_tools"] = nonNilStrings(savedAppSelection(state.Servers)), nonNilStrings(state.Tools)
			if state.ChosenVaultAppsOnly {
				capabilities["chosen_vault_apps_only"] = true
			} else {
				delete(capabilities, "chosen_vault_apps_only")
			}
		case "skills":
			capabilities["selected_skills"] = nonNilStrings(state.Skills)
		case "secrets":
			capabilities["selected_secrets"] = nonNilStrings(state.Secrets)
			if state.GlobalSecrets != nil {
				capabilities["selected_global_secret_names"] = nonNilStrings(*state.GlobalSecrets)
			}
		case "browser_mode":
			capabilities["browser_mode"] = state.BrowserMode
		case "browser_cdp_port":
			capabilities["browser_cdp_port"] = state.BrowserCDPPort
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
	externalSettingsReply(w, view, modelsOnly)
}

// These are existing workflow settings; they grant no access by being read.
func addWorkflowSettingsView(view map[string]any, manifest *WorkflowManifest) {
	view["knowledgebase_sources"] = manifest.KnowledgebaseSources
	grants := []string{}
	if manifest.Access != nil {
		grants = nonNilStrings(manifest.Access.AllowedKBWriters)
	}
	view["kb_write_grants"] = grants
}
