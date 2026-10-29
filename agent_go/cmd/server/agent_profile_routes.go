package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

const maxAgentProfileRequestBytes = 2 << 20

// AgentProfileChatRequest is the intentionally small public contract for a
// product-owned chat. Prompt, model, tools, skills, permissions and workspace
// all come from the registered profile rather than the browser. Engine is the
// one exception, and only within the profile's own declared bounds: it may
// name one of profile.Runtime.ProviderOptions[].ID, letting the client choose
// among a product-curated set of coding-agent runtimes (see ProviderOption's
// doc comment) — never an arbitrary provider or model.
type AgentProfileChatRequest struct {
	DeliveryMode     string                  `json:"delivery_mode,omitempty"`
	LaptopConnection *laptopConnectionTarget `json:"laptop_connection,omitempty"`
	ConnectionID     string                  `json:"connection_id,omitempty"`
	Message          string                  `json:"message"`
	ConversationKey  string                  `json:"conversation_key,omitempty"`
	Engine           string                  `json:"engine,omitempty"`
	// ModelID picks a model within the engine's provider: one the platform's
	// model catalog lists for that provider (or, when the engine declares its
	// own Models list, one of those). Empty keeps the option's own model.
	ModelID string `json:"model_id,omitempty"`
	// ReasoningEffort picks a level from the engine's declared
	// ReasoningEfforts. Empty keeps whatever the engine's own Options declare.
	ReasoningEffort      string   `json:"reasoning_effort,omitempty"`
	EnabledServers       []string `json:"enabled_servers,omitempty"`
	SelectedSkills       []string `json:"selected_skills,omitempty"`
	WorkflowContextPaths []string `json:"workflow_context_paths,omitempty"`
	// WorkflowContextRefs are the # tag labels for WorkflowContextPaths.
	WorkflowContextRefs []workflowContextRef `json:"workflow_context_refs,omitempty"`
	// KnowledgebaseFolderPath is a validated selection hint, never authority.
	KnowledgebaseFolderPath *string `json:"knowledgebase_folder_path,omitempty"`
	// interactive is set by the web chat route only: a person is sending this message. Bot and
	// email turns leave it false, so a chat they start gets the server account, not a personal plan.
	interactive bool
}

type AgentProfileConversationRequest struct {
	ConversationKey string `json:"conversation_key,omitempty"`
	// ResourceID lets Work resume with the stable project id plus the saved chat
	// id. The server derives the tab-specific conversation key; the browser does
	// not need to understand registry key construction.
	ResourceID string `json:"resource_id,omitempty"`
	// SessionID names an earlier conversation of the slot to make live again
	// (POST …/conversation/switch); the other conversation routes ignore it.
	SessionID string `json:"session_id,omitempty"`
	// Name is required when the key opens a NEW side chat (tab): every agent
	// in a project has a name (owner, 2026-10-19).
	Name string `json:"name,omitempty"`
}

func agentProfileResumeConversationKey(profileID string, input AgentProfileConversationRequest) string {
	if key := strings.TrimSpace(input.ConversationKey); key != "" {
		return key
	}
	resourceID := strings.TrimSpace(input.ResourceID)
	if isProjectProfileID(profileID) && resourceID != "" && strings.TrimSpace(input.SessionID) != "" {
		return resourceID + ":" + strings.TrimSpace(input.SessionID)
	}
	return resourceID
}

type AgentProfileConversationResponse struct {
	TabOwnerID      string `json:"tab_owner_id,omitempty"`
	TabOwnerName    string `json:"tab_owner_name,omitempty"`
	CanWrite        bool   `json:"can_write"`
	IsMainChat      bool   `json:"is_main_chat,omitempty"`
	ConversationID  string `json:"conversation_id"`
	ConversationKey string `json:"conversation_key"`
	SessionID       string `json:"session_id"`
	// Provider and ConnectionID are the coding agent and account the chat is bound to, so the
	// Models panel shows the account actually in use. Empty: none recorded yet.
	Provider     string `json:"provider,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
}

type resolvedResumeTargetContextKey struct{}

func resolveProductResumeTarget(userID string, conversation ProductConversationRecord) (*resolvedResumeTarget, bool, error) {
	workspacePath := normalizeConversationWorkspace(conversation.WorkspacePath)
	// Local Work history lives below the authenticated user's project root.
	// Rebuild that canonical owner prefix after normalizing absolute/runtime
	// variants; the history reader can still fall back to legacy global chats.
	lookupWorkspacePath := workspacePath
	if workspacePath != "" {
		lookupWorkspacePath = workspaceref.PhysicalPath(userID, workspacePath)
	}
	target, ok, err := readRestoredChatHistoryPersistTargetForSession(userID, conversation.SessionID, lookupWorkspacePath)
	if err != nil || !ok || target == nil {
		return nil, false, err
	}
	return &resolvedResumeTarget{
		SessionID:        target.SessionID,
		WorkspacePath:    workspacePath,
		ConversationPath: target.ConversationPath,
		History:          target.History,
		Runtime:          target.Runtime,
		WorkshopMode:     target.WorkshopMode,
	}, true, nil
}

// hasSelectedServers reports whether the browser named a real MCP server
// selection. ChatArea.tsx sends the mcpclient.NoServers sentinel
// unconditionally for every profile-based chat tab, including ones whose
// profile disables MCP selection entirely -- that sentinel means "explicitly
// none", not a selection, and must not trip the capability check below.
func hasSelectedServers(servers []string) bool {
	if len(servers) == 0 {
		return false
	}
	return !(len(servers) == 1 && servers[0] == mcpclient.NoServers)
}

func queryRequestForAgentProfileChat(profile agentprofiles.Profile, input AgentProfileChatRequest, conversation ProductConversationRecord) (QueryRequest, error) {
	if !validChatDeliveryMode(input.DeliveryMode) {
		return QueryRequest{}, fmt.Errorf("delivery_mode must be steer or queue")
	}
	if input.LaptopConnection != nil && (!isProjectProfileID(profile.ID) || !input.LaptopConnection.valid()) {
		return QueryRequest{}, fmt.Errorf("Laptop connection requires a Crew and a valid device and folder")
	}
	if input.KnowledgebaseFolderPath != nil && profile.ID != "knowledgebase" {
		return QueryRequest{}, fmt.Errorf("this profile does not accept Brain context")
	}
	if strings.TrimSpace(conversation.SessionID) == "" || strings.TrimSpace(conversation.WorkspacePath) == "" {
		return QueryRequest{}, fmt.Errorf("product conversation has no runtime binding")
	}
	req := QueryRequest{
		LaptopConnection:            input.LaptopConnection,
		Query:                       input.Message,
		DeliveryMode:                input.DeliveryMode,
		ConnectionID:                firstNonEmptyTrimmed(input.ConnectionID, conversation.ConnectionID),
		SessionTitle:                firstNonEmptyTrimmed(conversation.Title, profile.Name),
		AgentMode:                   "multi-agent",
		AgentProfileID:              profile.ID,
		AgentProfileVersion:         profile.Version,
		AgentProfileConversationKey: conversation.ConversationKey,
		// The registry has already verified that this durable session belongs to
		// the signed-in user and selected product resource. Carry that trusted
		// identity into the shared runner so a reopened product chat can restore
		// its provider-native session, or replay its bounded saved transcript when
		// native resume is unavailable. The narrow product API still gives the
		// browser no way to nominate an arbitrary restore path or session.
		RestoredConversationSessionID: conversation.SessionID,
		AgentProfileContext: agentprofiles.PromptContext{
			ProjectTitle:         firstNonEmptyTrimmed(conversation.Title, profile.Name),
			WorkspaceDescription: conversation.Description,
		},
		SelectedFolder: conversation.WorkspacePath,
	}
	// Work is the general coding product: unlike fixed-purpose profiles, it
	// exposes AgentWorks' existing per-chat MCP and skill selection. The
	// profile resolver still adds its built-in skills and enforces its tool and
	// workspace policy. Other products retain their deliberately narrow input.
	if profile.Runtime.Capabilities.MCPSelection != "" && profile.Runtime.Capabilities.MCPSelection != agentprofiles.CapabilityDisabled {
		if strings.TrimSpace(conversation.ResourceID) != "" {
			req.EnabledServers = appendUniqueStrings(nil, conversation.ProjectSelectedServers...)
			if len(req.EnabledServers) == 0 {
				// Preserve an explicit project-level "none" through the shared
				// query path. This also differs from legacy registry records that
				// silently mounted every connected account MCP, forcing one clean
				// CLI relaunch during migration.
				req.EnabledServers = []string{mcpclient.NoServers}
			}
		} else {
			req.EnabledServers = appendUniqueStrings(nil, input.EnabledServers...)
		}
	} else if hasSelectedServers(input.EnabledServers) {
		return QueryRequest{}, fmt.Errorf("profile %q does not accept user-selected MCP servers", profile.ID)
	}
	if profile.Runtime.Capabilities.SkillSelection != "" && profile.Runtime.Capabilities.SkillSelection != agentprofiles.CapabilityDisabled {
		if strings.TrimSpace(conversation.ResourceID) != "" {
			req.SelectedSkills = appendUniqueStrings(nil, conversation.ProjectSelectedSkills...)
		} else {
			req.SelectedSkills = appendUniqueStrings(nil, input.SelectedSkills...)
		}
	} else if len(input.SelectedSkills) > 0 {
		return QueryRequest{}, fmt.Errorf("profile %q does not accept user-selected skills", profile.ID)
	}
	if profile.Runtime.Capabilities.WorkflowReferences != "" && profile.Runtime.Capabilities.WorkflowReferences != agentprofiles.CapabilityDisabled {
		req.WorkflowContextPaths = appendUniqueStrings(nil, conversation.ProjectWorkflowContextPaths...)
		req.WorkflowContextPaths = appendUniqueStrings(req.WorkflowContextPaths, input.WorkflowContextPaths...)
		req.WorkflowContextRefs = append([]workflowContextRef(nil), input.WorkflowContextRefs...)
	} else if len(input.WorkflowContextPaths) > 0 {
		return QueryRequest{}, fmt.Errorf("profile %q does not accept workflow references", profile.ID)
	}
	// Project schedules, bots and restored clients may not send a browser tab's
	// engine fields. Reuse the same capabilities.llm_config stored in
	// workflow.json that the Work UI uses, just as AgentWorks turns resolve their
	// runtime from workflow.json.
	if strings.TrimSpace(input.Engine) == "" && conversation.ProjectLLMConfig != nil {
		builder := presetPrimaryLLMForChat(conversation.ProjectLLMConfig)
		provider := ""
		modelID := ""
		reasoningEffort := ""
		if builder != nil {
			if req.ConnectionID == "" {
				req.ConnectionID = builder.ConnectionID
			}
			provider = strings.TrimSpace(builder.Provider)
			modelID = strings.TrimSpace(builder.ModelID)
			if effort, ok := builder.Options["reasoning_effort"].(string); ok {
				reasoningEffort = strings.TrimSpace(effort)
			}
		}
		if provider != "" {
			for _, option := range profile.Runtime.ProviderOptions {
				if !strings.EqualFold(strings.TrimSpace(option.Provider), provider) {
					continue
				}
				input.Engine = option.ID
				input.ModelID = modelID
				input.ReasoningEffort = reasoningEffort
				break
			}
			if strings.TrimSpace(input.Engine) == "" {
				return QueryRequest{}, fmt.Errorf("project LLM provider %q is not offered by profile %q", provider, profile.ID)
			}
		}
	}
	// Projects created before capabilities.llm_config was introduced fall back
	// to the conversation's last actual runtime, never the profile default. The
	// Work UI backfills this value into workflow.json when the project is opened.
	if strings.TrimSpace(input.Engine) == "" && conversation.ProjectLLMConfig == nil && strings.TrimSpace(conversation.Provider) != "" {
		for _, option := range profile.Runtime.ProviderOptions {
			if !strings.EqualFold(strings.TrimSpace(option.Provider), strings.TrimSpace(conversation.Provider)) {
				continue
			}
			input.Engine = option.ID
			input.ModelID = conversation.ModelID
			input.ReasoningEffort = conversation.ReasoningEffort
			if req.ConnectionID == "" {
				req.ConnectionID = conversation.ConnectionID
			}
			break
		}
	}
	if engine := strings.TrimSpace(input.Engine); engine != "" {
		option, ok := findProviderOptionByID(profile.Runtime.ProviderOptions, engine)
		if !ok {
			return QueryRequest{}, fmt.Errorf("engine %q is not offered by this profile", engine)
		}
		req.Provider = option.Provider
		req.ModelID = option.ModelID
		if modelID := strings.TrimSpace(input.ModelID); modelID != "" {
			// A saved retired model (claude-sonnet-5) means its replacement:
			// the project keeps working instead of failing validation.
			modelID = currentCodingAgentModel(option.Provider, modelID)
			if !providerOptionOffersModel(option, modelID) {
				return QueryRequest{}, fmt.Errorf("model %q is not offered for engine %q", modelID, engine)
			}
			req.ModelID = modelID
		}
		if effort := strings.TrimSpace(input.ReasoningEffort); effort != "" {
			if !reasoningEffortOffered(option, effort) {
				return QueryRequest{}, fmt.Errorf("reasoning effort %q is not offered for engine %q", effort, engine)
			}
			req.ReasoningEffort = effort
		}
	}
	// An account belongs to one provider. The one inherited from the conversation is only valid
	// while the chat stays on that provider: switching Pi -> Agy with no account picked used to
	// send Pi's account with Agy and fail "provider connection does not match selected provider"
	// (server C 2026-09-30). The new provider then gets its own default account.
	if strings.TrimSpace(input.ConnectionID) == "" && req.ConnectionID != "" &&
		strings.TrimSpace(conversation.Provider) != "" && strings.TrimSpace(req.Provider) != "" &&
		!strings.EqualFold(strings.TrimSpace(conversation.Provider), strings.TrimSpace(req.Provider)) &&
		req.ConnectionID == strings.TrimSpace(conversation.ConnectionID) {
		req.ConnectionID = ""
	}
	return req, nil
}

// providerOptionOffersModel reports whether modelID may be chosen for this
// engine: one of its own curated Models when it declares any, otherwise one
// the platform's model catalog lists for its provider (the same catalog the
// composer's switcher is filled from when the engine declares no curation).
func providerOptionOffersModel(option agentprofiles.ProviderOption, modelID string) bool {
	// Pi runs any "<service>/<model>" its key's service offers: the models a person picked on their own key
	// (OpenRouter, NVIDIA NIM, ...) and custom ids typed into the picker are not in the platform catalog, and
	// rejecting them made a key account's model impossible to change (422, server B 2026-10-08, PLAT-720).
	// Which models an account may use is the account's own list, enforced when the turn runs.
	if accountListedModel(option.Provider, modelID) {
		return true
	}
	if len(option.Models) > 0 {
		for _, id := range option.Models {
			if strings.EqualFold(strings.TrimSpace(id), modelID) {
				return true
			}
		}
		return false
	}
	return providerOffersModel(option.Provider, modelID)
}

// accountListedModel is a model the selected account itself lists, which the static catalog cannot know:
// Pi's "<service>/<model>" on a person's key, and any Cursor model from the account's live --list-models
// (Auto and DeepSeek on paid plans were refused with 422 once the static list dropped Auto, PLAT-872).
// The account's own models are enforced when the turn runs.
func accountListedModel(provider, modelID string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "pi-cli":
		return piServiceModelID(modelID)
	case "cursor-cli":
		return cursorModelID(modelID)
	}
	return false
}

// cursorModelID is one plain Cursor selector ("auto", "composer-2.5", "deepseek-v41-flash-1017-max").
func cursorModelID(modelID string) bool {
	if modelID == "" || len(modelID) > 120 {
		return false
	}
	for _, r := range modelID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}

// piServiceModelID is a "<service>/<model>" id: a lowercase service name, then a non-empty model path of
// ordinary id characters (NVIDIA's "nvidia/z-ai/glm-5.3-flash", OpenRouter's "openrouter/vendor/model:free").
func piServiceModelID(modelID string) bool {
	service, model, ok := strings.Cut(strings.TrimSpace(modelID), "/")
	if !ok || service == "" || model == "" || len(modelID) > 200 {
		return false
	}
	for _, r := range service {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	for _, r := range model {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@+", r)) {
			return false
		}
	}
	return true
}

// reasoningEffortOffered reports whether effort is one of the engine's
// declared ReasoningEfforts. An engine that declares none offers no control
// at all — nothing to compare against, so nothing is accepted.
func reasoningEffortOffered(option agentprofiles.ProviderOption, effort string) bool {
	for _, level := range option.ReasoningEfforts {
		if strings.EqualFold(strings.TrimSpace(level), effort) {
			return true
		}
	}
	return false
}

// providerOffersModel reports whether the platform's model catalog lists
// modelID under provider — the same catalog the composer's switcher is
// filled from, so a client can only send back what it was offered. A
// provider the catalog does not know at all accepts any id: nothing to
// check against.
func providerOffersModel(provider, modelID string) bool {
	known := false
	for _, model := range allProviderModelMetadata() {
		if model == nil || !strings.EqualFold(strings.TrimSpace(model.Provider), strings.TrimSpace(provider)) {
			continue
		}
		known = true
		if strings.EqualFold(strings.TrimSpace(model.ModelID), modelID) {
			return true
		}
	}
	return !known
}

func findProviderOptionByID(options []agentprofiles.ProviderOption, id string) (agentprofiles.ProviderOption, bool) {
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.ID), id) {
			return option, true
		}
	}
	return agentprofiles.ProviderOption{}, false
}

func AgentProfileRoutes(router *mux.Router, registry *agentprofiles.Registry) {
	router.HandleFunc("/agent-profiles", listAgentProfilesHandler(registry)).Methods(http.MethodGet, http.MethodOptions)
	router.HandleFunc("/agent-profiles/{id}", getAgentProfileHandler(registry)).Methods(http.MethodGet, http.MethodOptions)
}

func (api *StreamingAPI) handleAgentProfileChatQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	var input AgentProfileChatRequest
	if err := decoder.Decode(&input); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid profile chat request: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid profile chat request: expected one JSON object")
		return
	}
	if strings.TrimSpace(input.Message) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "message is required")
		return
	}
	if api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "agent profiles are unavailable")
		return
	}

	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	profile, err := api.agentProfiles.Resolve(profileID, 0, GetUserIDFromContext(r.Context()))
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	if !userAllowedProduct(GetUserFromContext(r.Context()), profile.Product) || !canUseCapLayerProfile(r.Context(), profile.ID) {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	// A person is using the product right now: the quiet rule must hold any
	// due schedule back. Scheduled runs enter through Run, never here, so a
	// check-in's own turns are not mistaken for family activity.
	productInteractions.Note(r.Context(), GetUserIDFromContext(r.Context()), profile.Product)
	conversation, err := api.resolveAgentProfileConversation(r, profile, input.ConversationKey, "")
	if err != nil {
		writeAgentProfileQueryError(w, err)
		return
	}
	if err := crewTabWriteError(r.Context(), productWorkspaceUserID(r.Context()), profile.ID, conversation.ConversationKey, conversation.WorkspacePath); err != nil {
		externalError(w, http.StatusForbidden, "tab_owner", err.Error())
		return
	}
	input.interactive = true
	query, err := prepareProductConversationTurn(r.Context(), productWorkspaceUserID(r.Context()), profile, input, conversation)
	if err != nil {
		writeAgentProfileQueryError(w, err)
		return
	}
	if input.KnowledgebaseFolderPath != nil {
		folder := *input.KnowledgebaseFolderPath
		if len(folder) > 1024 {
			writeAgentProfileError(w, 400, "invalid Brain folder")
			return
		}
		service, err := knowledgebaseService()
		if err != nil {
			knowledgebaseHTTPError(w, r, "store_open", err)
			return
		}
		if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
			knowledgebaseHTTPError(w, r, "identity_sync", err)
			return
		}
		if _, err = service.Call(r.Context(), knowledgebasePrincipal(r, GetUserFromContext(r.Context())), "list_knowledgebase_folders", map[string]any{"folder_path": folder, "depth": 1}); err != nil {
			knowledgebaseHTTPError(w, r, "folder_check", err)
			return
		}
		query.Query = "Selected Brain folder (context only): " + strconv.Quote(folder) + "\n\n" + query.Query
	}

	encoded, err := json.Marshal(query)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "encode profile chat request")
		return
	}

	// Preserve authentication, cancellation and X-Session-ID, but replace the
	// broad browser-authored QueryRequest with the server-authored profile turn.
	// This is the migration seam: product clients now have a stable minimal API;
	// the shared turn runner can be extracted from handleQuery behind this seam
	// without another frontend migration.
	// The rootless product gateway identifies its loopback caller as the
	// product (for example "sparkquill"), while a single-user deployment's
	// durable workspace remains owned by DEFAULT_USER_ID. Keep that gateway
	// identity out of the shared query path so the folder guard, project
	// initializer, history and registry all address the same project files.
	forwardedContext := productWorkspaceContext(r.Context())
	if query.resolvedResumeTarget != nil {
		forwardedContext = context.WithValue(forwardedContext, resolvedResumeTargetContextKey{}, query.resolvedResumeTarget)
	}
	forwarded := r.Clone(forwardedContext)
	forwarded.Body = io.NopCloser(bytes.NewReader(encoded))
	forwarded.ContentLength = int64(len(encoded))
	forwarded.Header = r.Header.Clone()
	forwarded.Header.Set("Content-Type", "application/json")
	forwarded.Header.Set("X-Session-ID", conversation.SessionID)
	api.handleQuery(w, forwarded)
}

func (api *StreamingAPI) handleResolveAgentProfileConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	var input AgentProfileConversationRequest
	if err := decoder.Decode(&input); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid product conversation request: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid product conversation request: expected one JSON object")
		return
	}
	if api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "agent profiles are unavailable")
		return
	}
	profile, err := api.agentProfiles.Resolve(strings.TrimSpace(mux.Vars(r)["id"]), 0, GetUserIDFromContext(r.Context()))
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	if !userAllowedProduct(GetUserFromContext(r.Context()), profile.Product) || !canUseCapLayerProfile(r.Context(), profile.ID) {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	// Opening the product's conversation is what the app does on launch: the
	// family is here, so a due check-in waits for a quiet moment.
	productInteractions.Note(r.Context(), GetUserIDFromContext(r.Context()), profile.Product)
	conversation, err := api.resolveAgentProfileConversation(r, profile, input.ConversationKey, input.Name)
	if err != nil {
		log.Printf("[PRODUCT_CONVERSATION] resolve refused profile=%s key=%q: %v", profile.ID, input.ConversationKey, err)
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ownerID, ownerName, canWrite := crewConversationTabAccess(r.Context(), productWorkspaceUserID(r.Context()), profile, conversation)
	writeAgentProfileJSON(w, http.StatusOK, AgentProfileConversationResponse{
		TabOwnerID: ownerID, TabOwnerName: ownerName, CanWrite: canWrite,
		IsMainChat:      conversation.IsMainChat,
		ConversationID:  conversation.ConversationID,
		ConversationKey: conversation.ConversationKey,
		SessionID:       conversation.SessionID,
		Provider:        conversation.Provider,
		ConnectionID:    conversation.ConnectionID,
	})
}

// handleRotateAgentProfileConversation is the server-owned implementation of
// “New chat” for products. A browser cannot safely rotate a product session by
// inventing an ID because keyed products bind their chat to a durable project
// manifest; the server updates that binding and the registry together.
func (api *StreamingAPI) handleRotateAgentProfileConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	var input AgentProfileConversationRequest
	if err := decoder.Decode(&input); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid product conversation request: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid product conversation request: expected one JSON object")
		return
	}
	if api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "agent profiles are unavailable")
		return
	}
	userID := productWorkspaceUserID(r.Context())
	profile, err := api.agentProfiles.Resolve(strings.TrimSpace(mux.Vars(r)["id"]), 0, userID)
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	if !userAllowedProduct(GetUserFromContext(r.Context()), profile.Product) || !canUseCapLayerProfile(r.Context(), profile.ID) {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	binding, _, err := resolveConversationBindingForUser(r.Context(), userID, profile, input.ConversationKey)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := crewTabWriteError(r.Context(), userID, profile.ID, binding.ConversationKey, binding.WorkspacePath); err != nil {
		externalError(w, 403, "tab_owner", err.Error())
		return
	}
	conversation, err := defaultProductConversationRegistryStore().rotate(r.Context(), userID, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ownerID, ownerName, canWrite := crewConversationTabAccess(r.Context(), productWorkspaceUserID(r.Context()), profile, conversation)
	writeAgentProfileJSON(w, http.StatusOK, AgentProfileConversationResponse{
		TabOwnerID: ownerID, TabOwnerName: ownerName, CanWrite: canWrite,
		IsMainChat:      conversation.IsMainChat,
		ConversationID:  conversation.ConversationID,
		ConversationKey: conversation.ConversationKey,
		SessionID:       conversation.SessionID,
	})
}

func initializeProductConversationWorkspace(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding) error {
	if profile.ID == caplayerproduct.ProfileID {
		if err := ensureVaultChatWorkspace(ctx, userID); err != nil {
			return err
		}
	}
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
	// Work projects have a conventional source-code home. Creating it here is
	// idempotent and also upgrades projects created before the convention was
	// introduced. The frontend creates it eagerly so it is visible immediately;
	// this server-side guard keeps non-UI callers consistent.
	if isProjectProfileID(profile.ID) {
		if err := client.CreateFolder(ctx, filepath.ToSlash(filepath.Join(binding.WorkspacePath, "code"))); err != nil {
			return fmt.Errorf("initialize product code folder: %w", err)
		}
	}
	// Database is a product feature, so its physical managed SQLite location is
	// part of enabling that feature too. Create the valid empty database through
	// the trusted server-to-workspace channel when a project is first opened;
	// repeat calls are idempotent and older projects are upgraded automatically.
	if agentprofiles.HasFeature(profile, "database") {
		if _, err := client.InitializeWorkflowDB(ctx, workspace.InitializeWorkflowDBParams{
			DBPath: filepath.ToSlash(filepath.Join(binding.WorkspacePath, "db", "db.sqlite")),
		}); err != nil {
			return fmt.Errorf("initialize product database: %w", err)
		}
	}
	return nil
}

// A new side chat (tab) needs name; it becomes the tab's agent name and title.
func (api *StreamingAPI) resolveAgentProfileConversation(r *http.Request, profile agentprofiles.Profile, requestedKey, name string) (ProductConversationRecord, error) {
	userID := productWorkspaceUserID(r.Context())
	// Resolve current project access before treating a missing registry entry
	// as a new tab. A stale dashboard can still carry a removed chat's key.
	binding, owned, err := resolveConversationBindingForUser(r.Context(), userID, profile, requestedKey)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	if owned {
		if tab, found, tabErr := crewTabForKey(r.Context(), userID, profile.ID, binding.ConversationKey, binding.WorkspacePath); tabErr != nil {
			return ProductConversationRecord{}, tabErr
		} else if found && tab.OwnerID != userID {
			return tab.Record, nil
		}
	}
	if binding.ConversationKey == crewOwnerDefaultChatKey(binding.ResourceID, userID) {
		name = crewOwnerDisplayName(userID) + " · Builder"
	}
	unlock, isNew, capErr := guardNewSideChat(r.Context(), userID, profile.ID, binding.ConversationKey)
	if capErr != nil {
		return ProductConversationRecord{}, capErr
	}
	defer unlock()
	if isNew {
		if strings.TrimSpace(name) == "" {
			return ProductConversationRecord{}, fmt.Errorf("Give the new tab a name, a few words saying what this agent is for.")
		}
		normalized, err := normalizeChatHistoryTitle(name)
		if err != nil {
			return ProductConversationRecord{}, err
		}
		name = normalized
	}
	if agentID := publicationAgentID(requestedKey); agentID != "" {
		if !owned {
			return ProductConversationRecord{}, fmt.Errorf("Open this agent from a Crew you may build; shared use is through its dashboard and functions.")
		}
		record, err := readPublishedAgent(r.Context(), agentProfileRuntimeWorkspace(userID, binding.WorkspacePath), agentID)
		if err != nil {
			return ProductConversationRecord{}, err
		}
		if record == nil {
			return ProductConversationRecord{}, fmt.Errorf("This agent is unavailable. Choose one from the Crew's Agents list.")
		}
	}

	if owned {
		if err := initializeProductConversationWorkspace(r.Context(), userID, profile, binding); err != nil {
			return ProductConversationRecord{}, err
		}
	}

	candidate := strings.TrimSpace(r.Header.Get("X-Session-ID"))
	continuation := strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Conversation-Continuation")), "true")
	if candidate != "" && !api.canUseSessionIDForQuery(r, candidate) {
		return ProductConversationRecord{}, fmt.Errorf("requested product conversation belongs to another user")
	}
	preferredSessionID := ""
	preferredSessionVerifiedForWorkspace := false
	if binding.AuthoritativeSessionID == "" {
		if candidate != "" && api.canUseSessionIDForQuery(r, candidate) {
			if active, ok := api.getActiveSession(candidate); ok && sessionVisibleTo(active.UserID, GetUserFromContext(r.Context())) {
				preferredSessionID = candidate
			} else if _, found, findErr := FindChatHistoryConversationPathForSession(userID, candidate, ""); findErr != nil {
				return ProductConversationRecord{}, fmt.Errorf("find existing product conversation: %w", findErr)
			} else if found {
				preferredSessionID = candidate
			}
			if session, found := chatHistorySessionsByID(userID, binding.WorkspacePath)[candidate]; found {
				preferredSessionVerifiedForWorkspace = chatHistorySessionWorkspace(session) == normalizeConversationWorkspace(binding.WorkspacePath)
			}
		}
	}
	if preferredSessionID != "" {
		continuation = true
		if !preferredSessionVerifiedForWorkspace {
			api.sessionWorkspaceMu.RLock()
			activeWorkspace := api.sessionWorkspaceFolders[candidate]
			api.sessionWorkspaceMu.RUnlock()
			preferredSessionVerifiedForWorkspace = activeWorkspace != "" && normalizeConversationWorkspace(activeWorkspace) == normalizeConversationWorkspace(binding.WorkspacePath)
		}
		if !preferredSessionVerifiedForWorkspace {
			// A session's history is only saved when its turn ends, so a follow-up sent while
			// the first turn still runs (or a session that has not saved yet) has no history to
			// verify against. The server's own registry entry for this very slot, bound to this
			// project, is the same proof.
			if current, found, _, historyErr := defaultProductConversationRegistryStore().history(r.Context(), userID, profile, binding); historyErr == nil && found &&
				strings.TrimSpace(current.SessionID) == candidate &&
				normalizeConversationWorkspace(current.WorkspacePath) == normalizeConversationWorkspace(binding.WorkspacePath) {
				preferredSessionVerifiedForWorkspace = true
			}
		}
		if isProjectProfileID(profile.ID) && !preferredSessionVerifiedForWorkspace {
			return ProductConversationRecord{}, fmt.Errorf("conversation continuity conflict: requested session is not verified in this project")
		}
	}
	if continuation && candidate == "" {
		return ProductConversationRecord{}, fmt.Errorf("continuation requires an explicit session ID")
	}
	store := defaultProductConversationRegistryStore()
	record, err := store.resolveOrCreate(r.Context(), userID, profile, binding, preferredSessionID)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	// An open Work tab may survive a browser refresh and many backend
	// deployments. Its authenticated X-Session-ID is the conversation the user
	// can actually see. If a tab-specific registry slot drifted to another
	// session, repair it on the send boundary before the agent launches. The
	// candidate must already exist in this exact project workspace; a caller
	// cannot use this to nominate an arbitrary or another user's session.
	if shouldRebindWorkConversation(profile, binding, record, preferredSessionID, preferredSessionVerifiedForWorkspace) {
		record, err = store.switchTo(r.Context(), userID, profile, binding, preferredSessionID, true)
		if err != nil {
			return ProductConversationRecord{}, fmt.Errorf("restore open Work conversation: %w", err)
		}
	}
	if err := validateProductConversationContinuation(continuation, candidate, record.SessionID); err != nil {
		return ProductConversationRecord{}, err
	}
	if !api.canUseSessionIDForQuery(r, record.SessionID) {
		return ProductConversationRecord{}, fmt.Errorf("product conversation session belongs to another user")
	}
	if isNew {
		if err := setCodeTabName(r.Context(), userID, record.ConversationKey, name, profile.ID); err != nil {
			return ProductConversationRecord{}, err
		}
		_ = RenameChatHistorySession(userID, record.SessionID, binding.WorkspacePath, name)
	}
	if owned && (record.IsMainChat || binding.ConversationKey == crewOwnerDefaultChatKey(binding.ResourceID, userID)) {
		if identity, e := store.agentIdentity(r.Context(), userID, profile.ID, record.ConversationKey); e == nil && identity.Name == "" {
			title := crewOwnerDisplayName(userID) + " · Builder"
			if e = setCodeTabName(r.Context(), userID, record.ConversationKey, title, profile.ID); e != nil {
				return ProductConversationRecord{}, e
			}
		}
	}
	return record, nil
}

func shouldRebindWorkConversation(
	profile agentprofiles.Profile,
	binding productConversationBinding,
	record ProductConversationRecord,
	preferredSessionID string,
	verifiedForWorkspace bool,
) bool {
	if !isProjectProfileID(profile.ID) || !verifiedForWorkspace || binding.ProjectMainChatKey != "" {
		return false
	}
	// The base project key is the permanent Builder/current-project slot and is
	// governed by product.json. Only tab-specific keys may follow an already-open
	// browser tab back to its durable session.
	if strings.TrimSpace(binding.ResourceID) == "" || strings.TrimSpace(binding.ConversationKey) == strings.TrimSpace(binding.ResourceID) {
		return false
	}
	preferredSessionID = strings.TrimSpace(preferredSessionID)
	return preferredSessionID != "" && preferredSessionID != strings.TrimSpace(record.SessionID)
}

// productWorkspaceUserID preserves true per-user isolation where it is
// enabled. A dedicated single-user product deployment is different: its
// gateway uses an internal product principal for the reverse-proxy JWT, but
// all durable project data belongs to the configured default owner.
func productWorkspaceUserID(ctx context.Context) string {
	if !IsMultiUserMode() {
		return GetDefaultUserID()
	}
	return GetUserIDFromContext(ctx)
}

// productWorkspaceContext makes the downstream shared /query path use the
// same owner chosen by productWorkspaceUserID. It deliberately leaves every
// other claim intact (including access level), changing only the workspace
// namespace in the single-user gateway case.
func productWorkspaceContext(ctx context.Context) context.Context {
	if IsMultiUserMode() {
		return ctx
	}
	claims := GetUserFromContext(ctx)
	if claims == nil {
		return ctx
	}
	copy := *claims
	copy.UserID = GetDefaultUserID()
	return context.WithValue(ctx, UserContextKey, &copy)
}

func listAgentProfilesHandler(registry *agentprofiles.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		userID := GetUserIDFromContext(r.Context())
		profiles := registry.List(userID)
		claims := GetUserFromContext(r.Context())
		visible := profiles[:0]
		for _, profile := range profiles {
			if userAllowedProduct(claims, profile.Product) && canUseCapLayerProfile(r.Context(), profile.ID) {
				visible = append(visible, profileWithAvailableProviders(profile.ForProducts(builderProductSelection(claims))))
			}
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{
			"profiles": visible,
		})
	}
}

func getAgentProfileHandler(registry *agentprofiles.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		version := 0
		if rawVersion := strings.TrimSpace(r.URL.Query().Get("version")); rawVersion != "" {
			parsed, err := strconv.Atoi(rawVersion)
			if err != nil || parsed < 1 {
				writeAgentProfileError(w, http.StatusBadRequest, "version must be a positive integer")
				return
			}
			version = parsed
		}
		profileID := mux.Vars(r)["id"]
		profile, err := registry.Resolve(profileID, version, GetUserIDFromContext(r.Context()))
		// Relay's product catalog is readable here, but its chats keep using
		// the authorized workflow Builder path, not generic profile execution.
		if profileID == "relays" {
			if !productEnabled("relays") {
				writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
				return
			}
			profiles, catalogErr := relayproduct.BuiltinAgentProfiles()
			if catalogErr != nil {
				writeAgentProfileError(w, http.StatusInternalServerError, "product catalog unavailable")
				return
			}
			profile, err = profiles[0], nil
			profile.Product = "relays"
			if version != 0 && version != profile.Version {
				writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
				return
			}
		}
		if err != nil {
			writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
			return
		}
		if !userAllowedProduct(GetUserFromContext(r.Context()), profile.Product) || !canUseCapLayerProfile(r.Context(), profile.ID) {
			writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
			return
		}
		profile = limitProfileModelsToAccount(r.Context(), GetUserIDFromContext(r.Context()), profileWithAvailableProviders(profile.ForProducts(builderProductSelection(GetUserFromContext(r.Context())))))
		if r.URL.Query().Get("include_skill_catalog") != "true" {
			writeAgentProfileJSON(w, http.StatusOK, profile)
			return
		}
		defaults, err := agentProfileSkillDefaults(profile)
		if err != nil {
			writeAgentProfileError(w, http.StatusInternalServerError, "product skill defaults unavailable")
			return
		}
		selection := builderProductSelection(GetUserFromContext(r.Context()))
		for mode, names := range defaults {
			allowed := []string{}
			for _, name := range names {
				if selection.AllowsSkill(name) {
					allowed = append(allowed, name)
				}
			}
			defaults[mode] = allowed
		}
		definitions, err := agentProfileSkillDefinitions(profile, defaults, selection)
		if err != nil {
			writeAgentProfileError(w, http.StatusInternalServerError, "product skill definitions unavailable")
			return
		}
		// Read-only presentation metadata, after the same profile/product admission.
		// The panel must not infer mode defaults from installed filenames.
		writeAgentProfileJSON(w, http.StatusOK, struct {
			agentprofiles.Profile
			SkillDefaultsByMode    map[string][]string       `json:"skill_defaults_by_mode"`
			SkillDefinitionsByMode map[string][]skills.Skill `json:"skill_definitions_by_mode"`
		}{profile, defaults, definitions})
	}
}

// These are declared product defaults, not a claim about a running session or
// its selected domain skills. Use the same manifests as each chat runtime.
func agentProfileSkillDefaults(profile agentprofiles.Profile) (map[string][]string, error) {
	defaults := map[string][]string{"profile": appendUniqueStrings(nil, profile.Skills...)}
	if !profile.BuiltIn {
		return defaults, nil
	}
	switch profile.ID {
	case workproduct.ProfileID:
		manifest, err := workproduct.WorkManifest()
		if err != nil {
			return nil, err
		}
		defaults = map[string][]string{
			"builder": appendUniqueStrings(append([]string(nil), profile.Skills...), manifest.Chat["builder"].Skills...),
			"run":     appendUniqueStrings(nil, manifest.Chat["run"].Skills...),
		}
	case "agentworks":
		defaults = map[string][]string{}
		for _, mode := range []string{"builder", "run"} {
			defaults[mode] = appendUniqueStrings(append([]string(nil), profile.Skills...), agentworksproduct.ChatSkills(mode)...)
		}
	case "relays":
		manifest, err := relayproduct.Manifest()
		if err != nil {
			return nil, err
		}
		defaults = map[string][]string{}
		for _, mode := range []string{"builder", "run"} {
			defaults[mode] = appendUniqueStrings(append([]string(nil), profile.Skills...), manifest.Chat[mode].Skills...)
		}
	}
	return defaults, nil
}

// Product provider options remain visible; runtime/auth readiness is reported
// by the provider manifest rather than silently removing an engine.
func profileWithAvailableProviders(profile agentprofiles.Profile) agentprofiles.Profile {
	return profileWithProductDefault(context.Background(), profile)
}

func writeAgentProfileQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, errCrewProjectUnavailable) {
		externalError(w, http.StatusForbidden, "project_unavailable", errCrewProjectUnavailable.Error())
		return
	}
	writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
}

func writeAgentProfileError(w http.ResponseWriter, status int, message string) {
	writeAgentProfileJSON(w, status, map[string]string{"error": message})
}

func writeAgentProfileJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// prepareProductConversationTurn is shared by every product-chat adapter. It
// owns history/native restoration and runtime rebinding, so connector turns
// pick up the same migrations as web turns.
func prepareProductConversationTurn(ctx context.Context, userID string, profile agentprofiles.Profile, input AgentProfileChatRequest, conversation ProductConversationRecord) (QueryRequest, error) {
	// A chat with its own model (chosen by the person on the Crew's Models page) runs on it for every turn:
	// typed in the app, or a workflow, function, schedule or another chat reaching it. The browser sends
	// the Crew's model for every tab, so the server's per-chat choice wins.
	modelConversation := conversation
	own, err := defaultProductConversationRegistryStore().agentIdentity(ctx, userID, profile.ID, conversation.ConversationKey)
	if err != nil {
		return QueryRequest{}, err
	}
	if own.Engine == "" {
		useProjectChatDefault(&input, &modelConversation, profile)
	} else {
		if _, offered := findProviderOptionByID(profile.Runtime.ProviderOptions, own.Engine); offered {
			if own.ConnectionID != "" {
				input.ConnectionID = own.ConnectionID
			} else if !strings.EqualFold(strings.TrimSpace(input.Engine), own.Engine) {
				input.ConnectionID = ""
			}
			input.Engine, input.ModelID, input.ReasoningEffort = own.Engine, own.ModelID, own.ReasoningEffort
		}
	}
	query, err := queryRequestForAgentProfileChat(profile, input, modelConversation)
	if err != nil {
		return QueryRequest{}, err
	}
	if err := constrainProductChatModel(ctx, &query); err != nil {
		return QueryRequest{}, err
	}
	// "1M context" in the tab's model menu (PLAT-872-71): the same model with its 1M window. A model
	// change makes bindRuntimeConfiguration below relaunch the CLI, which resumes the same conversation.
	if own.Context1M && strings.EqualFold(strings.TrimSpace(query.Provider), "claude-code") {
		query.ModelID = claudecode.With1MContext(query.ModelID)
		if query.LLMConfig != nil {
			configCopy := *query.LLMConfig
			configCopy.Primary.ModelID = query.ModelID
			query.LLMConfig = &configCopy
		}
	}
	applyUserViewChat(&query, userID, conversation.ConversationKey, conversation.WorkspacePath)
	// A chat has one account, and only the person's choice in Models changes it (owner, 2026-10-07).
	// When neither the request nor the chat names one for this provider (a new chat, or the first
	// turn after a provider switch), it is resolved here once, the same way the turn itself would
	// resolve it, and recorded on the chat below. Before, the first turn recorded none and ran on
	// the person's own account, and the second pinned the server account: a restart and a silent
	// move onto the shared account (PLAT-676).
	filledAccount := false
	if strings.TrimSpace(query.ConnectionID) == "" && strings.TrimSpace(query.Provider) != "" {
		query.ConnectionID = productChatDefaultAccount(ctx, userID, profile, query.Provider, input.interactive)
		filledAccount = true
	}
	// Filling in the account a chat with none recorded already ran on is not a change.
	fillsUnrecorded := filledAccount && strings.TrimSpace(conversation.ConnectionID) == ""
	// An account change is always an explicit choice here: an omitted account inherits the bound
	// one. The chat stays one conversation with its history; its CLI restarts on the new account
	// (bindRuntimeConfiguration below), and the old account's saved native session cannot resume
	// under another login, so it is not resumed.
	accountChanged := conversation.Provider != "" && !fillsUnrecorded &&
		canonicalProviderConnectionID(conversation.Provider, conversation.ConnectionID) != canonicalProviderConnectionID(query.Provider, query.ConnectionID) &&
		strings.EqualFold(strings.TrimSpace(conversation.Provider), strings.TrimSpace(query.Provider)) &&
		(strings.TrimSpace(conversation.ConnectionID) != "" || strings.TrimSpace(query.ConnectionID) != "")
	if accountChanged {
		log.Printf("[PRODUCT_CHAT] account change for conversation %q: %s -> %s (same chat, CLI restarts on the new account)", conversation.ConversationKey, canonicalProviderConnectionID(conversation.Provider, conversation.ConnectionID), canonicalProviderConnectionID(query.Provider, query.ConnectionID))
	}
	// "Fresh chat" waits for a running turn to end: a message queued behind it would resume the old conversation.
	fresh := conversation.FreshNative && !(runningServerAPI != nil && runningServerAPI.conversationTurnOccupied(conversation.SessionID))
	target, found, err := resolveProductResumeTarget(userID, conversation)
	if err != nil {
		return QueryRequest{}, fmt.Errorf("resolve saved conversation: %w", err)
	}
	if found {
		if accountChanged && target != nil {
			// Keep the visible history; start a fresh CLI session on the new account.
			target.Runtime = nil
		}
		if fresh && target != nil {
			// "Fresh chat" (PLAT-872-71): same as an account change. The visible history stays and the new
			// native conversation starts from the continuity notice (recent turns and the archive path).
			target.Runtime = nil
			log.Printf("[CHAT_CONTEXT] fresh native conversation for %q session=%s", conversation.ConversationKey, conversation.SessionID)
		}
		query.resolvedResumeTarget = target
	}
	if fresh {
		if err := defaultProductConversationRegistryStore().setFreshNative(ctx, userID, profile.ID, conversation.ConversationKey, false); err != nil {
			return QueryRequest{}, err
		}
	}
	if strings.TrimSpace(query.Provider) != "" {
		skillFilesDigest := conversation.SkillFilesDigest
		if workspaceref.MustParse(conversation.WorkspacePath).IsShared() {
			if digest, err := skills.WorkspaceSkillDigest(getWorkspaceAPIURL(), conversation.WorkspacePath, query.SelectedSkills); err == nil {
				skillFilesDigest = digest
			} else {
				log.Printf("[SKILLS] workspace=%q could not check changed skill files: %v", conversation.WorkspacePath, err)
			}
		}
		_, restart, err := defaultProductConversationRegistryStore().bindRuntimeConfiguration(ctx, userID, profile, conversation.ConversationKey, query.Provider, query.ModelID, query.ReasoningEffort, query.EnabledServers, query.SelectedSkills, query.WorkflowContextPaths, productConversationAccount{ConnectionID: query.ConnectionID, FillsUnrecorded: fillsUnrecorded, SkillFilesDigest: skillFilesDigest})
		if err != nil {
			return QueryRequest{}, err
		}
		restart = restart || fresh // the retained CLI holds the old conversation
		// A previous attempt may already have persisted the new selection while
		// leaving the old durable session alive. Check the actual retained runtime
		// too, so retrying that selection cannot deliver to the old model.
		retained, retainedExists := mcpagent.LookupSession(conversation.SessionID)
		if retainedExists {
			if handle := retained.Snapshot(); handle != nil {
				restart = restart ||
					(handle.Provider.Provider != "" && !strings.EqualFold(handle.Provider.Provider, query.Provider)) ||
					(handle.Provider.Model != "" && query.ModelID != "" && !strings.EqualFold(handle.Provider.Model, query.ModelID))
			}
		}
		if restart {
			query.DisableLiveInputDelivery = true
			if runningServerAPI != nil && runningServerAPI.conversationTurnOccupied(conversation.SessionID) {
				// A turn is running: never close its CLI here (PLAT-676, the second message of a chat
				// killed the first run). handleQuery queues this message and restarts the CLI when it runs.
				query.RestartCodingCLI = true
			} else {
				retireProductCodingCLI(conversation.SessionID, "product chat: runtime configuration changed")
			}
		}
	}
	return query, nil
}

// productChatDefaultAccount is the account a product chat that names none runs on: for a person
// using the chat, their own signed-in account for the provider (ownDefaultProviderAccountID, owner
// decision 2026-09-30); otherwise, and for a global-scope profile, the server account. It is the
// same rule resolveAgentProfileForQuery applied at run time, now applied once and recorded.
func productChatDefaultAccount(ctx context.Context, userID string, profile agentprofiles.Profile, provider string, interactive bool) string {
	provider = strings.TrimSpace(provider)
	if interactive && profile.EffectiveScope() != agentprofiles.ProfileScopeGlobal {
		if own := ownDefaultProviderAccountID(ctx, userID, provider); own != "" {
			return own
		}
	}
	return "global:" + provider
}

// retireProductCodingCLI closes a product chat's coding CLI and its retained Session.Send target, so
// the next turn relaunches the CLI with the chat's current runtime. Closing a provider CLI alone does
// not remove the transport-neutral Send target.
func retireProductCodingCLI(sessionID, reason string) {
	closeCodingCLIAndReleaseTurnMarkers(sessionID, reason)
	if retained, ok := mcpagent.LookupSession(sessionID); ok {
		_ = retained.Close()
	}
}

// Fresh browser tabs may carry provisional IDs. An acknowledged continuation
// must never silently receive a different registry conversation.
func validateProductConversationContinuation(continuation bool, requested, resolved string) error {
	if continuation && (strings.TrimSpace(requested) == "" || strings.TrimSpace(requested) != strings.TrimSpace(resolved)) {
		return fmt.Errorf("conversation continuity conflict: requested session could not be verified in this workspace; reload or recover the original chat")
	}
	return nil
}

// sideChatLocks serialises side-chat creation per user and project.
var sideChatLocks sync.Map

func sideChatLock(userID, profileID, key string) *sync.Mutex {
	project, _, _ := strings.Cut(strings.TrimSpace(key), codeChatSideMarker)
	lock, _ := sideChatLocks.LoadOrStore(userID+"|"+profileID+"|"+project, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// limitsSideChats is true for the profiles whose projects have tabs: Code projects and Crews.
func limitsSideChats(profileID string) bool { return profileID == crewProfileID || profileID == "work" }

// guardNewSideChat takes the project's side-chat lock and refuses a NEW side chat past the cap;
// the caller holds the lock until the chat exists, so two requests cannot both pass the check.
// Every route that can create a side chat's registry entry (open, resume, the shared resolver)
// goes through it. Other keys get a no-op.
func guardNewSideChat(ctx context.Context, userID, profileID, key string) (unlock func(), isNew bool, err error) {
	if !limitsSideChats(profileID) || !isSideChatConversationKey(key) {
		return func() {}, false, nil
	}
	lock := sideChatLock(userID, profileID, key)
	lock.Lock()
	isNew, err = checkSideChatCapacity(ctx, defaultProductConversationRegistryStore(), userID, profileID, key)
	if err != nil {
		lock.Unlock()
		return nil, false, err
	}
	return lock.Unlock, isNew, nil
}

// checkSideChatCapacity refuses a NEW side chat key once the project already has the most the
// product allows (externalCrewMaxSideChats). The count is the registry's, not a browser's tabs.
// isNew reports that key is not open yet.
func checkSideChatCapacity(ctx context.Context, store productConversationRegistryStore, userID, profileID, key string) (isNew bool, err error) {
	project, _, _ := strings.Cut(strings.TrimSpace(key), codeChatSideMarker)
	records, err := store.projectChats(ctx, userID, profileID, project)
	if err != nil {
		return false, nil // an unreadable registry is the resolve step's error to report
	}
	sides := 0
	for _, record := range records {
		if record.ConversationKey == strings.TrimSpace(key) {
			return false, nil // already open: not a new chat
		}
		if !record.IsMainChat && record.ConversationKey != crewOwnerDefaultChatKey(project, userID) {
			sides++
		}
	}
	if sides >= externalCrewMaxSideChats {
		return true, fmt.Errorf("this project already has %d side chats; close one first", externalCrewMaxSideChats)
	}
	return true, nil
}

// isSideChatConversationKey accepts only a side chat's key, "<projectId>:chat:<id>";
// the main chat ("<projectId>") and anything else are never closed this way.
func isSideChatConversationKey(key string) bool {
	project, chatID, ok := strings.Cut(strings.TrimSpace(key), codeChatSideMarker)
	return ok && project != "" && !strings.Contains(project, ":") && chatID != "" && !strings.Contains(chatID, ":")
}

// handleCloseAgentProfileSideChat forgets a closed side chat (tab): its
// registry entry goes, so other chats can no longer message it and reminders
// set from it run in the main chat. Its transcript is kept. Before this a
// closed tab stayed reachable (PLAT-648, PLAT-653).
func (api *StreamingAPI) handleCloseAgentProfileSideChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	var input AgentProfileConversationRequest
	if err := decoder.Decode(&input); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid product conversation request: "+err.Error())
		return
	}
	if !isSideChatConversationKey(input.ConversationKey) {
		writeAgentProfileError(w, http.StatusBadRequest, "only a side chat (tab) can be closed")
		return
	}
	if api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "agent profiles are unavailable")
		return
	}
	userID := productWorkspaceUserID(r.Context())
	profile, err := api.agentProfiles.Resolve(strings.TrimSpace(mux.Vars(r)["id"]), 0, userID)
	if err != nil || !userAllowedProduct(GetUserFromContext(r.Context()), profile.Product) || !canUseCapLayerProfile(r.Context(), profile.ID) {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	binding, _, err := resolveConversationBindingForUser(r.Context(), userID, profile, input.ConversationKey)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := crewTabWriteError(r.Context(), userID, profile.ID, binding.ConversationKey, binding.WorkspacePath); err != nil {
		externalError(w, 403, "tab_owner", err.Error())
		return
	}
	if binding.ProjectMainChatKey != "" && binding.ConversationKey == binding.ProjectMainChatKey {
		writeAgentProfileError(w, http.StatusBadRequest, "The main agent cannot be closed.")
		return
	}
	store := defaultProductConversationRegistryStore()
	current, hasCurrent, _, err := store.history(r.Context(), userID, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A stopped pane may remain listed until the watchdog confirms (PLAT-815),
	// and a finished chat retains an idle coding CLI. Release both here for app
	// and MCP callers (PLAT-819); refuse a chat with a turn in flight.
	if hasCurrent && api.sessionStillWorkingAfterIdleRelease(current.SessionID) {
		writeAgentProfileError(w, http.StatusConflict, "This agent is still working. Stop it before closing.")
		return
	}
	if _, err := store.removeSlot(r.Context(), userID, profile, binding); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "close side chat: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
