package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"log"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
)

type resolvedAgentProfile struct {
	Definition      agentprofiles.Profile
	SelectedServers []string
	Prompt          string
	// IdentityKey carries the Crew's user-owned identity (role and purpose)
	// resolved from product.json for this query. Empty for products without
	// one. It feeds the session fingerprint so a retained native session
	// relaunches when the identity it was launched with changes.
	IdentityKey string
	// ChatConnections are this chat's own MCP connections: a Code's personal
	// servers switched on for it, or a Crew's attached ones. They join the
	// turn's servers later in the query path; here they only feed the session
	// fingerprint, so connecting Gmail to a Code relaunches its retained CLI
	// (resuming the same conversation) instead of the old process answering
	// "not registered by any connected server" (RTS 2026-09-29).
	ChatConnections []string
	// ChatSecrets are the names (never values) of the secrets this chat gets as SECRET_*
	// environment variables. The coding CLI's environment is fixed when it launches, so a secret
	// attached to the project later reaches it only through a relaunch: the names feed the
	// session fingerprint, like ChatConnections (RTS, SDE private, GITHUB_TOKEN, 2026-09-30).
	ChatSecrets []string
	// APIKeys carries the project-scoped credential this resolver loaded from the
	// encrypted per-user/workspace store. It is returned on the resolver's own
	// result rather than handed back through req.LLMConfig so the query path can
	// never take a credential from the request body: LLMConfig is deserialized
	// from client JSON, and every field of ProviderAPIKeys except
	// ClaudeCodeOAuthToken is JSON-visible. nil when no profile is bound.
	APIKeys *llm.ProviderAPIKeys
}

// agentProfileSessionKey identifies the immutable product definition projected
// into a provider-native coding session. Native CLIs retain their original
// system prompt and tools when resumed, so a changed definition, MCP server
// selection, or Crew identity (role and purpose) must start a fresh native
// session even though the application conversation history remains intact.
// Skills stay out of the key: they are markdown files the agent re-reads
// from the workspace on demand, so edits apply without a relaunch.
// agentProfileToolsMode is the resolved profile's agent_tools mode.
func agentProfileToolsMode(profile *resolvedAgentProfile) string {
	if profile == nil {
		return ""
	}
	return normalizeAgentToolsMode(profile.Definition.Runtime.AgentTools.Mode)
}

// normalizeAgentToolsMode maps an agent_tools mode to "full" (Native agent
// tools on) or "mcp_only" (the default). The retired "hybrid" in older saved
// profiles and settings reads as "full".
func normalizeAgentToolsMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "full", "hybrid":
		return "full"
	}
	return "mcp_only"
}

func agentProfileSessionKey(profile *resolvedAgentProfile) string {
	if profile == nil {
		return ""
	}
	servers := append([]string(nil), profile.SelectedServers...)
	sort.Strings(servers)
	connections := append([]string(nil), profile.ChatConnections...)
	sort.Strings(connections)
	secrets := append([]string(nil), profile.ChatSecrets...)
	sort.Strings(secrets)
	payload, err := json.Marshal(struct {
		Definition      agentprofiles.Profile `json:"definition"`
		SelectedServers []string              `json:"selected_servers,omitempty"`
		IdentityKey     string                `json:"identity_key,omitempty"`
		ChatConnections []string              `json:"chat_connections,omitempty"`
		ChatSecrets     []string              `json:"chat_secrets,omitempty"`
	}{Definition: profile.Definition, SelectedServers: servers, IdentityKey: profile.IdentityKey, ChatConnections: connections, ChatSecrets: secrets})
	if err != nil {
		return fmt.Sprintf("%s@%d", profile.Definition.ID, profile.Definition.Version)
	}
	sum := sha256.Sum256(payload)
	return "profile-sha256:" + hex.EncodeToString(sum[:])
}

// cleanAgentProfileWorkspace validates the client-supplied selected_folder for
// a profile-backed turn. It is the authorization gate for that path: every
// agentProfileRuntimeWorkspace() call in the query path runs only after a
// profile resolved successfully, so rejecting here keeps an unauthorized path
// from ever reaching a folder guard or CLI working directory.
//
// Blocking traversal is not sufficient on its own. agentProfileRuntimeWorkspace
// only re-scopes paths that start with "Chats" into the caller's own
// _users/<id>/Chats space and returns anything else verbatim, so an explicit
// "_users/<someone-else>/..." never gets rewritten and would be handed to the
// agent as its read/write root. userID is therefore required, and a foreign
// _users/ prefix is refused rather than silently rewritten -- a caller aiming
// at another user's workspace is not a path to normalize, it is a request to
// deny.
func cleanAgentProfileWorkspace(raw, userID string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("selected_folder is required when agent_profile_id is set")
	}
	if filepath.IsAbs(raw) {
		return "", fmt.Errorf("selected_folder must be workspace-relative")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("selected_folder must stay inside the workspace")
	}
	// A shared crew root is reachable only as a verified crew binding
	// (resolveCrewProjectBinding), never as a free-form selected folder:
	// unlike _users/<id>/, its location says nothing about who may use it.
	if clean == crewSharedRootName || strings.HasPrefix(clean, crewSharedRootName+"/") {
		return "", fmt.Errorf("selected_folder must be a crew you can open")
	}
	if clean == "_users" || strings.HasPrefix(clean, "_users/") {
		owner := strings.TrimPrefix(clean, "_users")
		owner = strings.TrimPrefix(owner, "/")
		if idx := strings.Index(owner, "/"); idx >= 0 {
			owner = owner[:idx]
		}
		if owner == "" || owner != sanitizeUserIDForPath(userID) {
			return "", fmt.Errorf("selected_folder must stay inside your own workspace")
		}
	}
	return clean, nil
}

func appendUniqueStrings(current []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(current)+len(additions))
	out := make([]string, 0, len(current)+len(additions))
	for _, values := range [][]string{current, additions} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

// isGlobalScopedProfile reports whether the resolved profile declared
// agentprofiles.ProfileScopeGlobal -- any future global product profile
// profile with no single project workspace. A global-scoped profile keeps
// the same chat-wide grants (including the pulse/ write grant) and
// workspace description a profile-less turn already has; only a
// project-scoped profile narrows to one project root. Every call site that
// narrows behavior on bare `resolvedProfile != nil` must also check this, or
// it silently narrows a global-scoped profile the same way it narrows a
// project-scoped one.
func isGlobalScopedProfile(p *resolvedAgentProfile) bool {
	return p != nil && p.Definition.EffectiveScope() == agentprofiles.ProfileScopeGlobal
}

func agentProfileRuntimeWorkspace(userID, workspacePath string) string {
	workspacePath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(workspacePath)))
	if workspacePath == "Chats" || strings.HasPrefix(workspacePath, "Chats/") {
		suffix := strings.TrimPrefix(strings.TrimPrefix(workspacePath, "Chats"), "/")
		return filepath.ToSlash(filepath.Join(perUserChatsFolderFor(userID), suffix))
	}
	return workspacePath
}

// productConversationRuntimeWorkspace is the physical workspace holding a
// product chat's persisted conversation (and its native-resume runtime). A
// logical "Chats/..." project path maps into the caller's own tree; an
// explicit "_users/<owner>/..." path (Crew Run-mode readers) is kept. The
// logical form alone resolves outside any user tree and finds nothing — which
// made restored Crews start a fresh native session (RTS 2026-09-24).
func productConversationRuntimeWorkspace(userID, selectedFolder string) string {
	clean := strings.Trim(filepath.ToSlash(strings.TrimSpace(selectedFolder)), "/")
	if clean == "" {
		return ""
	}
	if strings.HasPrefix(clean, "_users/") {
		return clean
	}
	return agentProfileRuntimeWorkspace(userID, normalizeConversationWorkspace(clean))
}

// isActiveWorkProjectWorkspace distinguishes an actual Crew or Code project
// from the product's landing/root workspace. Project-scoped tools must be absent on the
// landing chat: registering them there either fails immediately (share links,
// schedules) or gives the model tools that cannot operate without a project manifest.
func isActiveWorkProjectWorkspace(userID, workspacePath string) bool {
	canonical := canonicalChatHistoryWorkspacePath(userID, workspacePath)
	// Crew Run mode: a work project is a work project whoever owns it.
	// Strip any owner's physical prefix before the shape check, so a
	// reader turn in someone else's crew still counts as project-scoped
	// (tool registration, guards) rather than landing-chat-scoped.
	return isProjectWorkspacePath(canonical)
}

// providerOptionRuntimeOptions returns a copy of the runtime options the
// profile declares for the (provider, model) binding a turn resolved to —
// nil when the binding is not one of the profile's provider_options or
// declares none. These become LLMModel.Options, which the agent runtime
// already turns into provider call options (reasoning_effort and friends).
func providerOptionRuntimeOptions(runtime agentprofiles.RuntimePolicy, provider, modelID string) map[string]interface{} {
	for _, option := range runtime.ProviderOptions {
		if !strings.EqualFold(strings.TrimSpace(option.Provider), strings.TrimSpace(provider)) || !strings.EqualFold(strings.TrimSpace(option.ModelID), strings.TrimSpace(modelID)) {
			continue
		}
		if len(option.Options) == 0 {
			return nil
		}
		out := make(map[string]interface{}, len(option.Options))
		for k, v := range option.Options {
			out[k] = v
		}
		return out
	}
	return nil
}

func resolveProfileRuntimeModel(runtime agentprofiles.RuntimePolicy, requestedProvider, requestedModelID string) (string, string) {
	provider, modelID := strings.TrimSpace(runtime.Provider), strings.TrimSpace(runtime.ModelID)
	for _, option := range runtime.ProviderOptions {
		if option.Default {
			provider, modelID = strings.TrimSpace(option.Provider), strings.TrimSpace(option.ModelID)
			break
		}
	}
	requestedProvider = strings.TrimSpace(requestedProvider)
	requestedModelID = strings.TrimSpace(requestedModelID)
	for _, option := range runtime.ProviderOptions {
		if !strings.EqualFold(requestedProvider, strings.TrimSpace(option.Provider)) {
			continue
		}
		if strings.EqualFold(requestedModelID, strings.TrimSpace(option.ModelID)) {
			return strings.TrimSpace(option.Provider), strings.TrimSpace(option.ModelID)
		}
		// option.ModelID is only this engine's own default model — Models is
		// the full curated list a turn may request within it (ProviderOption's
		// own doc comment: "narrows ... the server's accepted model_id to
		// exactly these ids"). Missing this fell through to the profile's
		// unrelated default provider option below, discarding both the
		// requested provider AND model. Caught live: switching SparkQuill's
		// composer from Luna to Sol (both codex-cli) silently
		// ran the next turn on claude-code with no prior conversation context.
		for _, id := range option.Models {
			if strings.EqualFold(requestedModelID, strings.TrimSpace(id)) {
				return strings.TrimSpace(option.Provider), requestedModelID
			}
		}
		if len(option.Models) == 0 && providerOffersModel(option.Provider, requestedModelID) {
			return strings.TrimSpace(option.Provider), requestedModelID
		}
	}
	return provider, modelID
}

// lookupAgentProfileDefinition returns the profile a request is bound to
// WITHOUT running its runtime initializer and without mutating the request.
//
// resolveAgentProfileForQuery is not a resolver: it calls
// agentProfiles.Initialize, which for a product like Video Studio seeds the
// workspace, writes a plan refresh, initializes the workflow DB, and runs
// productdeps.Ensure (which can shell out to `npx skills add`). It also rewrites
// the request's provider, model, skills, and secrets. That is correct once per
// turn and wrong for a caller that only needs to read the declared surface —
// delegation ran the whole initializer again for every sub-agent.
//
// Use this wherever the profile is being consulted rather than entered. It
// keeps the same authorization boundary: Resolve is what enforces ownership of
// a non-built-in profile.
func (api *StreamingAPI) lookupAgentProfileDefinition(ctx context.Context, req *QueryRequest, userID string) (*resolvedAgentProfile, error) {
	profileID := strings.TrimSpace(req.AgentProfileID)
	if profileID == "" {
		return nil, nil
	}
	if api.agentProfiles == nil {
		return nil, fmt.Errorf("agent profiles are unavailable")
	}
	profile, err := api.agentProfiles.Resolve(profileID, req.AgentProfileVersion, userID)
	if err != nil {
		return nil, err
	}
	if !userAllowedProduct(GetUserFromContext(ctx), profile.Product) {
		return nil, fmt.Errorf("you don't have access to the %q product", profile.Product)
	}
	return &resolvedAgentProfile{Definition: profile}, nil
}

func (api *StreamingAPI) resolveAgentProfileForQuery(ctx context.Context, req *QueryRequest, userID, sessionID string) (*resolvedAgentProfile, error) {
	profileID := strings.TrimSpace(req.AgentProfileID)
	if profileID == "" {
		if req.AgentProfileVersion != 0 || strings.TrimSpace(req.AgentProfileContext.ProjectTitle) != "" || strings.TrimSpace(req.AgentProfileContext.WorkspaceDescription) != "" {
			return nil, fmt.Errorf("agent_profile_id is required when agent profile fields are provided")
		}
		return nil, nil
	}
	if api.agentProfiles == nil {
		return nil, fmt.Errorf("agent profiles are unavailable")
	}
	if req.AgentMode != "multi-agent" {
		return nil, fmt.Errorf("agent profiles currently require agent_mode=multi-agent")
	}

	// Resolve before validating workspace/project fields: a global-scoped
	// global profile has no single project workspace, so whether
	// those fields are required at all depends on what this profile declares.
	profile, err := api.agentProfiles.Resolve(profileID, req.AgentProfileVersion, userID)
	if err != nil {
		return nil, err
	}
	if !userAllowedProduct(GetUserFromContext(ctx), profile.Product) {
		return nil, fmt.Errorf("you don't have access to the %q product", profile.Product)
	}
	isGlobalScope := profile.EffectiveScope() == agentprofiles.ProfileScopeGlobal

	selectedFolder := strings.TrimSpace(req.SelectedFolder)
	if isGlobalScope && selectedFolder == "" {
		// "Chats" is the same alias agentProfileRuntimeWorkspace already
		// rewrites to perUserChatsFolderFor(userID) -- the exact folder a
		// profile-less multi-agent turn already uses today.
		selectedFolder = "Chats"
	}
	// Crew Run mode resolves the binding before folder validation: a
	// reader's folder is another owner's physical crew root, which the
	// ownership check in cleanAgentProfileWorkspace must keep rejecting
	// for every path except the freshly verified binding root.
	crewOwned := true
	crewRoot := ""
	folderForClean := selectedFolder
	if strings.EqualFold(strings.TrimSpace(profile.ID), codeproduct.ProfileID) {
		// A Code resolves in the caller's own tree, or under an owner who
		// shared it with them; turn access (owner/co-owner/editor) was
		// checked by conversationTargetAccess. Like a Crew, a Code always runs
		// with native agent tools: there is no switch (owner decision
		// 2026-09-29; the CLIs' own reads are not sandboxed yet, PLAT-364
		// part 2).
		conversationKey := strings.TrimSpace(req.AgentProfileConversationKey)
		if conversationKey == "" {
			return nil, fmt.Errorf("agent_profile_conversation_key is required for Code")
		}
		project, bindErr := resolveCrewProjectBinding(ctx, userID, profile, conversationKey, selectedFolder)
		if bindErr != nil {
			return nil, fmt.Errorf("resolve Code workspace: %w", bindErr)
		}
		crewOwned = project.OwnedByCaller
		crewRoot = project.Binding.WorkspacePath
		// Everyone who may chat with a Code (owner, co-owner, editor) gets
		// native tools; viewers never reach this turn.
		if project.Binding.ProjectNativeAgentTools && profile.ToolPolicy.IsAllowlist() {
			profile.Runtime.AgentTools.Mode = "full"
		}
		if !crewOwned {
			if canonicalCrewWorkspaceRoot(selectedFolder) != canonicalCrewWorkspaceRoot(crewRoot) {
				return nil, fmt.Errorf("Code conversation does not match the selected session")
			}
			folderForClean = crewRoot
		}
	} else if strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) {
		conversationKey := strings.TrimSpace(req.AgentProfileConversationKey)
		if conversationKey == "" {
			return nil, fmt.Errorf("agent_profile_conversation_key is required for Work")
		}
		crew, crewErr := resolveCrewProjectBinding(ctx, userID, profile, conversationKey, selectedFolder)
		if crewErr != nil {
			return nil, fmt.Errorf("resolve Work workspace: %w", crewErr)
		}
		crewOwned = crew.OwnedByCaller
		crewRoot = crew.Binding.WorkspacePath
		// The crew's "Native agent tools" switch. Resolve returned a copy, so
		// this changes only this request's profile; the session key hashes the
		// definition, so toggling relaunches the coding CLI. Owners only:
		// readers keep AgentWorks-only tools. Hybrid needs an allowlist tool
		// policy (agentprofiles validation); the switch is on by default, so a
		// profile without one keeps AgentWorks-only tools instead of failing.
		if crewOwned && crew.Binding.ProjectNativeAgentTools && profile.ToolPolicy.IsAllowlist() {
			profile.Runtime.AgentTools.Mode = "full"
		}
		if !crewOwned {
			if canonicalCrewWorkspaceRoot(selectedFolder) != canonicalCrewWorkspaceRoot(crewRoot) {
				return nil, fmt.Errorf("Work conversation does not match the selected session")
			}
			folderForClean = crewRoot
		}
	}
	workspacePath, err := cleanAgentProfileWorkspace(folderForClean, userID)
	if err != nil {
		// The verified non-owned binding root is the one cross-owner path
		// a turn may address; its shape was verified by the projects-root
		// scan that produced it.
		if crewOwned || !isProjectWorkspacePath(folderForClean) {
			return nil, err
		}
		workspacePath = strings.Trim(filepath.ToSlash(strings.TrimSpace(folderForClean)), "/")
	}
	req.SelectedFolder = workspacePath
	if isProjectProfileID(profile.ID) && crewOwned {
		if !workspacePathsMatchForUser(userID, crewRoot, workspacePath) {
			return nil, fmt.Errorf("%s conversation does not match the selected session", profile.Name)
		}
	}

	promptContext := req.AgentProfileContext
	promptContext.ProjectTitle = strings.TrimSpace(promptContext.ProjectTitle)
	if promptContext.ProjectTitle == "" {
		if isGlobalScope {
			// A global profile has no per-turn project; its own declared Name
			// is the only sensible constant "title" for its prompt context.
			promptContext.ProjectTitle = profile.Name
		} else {
			return nil, fmt.Errorf("agent_profile_context.project_title is required")
		}
	}
	if strings.TrimSpace(promptContext.LocalDateTime) == "" {
		now := time.Now()
		_, offsetSeconds := now.Zone()
		offsetSign := "+"
		if offsetSeconds < 0 {
			offsetSign = "-"
			offsetSeconds = -offsetSeconds
		}
		promptContext.LocalDateTime = fmt.Sprintf("%s (UTC%s%02d:%02d)", now.Format("Monday, 2 January 2006 at 3:04 PM MST"), offsetSign, offsetSeconds/3600, (offsetSeconds%3600)/60)
	}
	// Product prompt variables are computed here from trusted state; never
	// from the request body.
	promptContext.Product = nil
	identityKey := ""
	if productVars, err := api.agentProfiles.PromptVariables(ctx, profile.ID, agentprofiles.RuntimeContext{
		UserID: userID, SessionID: sessionID, WorkspacePath: workspacePath,
	}); err != nil {
		return nil, fmt.Errorf("agent profile %q prompt variables: %w", profile.ID, err)
	} else if len(productVars) > 0 {
		promptContext.Product = productVars
		identityKey = productVars["WORK_IDENTITY_KEY"]
	}
	rendered, err := agentprofiles.RenderPrompt(profile, promptContext)
	if err != nil {
		return nil, err
	}
	if err := api.agentProfiles.Initialize(ctx, profile.ID, agentprofiles.RuntimeContext{
		UserID: userID, SessionID: sessionID, WorkspacePath: workspacePath,
	}); err != nil {
		return nil, fmt.Errorf("initialize agent profile %q: %w", profile.ID, err)
	}
	req.AgentProfileID = profile.ID
	req.AgentProfileVersion = profile.Version
	req.AgentProfileContext = promptContext
	req.SelectedSkills = appendUniqueStrings(req.SelectedSkills, profile.Skills...)
	if profile.Runtime.Capabilities.Secrets == agentprofiles.CapabilityDisabled {
		// Product profiles may explicitly opt out of the shared secret runtime.
		// Clear both scoped and global selections after saved chat configuration has
		// been applied, so a profile cannot inherit credentials accidentally.
		req.DecryptedSecrets = nil
		noGlobalSecrets := []string{}
		req.SelectedGlobalSecrets = &noGlobalSecrets
	} else if !isGlobalScope && api.chatStore != nil && userID != "" {
		// Product runtime manifests use the same selected_secrets contract.
		// Values stay in encrypted storage; only explicitly attached names enter
		// the coding-agent environment. For an older product manifest, preserve
		// the previous behavior once by attaching its existing project secrets.
		selectedGlobals, globalSecretErr := productSelectedGlobalSecrets(ctx, profile.ID, workspacePath)
		if globalSecretErr != nil {
			log.Printf("[SECRETS] Failed to read product global secret attachments for %s (%s): %v", userID, workspacePath, globalSecretErr)
			noGlobalSecrets := []string{}
			req.SelectedGlobalSecrets = &noGlobalSecrets
		} else {
			req.SelectedGlobalSecrets = selectedGlobals
		}
		selectedNames, initialized, secretErr := productSelectedSecrets(ctx, profile.ID, workspacePath)
		if secretErr != nil {
			log.Printf("[SECRETS] Failed to read product secret attachments for %s (%s): %v", userID, workspacePath, secretErr)
		} else if !initialized {
			stored, listErr := api.ensureSharedWorkflowSecrets(ctx, workspacePath, userID)
			if listErr != nil {
				log.Printf("[SECRETS] Failed to migrate product secret attachments for %s (%s): %v", userID, workspacePath, listErr)
			} else {
				for _, secret := range stored {
					selectedNames = appendUniqueStrings(selectedNames, secret.Name)
				}
				// Crew Run mode: a reader's turn attaches the stored names
				// in memory only. Persisting the migration would write the
				// owner's manifest from someone else's turn.
				if crewOwned {
					if writeErr := updateProductSelectedSecrets(ctx, profile.ID, workspacePath, func([]string) []string { return selectedNames }); writeErr != nil {
						log.Printf("[SECRETS] Failed to persist migrated product secret attachments for %s (%s): %v", userID, workspacePath, writeErr)
					}
				}
			}
		}
		if len(selectedNames) > 0 {
			req.DecryptedSecrets = api.loadSelectedSecrets(ctx, userID, workspacePath, selectedNames)
		} else {
			req.DecryptedSecrets = nil
		}
	}
	browserRequirement := profile.Runtime.Capabilities.Browser
	if browserRequirement != agentprofiles.CapabilityDisabled && isProjectWorkspacePath(workspacePath) {
		settings, err := readWorkspaceBrowserSettings(ctx, agentProfileRuntimeWorkspace(userID, workspacePath))
		if err != nil {
			return nil, fmt.Errorf("read project browser settings: %w", err)
		}
		req.BrowserMode = settings.Mode
		port := settings.Port
		req.CdpPort = &port
		req.CdpPorts = []int{port}
	}
	if browserRequirement == agentprofiles.CapabilityRequired || browserRequirement == agentprofiles.CapabilityPreferred || browserRequirement == agentprofiles.CapabilityOptional {
		// Agent profiles declare browser capability once. The generic chat
		// runtime then registers AgentWorks' managed agent_browser tool and
		// attaches its shared built-in skill; product code must not duplicate
		// either implementation.
		browserEnabled := true
		req.EnableBrowserAccess = &browserEnabled
		if strings.TrimSpace(req.BrowserMode) == "" || strings.EqualFold(strings.TrimSpace(req.BrowserMode), "none") {
			req.BrowserMode = "auto"
		}
	}
	if browserRequirement != agentprofiles.CapabilityDisabled {
		physical := agentProfileRuntimeWorkspace(userID, workspacePath)
		index := physical + "/browser-demonstrations/INDEX.md"
		if content, found, err := readFileFromWorkspace(ctx, index); err == nil && found && strings.TrimSpace(content) != "" {
			rendered += "\n\nSaved browser procedures for this workspace: " + index + ". When the user asks for a previously taught browser task, read this index and the matching tested procedure before acting. Resolve fresh page targets, supply the requested inputs and check the reviewed outcome. A saved procedure does not grant additional tool or website permissions.\n"
		}
	}
	var resolvedKeys *llm.ProviderAPIKeys
	requestHasExplicitModel := strings.TrimSpace(req.Provider) != "" && strings.TrimSpace(req.ModelID) != ""
	if provider, modelID := resolveProfileRuntimeModel(profile.Runtime, req.Provider, req.ModelID); provider != "" && modelID != "" {
		// A profile-owned model binding is authoritative over the user's global
		// AgentWorks chat selection for a project-scoped product (Video Studio):
		// the whole point is a curated, pinned choice. A global-scoped profile
		// global profile is meant to feel like a profile-less chat -- any
		// published LLM the user already picked wins; the declared binding is
		// only the starting default for a brand-new chat with no selection yet.
		if isGlobalScope && requestHasExplicitModel {
			provider, modelID = req.Provider, req.ModelID
		}
		req.Provider = provider
		req.ModelID = modelID
		llmOptions := providerOptionRuntimeOptions(profile.Runtime, provider, modelID)
		if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
			// providerOptionRuntimeOptions returns a fresh copy per call — safe
			// to mutate here without touching the profile's own declared options.
			if llmOptions == nil {
				llmOptions = map[string]interface{}{}
			}
			llmOptions["reasoning_effort"] = effort
		}
		// No account chosen: a person who added their own account for this provider uses it
		// (owner decision 2026-09-30). Choosing an account, the server one included, wins.
		// Interactive chats only: a schedule, webhook, bot or automatic notice keeps the account the
		// project names (else the server's) instead of silently spending someone's personal plan.
		interactiveTurn := strings.TrimSpace(req.TriggeredBy) == "" && strings.TrimSpace(req.BotPlatform) == "" && !req.IsAutoNotification
		if strings.TrimSpace(req.ConnectionID) == "" && !isGlobalScope && interactiveTurn {
			req.ConnectionID = ownDefaultProviderAccountID(ctx, userID, provider)
		}
		req.LLMConfig = &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: provider, ModelID: modelID, Options: llmOptions, ConnectionID: req.ConnectionID}}
		req.LLMConfigSource = llmConfigSourceAgentProfile
		if strings.EqualFold(strings.TrimSpace(profile.Runtime.CredentialScope), agentprofiles.CredentialScopeGlobal) {
			// Some products intentionally use the server-wide coding-agent login.
			// Do not layer a previously saved project credential when the profile
			// makes that choice, or the effective account would disagree with the UI.
			resolvedKeys = MergedProviderAPIKeys(ctx)
		} else if api.chatStore != nil {
			// Product workspaces use the same encrypted per-project credential store
			// as AgentWorks workflows (Claude Code's setup token, Cursor's API key).
			// It stays scoped to this user/workspace and is injected only into the
			// provider runtime, never into a prompt or tool. resolveEffectiveAPIKeys
			// always checks both supported providers and is a no-op for whichever
			// one isn't in use, so this call needs no gate on the resolved provider
			// name — that gate existed before and silently excluded cursor-cli.
			keys, credentialErr := api.resolveEffectiveAPIKeys(ctx, userID, workspacePath, nil)
			if credentialErr != nil {
				return nil, fmt.Errorf("load agent profile %s credential: %w", provider, credentialErr)
			}
			// Returned on resolvedAgentProfile, NOT written back onto req.LLMConfig.
			// The query path reads the resolver's result, so a credential can never
			// originate from the request body.
			resolvedKeys = keys
		}
	}
	if strings.TrimSpace(req.SessionTitle) == "" {
		req.SessionTitle = promptContext.ProjectTitle
	}
	selectedServers := append([]string(nil), req.EnabledServers...)
	if !isGlobalScope && agentprofiles.HasFeature(profile, "mcp") {
		selected, initialized, err := productSelectedServers(ctx, profile.ID, workspacePath)
		if err != nil {
			return nil, fmt.Errorf("load product MCP selection: %w", err)
		}
		if initialized {
			selectedServers = selected
			req.EnabledServers = append([]string(nil), selected...)
			req.Servers = nil
		}
	}
	return &resolvedAgentProfile{Definition: profile, Prompt: rendered, APIKeys: resolvedKeys, SelectedServers: selectedServers, IdentityKey: identityKey,
		ChatConnections: chatMCPConnections(ctx, profile.ID, userID, req.SelectedFolder),
		ChatSecrets:     chatSecretNames(req)}, nil
}

// chatSecretNames lists the secrets the turn will expose to the coding CLI, by name only.
func chatSecretNames(req *QueryRequest) []string {
	if req == nil {
		return nil
	}
	var names []string
	for _, secret := range mergeGlobalSecrets(req.DecryptedSecrets, req.SelectedGlobalSecrets) {
		names = append(names, secret.Name)
	}
	return names
}

// chatMCPConnections are the chat's own connections, resolved exactly as the
// query path adds them to the turn: the Crew's or Code's attached servers. Each
// carries whether its sign-in is done: a server added but not signed in starts
// with no tools, so the chat's retained CLI must relaunch again when the sign-in
// finishes (same list of names, different state), not only when a server is
// added. Without that the CLI kept the tool-less server and the agent said the
// connection was fine but had no tools (RTS, SDE private, Notion, 2026-09-30).
func chatMCPConnections(ctx context.Context, profileID, userID, selectedFolder string) []string {
	if !isProjectProfileID(profileID) {
		return nil
	}
	root := agentProfileRuntimeWorkspace(userID, selectedFolder)
	names, _ := attachedMCPServersForRoot(ctx, root)
	if len(names) == 0 {
		return names
	}
	signedIn := placeMCPSignedInInternalNames(ctx, root)
	out := make([]string, 0, len(names))
	for _, name := range names {
		if signedIn[name] {
			out = append(out, name+"#signed-in")
		} else {
			out = append(out, name+"#signed-out")
		}
	}
	return out
}

func profileRuntimeEventType(event any) string {
	if typed, ok := event.(unifiedevents.EventData); ok {
		if eventType := strings.TrimSpace(string(typed.GetEventType())); eventType != "" {
			return eventType
		}
	}
	if payload, ok := event.(map[string]interface{}); ok {
		if eventType, _ := payload["type"].(string); strings.TrimSpace(eventType) != "" {
			return strings.TrimSpace(eventType)
		}
	}
	return "agent_profile_event"
}

// emitAgentProfileEvent records an agent-profile-emitted event for this
// session's stream.
//
// event should be a value implementing unifiedevents.EventData -- a real
// struct from a schema-gen-registered event package (e.g.
// orchestrator_events.PresentationUpdatedEvent), not a hand-built
// map[string]interface{}. A typed value is used directly as the
// AgentEvent.Data payload, so it serializes at the same nesting depth as
// every other typed event (tool_call_end, llm_generation_end, ...) and gets
// a real generated TypeScript interface via cmd/schema-gen instead of an
// `unknown`-typed blob the frontend has to defensively unwrap.
//
// The map[string]interface{} path still exists underneath for a caller that
// has no registered event type yet, but it comes at a real cost: schema-gen
// has no way to generate a shape for it, so consumers get no compile-time
// guarantee about what is inside, and it wraps one JSON level deeper
// (GenericEventData's own "data" field) than a typed event does. Prefer
// registering a real type (see docs/design/agent_tool_surface_single_source.md
// for why "declared once, consumed everywhere" beats "reconstructed per
// consumer").
func (api *StreamingAPI) emitAgentProfileEvent(sessionID string, event any) {
	if api.eventStore == nil {
		return
	}
	eventType := profileRuntimeEventType(event)
	now := time.Now()

	var data unifiedevents.EventData
	if typed, ok := event.(unifiedevents.EventData); ok {
		stampEventData(typed, now)
		data = typed
	} else {
		payload := map[string]interface{}{"event": event}
		if untyped, ok := event.(map[string]interface{}); ok {
			payload = untyped
		}
		data = &unifiedevents.GenericEventData{Data: payload}
	}

	api.eventStore.AddEvent(sessionID, internalevents.Event{
		ID:        fmt.Sprintf("profile_%s_%d", strings.ReplaceAll(eventType, ".", "_"), now.UnixNano()),
		Type:      eventType,
		Timestamp: now,
		SessionID: sessionID,
		Data: &unifiedevents.AgentEvent{
			Type: unifiedevents.EventType(eventType), Timestamp: now,
			Data: data,
		},
	})
}

// stampEventData gives a product tool's event a real timestamp when its
// embedded BaseEventData was left at zero. A zero inner timestamp sorted the
// event to the year 0001 when a persisted trace was merged back into a
// restored conversation, so a product's suggestion pills vanished after a
// reload because the event landed before the user's message instead of after
// its reply.
func stampEventData(data unifiedevents.EventData, now time.Time) {
	v := reflect.ValueOf(data)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	base := v.Elem().FieldByName("BaseEventData")
	if !base.IsValid() {
		return
	}
	ts := base.FieldByName("Timestamp")
	if !ts.IsValid() || !ts.CanSet() {
		return
	}
	if current, ok := ts.Interface().(time.Time); ok && current.IsZero() {
		ts.Set(reflect.ValueOf(now))
	}
}

func (api *StreamingAPI) registerAgentProfileTools(registrar definitionToolRegistrar, gate *productToolGate, resolved *resolvedAgentProfile, userID, sessionID, workspacePath string, readOnly bool, req ...QueryRequest) error {
	if resolved == nil {
		return nil
	}
	for _, binding := range resolved.Definition.Tools {
		// Crew Run mode: readers keep caller-scoped factories
		// (custom commands) but lose crew mutations (identity,
		// project creation). The tool gate denies the same names
		// as a backstop.
		if readOnly && (binding.ID == "work.set-identity" || binding.ID == "work.create-project") {
			continue
		}
		tool, err := api.agentProfiles.BuildTool(binding, agentprofiles.ToolRuntimeContext{
			UserID: userID, SessionID: sessionID, WorkspacePath: workspacePath,
			Emit:         func(event any) { api.emitAgentProfileEvent(sessionID, event) },
			Presentation: binding.Presentation,
			Interaction:  binding.Interaction,
			Product:      resolved.Definition.Product,
		})
		if err != nil {
			return err
		}
		category := strings.TrimSpace(tool.Category)
		if category == "" {
			category = "agent_profile_tools"
		}
		// profile.tools is itself an explicit capability declaration. The
		// binding uses a factory id while tool_policy uses public tool names,
		// so admit the factory's resolved name instead of requiring products
		// to duplicate it in a second list that can drift out of sync.
		gate.Declare(tool.Name)
		if err := registrar.RegisterCustomTool(tool.Name, tool.Description, tool.Parameters, tool.Execute, category); err != nil {
			return fmt.Errorf("register profile tool %q: %w", tool.Name, err)
		}
	}
	activeWorkProject := isProjectProfileID(resolved.Definition.ID) && isActiveWorkProjectWorkspace(userID, workspacePath)
	if isProjectProfileID(resolved.Definition.ID) && agentprofiles.HasFeature(resolved.Definition, "attached-folders") {
		if err := api.registerWorkFolderTools(registrar, userID, sessionID); err != nil {
			return err
		}
	}
	if activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "workflow-references") {
		if err := api.registerWorkWorkflowReferenceTools(registrar, userID, sessionID, workspacePath, readOnly); err != nil {
			return err
		}
		// Run operations stay: the workflow side re-checks the caller's
		// own workflow access before anything runs.
		if err := api.registerCrewWorkflowRunTools(registrar, userID, sessionID, workspacePath); err != nil {
			return err
		}
		// Crew Run-mode readers never trigger other Crews or workflows.
		if !readOnly {
			// Typed functions (PLAT-357): generated per-function tools for the
			// Crews/workflows tagged in this message or attached to the Crew.
			functionReq := QueryRequest{SelectedFolder: workspacePath}
			if len(req) > 0 {
				functionReq.WorkflowContextPaths = req[0].WorkflowContextPaths
			}
			if err := api.registerCrewFunctionTools(registrar, userID, sessionID, functionReq, crewTriggerLinkCaller(workspacePath), gate.Declare); err != nil {
				return err
			}
		}
	}
	// Someone using another user's Crew can leave its owner a suggestion:
	// a reader in their own chat, or a guest whose call runs as the owner.
	if readOnly && activeWorkProject && resolved.Definition.ID == crewProfileID {
		guest := ""
		if len(req) > 0 {
			guest = crewGuestCallerForTurn(req[0], userID)
		}
		if ref, ok := resolveCrewPath(context.Background(), userID, workspacePath); ok {
			actor := ""
			switch {
			case guest != "":
				actor = guest
			case ref.OwnerID != sanitizeUserIDForPath(userID):
				actor = userID
			}
			if actor != "" {
				gate.Declare(crewSuggestionToolName)
				if err := api.registerCrewSuggestionTool(registrar, actor, sessionID, ref.Root); err != nil {
					return err
				}
			}
		}
		// A guest turn still answers the call it was started for.
		if guest != "" {
			functionReq := QueryRequest{SelectedFolder: workspacePath}
			if err := api.registerCrewFunctionTools(crewFunctionResultOnlyRegistrar{registrar}, userID, sessionID, functionReq, crewTriggerLinkCaller(workspacePath), nil); err != nil {
				return err
			}
		}
	}
	// Signed-in preview links. A Code's links open for its owner only.
	if !readOnly && activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "files") {
		if err := api.registerWorkShareLinkTool(registrar, userID, workspacePath); err != nil {
			return err
		}
	}
	if activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "schedules") {
		if err := api.registerWorkScheduleTools(registrar, resolved.Definition.ID, userID, workspacePath, readOnly); err != nil {
			return err
		}
	}
	if !readOnly && activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "mcp") {
		if err := api.registerWorkMCPSelectionTool(registrar, userID, workspacePath); err != nil {
			return err
		}
	}
	if !readOnly && activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "secrets") {
		if err := api.registerWorkGlobalSecretSelectionTool(registrar, userID, workspacePath); err != nil {
			return err
		}
	}
	if !readOnly && activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "skills") {
		if err := api.registerWorkSkillSelectionTool(registrar, userID, workspacePath); err != nil {
			return err
		}
	}
	if activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "bots") {
		var input QueryRequest
		if len(req) > 0 {
			input = req[0]
		}
		active, _ := api.getActiveSession(sessionID)
		policy := resolveWorkflowChatPolicy(sessionID, input, active, readOnly)
		// Run mode keeps the Slack read tool, as a workflow's Run chat does:
		// a crew answering in Slack reads its own thread (a bot turn's tool
		// is held to the channel and destination it arrived on). Changing
		// the bot's setup needs write access in the app.
		if err := api.registerSlackBotTools(registrar, sessionID, workspacePath, resolved.Definition.ID, !readOnly && policy.Origin == "interactive" && registerWorkUIAllowed(input), !readOnly); err != nil {
			return err
		}
		// Google account tools: everywhere a product keeps its channels, and
		// in a Code (gmail: own) for its owner's chats only.
		ownGmail := agentprofiles.FeatureOption(resolved.Definition, "bots", "gmail") == "own"
		if !readOnly && (agentprofiles.FeatureOption(resolved.Definition, "bots", "dm_only") != "true" || (ownGmail && crewProjectOwnedByCaller(userID, workspacePath))) {
			if err := api.registerGmailConnectionManagementTools(registrar, sessionID, workspacePath); err != nil {
				return err
			}
		}
	}
	if !readOnly && activeWorkProject && agentprofiles.HasFeature(resolved.Definition, "workspace-ui") && len(req) > 0 && registerWorkUIAllowed(req[0]) {
		for _, name := range []string{"list_ui_capabilities", "get_ui_state", "perform_ui_action"} {
			gate.Declare(name)
		}
		if err := api.registerOpenWorkWorkspaceViewTool(registrar, userID, sessionID, workspacePath); err != nil {
			return err
		}
	}
	return nil
}

func profileDisablesVirtualTool(profile *resolvedAgentProfile, toolName string) bool {
	if profile == nil {
		return false
	}
	for _, disabled := range profile.Definition.ToolPolicy.Disabled {
		if strings.EqualFold(strings.TrimSpace(disabled), strings.TrimSpace(toolName)) {
			return true
		}
	}
	return false
}

// agentProfileReadOnlyFolders is what a project-scoped profile may read but
// not write beyond its own workspace: the platform default (skills,
// subagents, Downloads, plus the workflow read-only set) unless the
// profile's sandbox policy names its own list. Entries are normalized to
// the trailing-slash form the folder guard compares with.
// agentProfileChatHistoryGrants is the account chat_history/ folder a product
// profile's shell may read and write — the platform default, so an assistant
// can recall earlier chats — or nothing when the profile's sandbox says
// chat_history: none. The server's own persistence of the profile's log
// does not go through the shell and is unaffected.
func agentProfileChatHistoryGrants(sandbox agentprofiles.SandboxPolicy, perUserChatHistory string) []string {
	if sandbox.ChatHistoryDenied() || strings.TrimSpace(perUserChatHistory) == "" {
		return nil
	}
	return []string{perUserChatHistory}
}

func agentProfileReadOnlyFolders(sandbox agentprofiles.SandboxPolicy, workflowReadOnlyFolders []string) []string {
	if sandbox.ReadOnly == nil {
		return append([]string{"skills/", "subagents/"}, workflowReadOnlyFolders...)
	}
	// sandbox.read_only controls the product's ambient/default read roots. An
	// authorized # workflow reference is request/project context, not an ambient
	// default, and must remain readable even when the product deliberately uses
	// `read_only: []` to disable those defaults.
	out := make([]string, 0, len(sandbox.ReadOnly)+len(workflowReadOnlyFolders))
	for _, folder := range sandbox.ReadOnly {
		clean := strings.Trim(strings.TrimSpace(folder), "/")
		if clean == "" {
			continue
		}
		out = append(out, clean+"/")
	}
	return appendUniqueStrings(out, workflowReadOnlyFolders...)
}
