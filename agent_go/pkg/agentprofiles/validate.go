package agentprofiles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

var (
	profileIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	skillIDPattern          = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	toolIDPattern           = regexp.MustCompile(`^[a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*)+$`)
	presentationKindPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:\.[a-z][a-z0-9]*)+$`)
)

func Validate(profile Profile) error {
	// Validation accepts both already-loaded manifests and direct JSON profile
	// payloads. Resolve a copy so feature declarations are checked and their
	// compatibility projection is included in all validation below.
	if err := ResolveFeatures(&profile); err != nil {
		return err
	}
	if !profileIDPattern.MatchString(strings.TrimSpace(profile.ID)) {
		return fmt.Errorf("invalid profile id %q", profile.ID)
	}
	if strings.TrimSpace(profile.Name) == "" {
		return fmt.Errorf("profile name is required")
	}
	if profile.Version < 1 {
		return fmt.Errorf("profile version must be at least 1")
	}
	if strings.TrimSpace(profile.SystemPromptTemplate) == "" {
		return fmt.Errorf("system prompt is required")
	}
	if _, err := parsePrompt(profile.SystemPromptTemplate); err != nil {
		return err
	}
	if profile.BuiltIn && strings.TrimSpace(profile.OwnerID) != "" {
		return fmt.Errorf("built-in profile cannot have an owner")
	}
	if !profile.BuiltIn && strings.TrimSpace(profile.OwnerID) == "" {
		return fmt.Errorf("user profile owner is required")
	}
	switch scope := strings.TrimSpace(profile.Scope); scope {
	case "", ProfileScopeProject, ProfileScopeGlobal:
	default:
		return fmt.Errorf("invalid profile scope %q (want %q, %q, or empty)", scope, ProfileScopeProject, ProfileScopeGlobal)
	}

	if err := productschedule.ValidateAll(profile.Schedules); err != nil {
		return fmt.Errorf("profile %q schedules: %w", profile.ID, err)
	}
	if len(profile.Schedules) > 0 && strings.ToLower(strings.TrimSpace(profile.Runtime.Conversation.Mode)) != ConversationModeSingleton {
		return fmt.Errorf("profile %q schedules: schedules run in the product's single conversation, so runtime.conversation.mode must be %q", profile.ID, ConversationModeSingleton)
	}

	seenSkills := make(map[string]struct{}, len(profile.Skills))
	for _, raw := range profile.Skills {
		skill := strings.TrimSpace(raw)
		if !skillIDPattern.MatchString(skill) {
			return fmt.Errorf("invalid skill id %q", raw)
		}
		if _, exists := seenSkills[skill]; exists {
			return fmt.Errorf("duplicate skill %q", skill)
		}
		seenSkills[skill] = struct{}{}
	}

	seenTools := make(map[string]struct{}, len(profile.Tools))
	for _, binding := range profile.Tools {
		toolID := strings.TrimSpace(binding.ID)
		if !toolIDPattern.MatchString(toolID) {
			return fmt.Errorf("invalid tool id %q", binding.ID)
		}
		if _, exists := seenTools[toolID]; exists {
			return fmt.Errorf("duplicate tool %q", toolID)
		}
		seenTools[toolID] = struct{}{}
		if len(binding.Config) > 0 && !json.Valid(binding.Config) {
			return fmt.Errorf("tool %q has invalid JSON config", toolID)
		}
		if binding.Presentation != nil {
			if !presentationKindPattern.MatchString(strings.TrimSpace(binding.Presentation.Kind)) {
				return fmt.Errorf("tool %q has invalid presentation kind %q (want dotted lowercase, e.g. media.video)", toolID, binding.Presentation.Kind)
			}
			activity := binding.Presentation.Activity
			if activity == nil || strings.TrimSpace(activity.Label) == "" || strings.TrimSpace(activity.Destination) == "" || strings.TrimSpace(activity.Detail) == "" {
				return fmt.Errorf("tool %q presentation requires activity.label, activity.destination, and activity.detail", toolID)
			}
		}
	}
	for _, name := range profile.ToolPolicy.Disabled {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("tool_policy.disabled contains an empty tool name")
		}
	}
	if mode := strings.TrimSpace(profile.ToolPolicy.Mode); mode != "" && !profile.ToolPolicy.IsAllowlist() {
		return fmt.Errorf("invalid tool_policy.mode %q (want %q or empty)", profile.ToolPolicy.Mode, ToolPolicyModeAllowlist)
	}
	seenEnabled := make(map[string]struct{}, len(profile.ToolPolicy.Enabled))
	for _, name := range profile.ToolPolicy.Enabled {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("tool_policy.enabled contains an empty tool name")
		}
		if _, exists := seenEnabled[trimmed]; exists {
			return fmt.Errorf("tool_policy.enabled lists %q twice", trimmed)
		}
		seenEnabled[trimmed] = struct{}{}
	}
	// An allowlist is the complete set; a deny-list alongside it would be a
	// second place deciding the same thing, which is the drift this replaces.
	if profile.ToolPolicy.IsAllowlist() {
		if len(profile.ToolPolicy.Enabled) == 0 {
			return fmt.Errorf("tool_policy.mode=%s requires a non-empty enabled list", ToolPolicyModeAllowlist)
		}
		if len(profile.ToolPolicy.Disabled) > 0 {
			return fmt.Errorf("tool_policy.mode=%s cannot be combined with tool_policy.disabled", ToolPolicyModeAllowlist)
		}
	}

	if err := validateRuntime(profile.Runtime); err != nil {
		return err
	}
	if mode := strings.ToLower(strings.TrimSpace(profile.Runtime.AgentTools.Mode)); mode == "full" || mode == "hybrid" {
		if !profile.ToolPolicy.IsAllowlist() {
			return fmt.Errorf("runtime agent_tools.mode=full requires tool_policy.mode=%q", ToolPolicyModeAllowlist)
		}
	}
	// The exclusion is between the two API transports, not between native
	// tools and the bridge shell. native_shell replaces execute_shell_command,
	// so having both is genuinely ambiguous — but native tools alone do not
	// imply the CLI can reach product APIs from its own shell. Codex cannot: it operates through a
	// JS sandbox with no network and no environment, so the bridge shell is its
	// only path. Requiring hybrid profiles to drop it left Codex with no way to
	// call any product API at all. See
	// docs/design/product_api_transport_for_coding_agents.md.
	// A native shell is sandboxed and has no product credentials, so product
	// APIs always go through the bridge transport.
	if strings.EqualFold(strings.TrimSpace(profile.Runtime.APITransport.Mode), "native_shell") {
		return fmt.Errorf("runtime api_transport.mode=native_shell is unsupported: product APIs go through the bridge transport")
	}
	return nil
}

func validateRuntime(runtime RuntimePolicy) error {
	transport := strings.ToLower(strings.TrimSpace(runtime.Transport))
	provider := strings.TrimSpace(runtime.Provider)
	modelID := strings.TrimSpace(runtime.ModelID)
	capabilities := runtime.Capabilities
	if transport == "" {
		transport = "auto"
	}
	if transport != "auto" && transport != "tmux" && transport != "structured" {
		return fmt.Errorf("invalid runtime transport %q", runtime.Transport)
	}
	// "hybrid" is the retired name for "full", kept so older profiles load.
	if mode := strings.ToLower(strings.TrimSpace(runtime.AgentTools.Mode)); mode != "" && mode != "mcp_only" && mode != "full" && mode != "hybrid" {
		return fmt.Errorf("invalid runtime agent_tools.mode %q", runtime.AgentTools.Mode)
	}
	if mode := strings.ToLower(strings.TrimSpace(runtime.Approvals.Mode)); mode != "" && mode != "provider_auto" && mode != "approve_all" {
		return fmt.Errorf("invalid runtime approvals.mode %q", runtime.Approvals.Mode)
	}
	switch mode := strings.ToLower(strings.TrimSpace(runtime.Sandbox.Mode)); mode {
	case "", SandboxModeFolder, SandboxModeStrict:
	default:
		return fmt.Errorf("invalid runtime sandbox.mode %q (want %q or %q)", runtime.Sandbox.Mode, SandboxModeFolder, SandboxModeStrict)
	}
	switch network := strings.ToLower(strings.TrimSpace(runtime.Sandbox.Network)); network {
	case "", SandboxNetworkAllowed, SandboxNetworkOff:
	default:
		return fmt.Errorf("invalid runtime sandbox.network %q (want %q or %q)", runtime.Sandbox.Network, SandboxNetworkAllowed, SandboxNetworkOff)
	}
	if runtime.Sandbox.NetworkDisabled() && !runtime.Sandbox.IsStrict() {
		return fmt.Errorf("runtime sandbox.network %q requires sandbox.mode %q", SandboxNetworkOff, SandboxModeStrict)
	}
	switch chatHistory := strings.ToLower(strings.TrimSpace(runtime.Sandbox.ChatHistory)); chatHistory {
	case "", SandboxChatHistoryNone:
	default:
		return fmt.Errorf("invalid runtime sandbox.chat_history %q (want empty or %q)", runtime.Sandbox.ChatHistory, SandboxChatHistoryNone)
	}
	for _, folder := range runtime.Sandbox.ReadOnly {
		clean := strings.TrimSpace(folder)
		if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, "..") {
			return fmt.Errorf("invalid runtime sandbox.read_only entry %q (workspace-relative folder expected)", folder)
		}
	}
	if mode := strings.ToLower(strings.TrimSpace(runtime.APITransport.Mode)); mode != "" && mode != "bridge_shell" && mode != "native_shell" && mode != "disabled" {
		return fmt.Errorf("invalid runtime api_transport.mode %q", runtime.APITransport.Mode)
	}
	conversationMode := strings.ToLower(strings.TrimSpace(runtime.Conversation.Mode))
	workspaceMode := strings.ToLower(strings.TrimSpace(runtime.Workspace.Mode))
	if conversationMode != "" {
		switch conversationMode {
		case ConversationModeSingleton:
			if strings.TrimSpace(runtime.Conversation.KeyType) != "" {
				return fmt.Errorf("runtime conversation singleton cannot declare key_type")
			}
			if workspaceMode != WorkspaceModeFixed || strings.TrimSpace(runtime.Workspace.Root) == "" {
				return fmt.Errorf("runtime conversation singleton requires workspace.mode=%q and workspace.root", WorkspaceModeFixed)
			}
		case ConversationModeKeyed:
			if strings.ToLower(strings.TrimSpace(runtime.Conversation.KeyType)) != ConversationKeyTypeProject {
				return fmt.Errorf("runtime conversation keyed currently requires key_type=%q", ConversationKeyTypeProject)
			}
			if workspaceMode != WorkspaceModeProject || strings.TrimSpace(runtime.Workspace.ProjectsRoot) == "" {
				return fmt.Errorf("runtime project conversation requires workspace.mode=%q and workspace.projects_root", WorkspaceModeProject)
			}
		default:
			return fmt.Errorf("invalid runtime conversation.mode %q", runtime.Conversation.Mode)
		}
	} else if workspaceMode != "" && workspaceMode != WorkspaceModeFixed && workspaceMode != WorkspaceModeProject {
		return fmt.Errorf("invalid runtime workspace.mode %q", runtime.Workspace.Mode)
	}
	if (provider == "") != (modelID == "") {
		return fmt.Errorf("runtime provider and model_id must be set together")
	}
	seenProviderOptions := make(map[string]struct{}, len(runtime.ProviderOptions))
	defaultProviderOptions := 0
	for _, option := range runtime.ProviderOptions {
		id := strings.TrimSpace(option.ID)
		if !profileIDPattern.MatchString(id) {
			return fmt.Errorf("invalid runtime provider option id %q", option.ID)
		}
		if _, exists := seenProviderOptions[id]; exists {
			return fmt.Errorf("duplicate runtime provider option %q", option.ID)
		}
		seenProviderOptions[id] = struct{}{}
		if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Provider) == "" || strings.TrimSpace(option.ModelID) == "" {
			return fmt.Errorf("runtime provider option %q requires label, provider, and model_id", id)
		}
		if option.Default {
			defaultProviderOptions++
		}
	}
	if defaultProviderOptions > 1 {
		return fmt.Errorf("runtime provider_options has more than one default")
	}
	credentialScope := strings.ToLower(strings.TrimSpace(runtime.CredentialScope))
	if credentialScope != "" && credentialScope != CredentialScopeWorkspace && credentialScope != CredentialScopeGlobal {
		return fmt.Errorf("invalid runtime credential_scope %q", runtime.CredentialScope)
	}

	values := []struct {
		name  string
		value CapabilityRequirement
	}{
		{"live_input", capabilities.LiveInput},
		{"raw_terminal", capabilities.RawTerminal},
		{"warm_session", capabilities.WarmSession},
		{"workflow_execution", capabilities.WorkflowExecution},
		{"browser", capabilities.Browser},
		{"secrets", capabilities.Secrets},
		{"mcp_selection", capabilities.MCPSelection},
		{"skill_selection", capabilities.SkillSelection},
		{"workflow_references", capabilities.WorkflowReferences},
		{"whatsapp", capabilities.WhatsApp},
	}
	for _, item := range values {
		if !validCapabilityRequirement(item.value) {
			return fmt.Errorf("invalid %s requirement %q", item.name, item.value)
		}
	}
	if transport == "structured" {
		if capabilities.LiveInput == CapabilityRequired {
			return fmt.Errorf("structured transport cannot require live_input")
		}
		if capabilities.RawTerminal == CapabilityRequired {
			return fmt.Errorf("structured transport cannot require raw_terminal")
		}
		if capabilities.WarmSession == CapabilityRequired {
			return fmt.Errorf("structured transport cannot require warm_session")
		}
	}
	return nil
}

func validCapabilityRequirement(value CapabilityRequirement) bool {
	switch value {
	case "", CapabilityRequired, CapabilityPreferred, CapabilityOptional, CapabilityDisabled:
		return true
	default:
		return false
	}
}

func parsePrompt(source string) (*template.Template, error) {
	parsed, err := template.New("agent-profile").Option("missingkey=error").Parse(source)
	if err != nil {
		return nil, fmt.Errorf("invalid system prompt template: %w", err)
	}
	return parsed, nil
}

func RenderPrompt(profile Profile, promptContext PromptContext) (string, error) {
	if err := Validate(profile); err != nil {
		return "", err
	}
	parsed, err := parsePrompt(profile.SystemPromptTemplate)
	if err != nil {
		return "", err
	}
	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, promptContext); err != nil {
		return "", fmt.Errorf("render system prompt: %w", err)
	}
	return rendered.String(), nil
}
