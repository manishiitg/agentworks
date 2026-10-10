package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/presentations"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/mcpclient"
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
	CodeChatMode        string               `json:"code_chat_mode,omitempty"`
	CodeChatAttachments []string             `json:"code_chat_attachments,omitempty"`
	CodeLocalFiles      *codeLocalFileTarget `json:"code_local_files,omitempty"`
	ConnectionID        string               `json:"connection_id,omitempty"`
	Message             string               `json:"message"`
	ConversationKey     string               `json:"conversation_key,omitempty"`
	Engine              string               `json:"engine,omitempty"`
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

// AgentProfilePresentationDeleteRequest is deliberately narrow: the browser
// can request deletion of one presented Video Studio asset, but cannot supply
// an arbitrary workspace root or a SQL statement. The server resolves the
// project from its durable manifest before touching either the files or the
// presentation row.
type AgentProfilePresentationDeleteRequest struct {
	ConversationKey string `json:"conversation_key"`
	Kind            string `json:"kind"`
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
	if input.CodeChatMode != "" && profile.ID != "code" {
		return QueryRequest{}, fmt.Errorf("chat mode is available only in Code")
	}
	if profile.ID == "code" {
		mode, err := resolveCodeChatMode(input.CodeChatMode, input.CodeLocalFiles)
		if err != nil {
			return QueryRequest{}, err
		}
		input.CodeChatMode = mode
	}
	if input.CodeLocalFiles != nil && (profile.ID != "code" || !input.CodeLocalFiles.valid()) {
		return QueryRequest{}, fmt.Errorf("local file selection requires Code and a valid device and folder alias")
	}
	if input.KnowledgebaseFolderPath != nil && profile.ID != "knowledgebase" {
		return QueryRequest{}, fmt.Errorf("this profile does not accept Brain context")
	}
	if strings.TrimSpace(conversation.SessionID) == "" || strings.TrimSpace(conversation.WorkspacePath) == "" {
		return QueryRequest{}, fmt.Errorf("product conversation has no runtime binding")
	}
	req := QueryRequest{
		CodeLocalFiles:              input.CodeLocalFiles,
		CodeChatMode:                input.CodeChatMode,
		CodeChatAttachments:         input.CodeChatAttachments,
		Query:                       input.Message,
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
	if strings.EqualFold(strings.TrimSpace(option.Provider), "pi-cli") && piServiceModelID(modelID) {
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
	router.HandleFunc("/agent-profiles/validate", validateAgentProfileHandler()).Methods(http.MethodPost, http.MethodOptions)
	router.HandleFunc("/agent-profiles/{id}", getAgentProfileHandler(registry)).Methods(http.MethodGet, http.MethodOptions)
}

func cleanPresentedAssetPath(raw string, kind string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("asset path must be project-relative")
	}
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return "", fmt.Errorf("asset path must stay inside the project")
	}
	extension := strings.ToLower(filepath.Ext(path))
	switch kind {
	case "media.video":
		if extension != ".mp4" && extension != ".mov" && extension != ".webm" && extension != ".mkv" {
			return "", fmt.Errorf("video deletion requires a video file")
		}
	case "media.character":
		if extension != ".png" && extension != ".jpg" && extension != ".jpeg" && extension != ".webp" && extension != ".md" {
			return "", fmt.Errorf("character deletion only permits its reference image and spec")
		}
	default:
		return "", fmt.Errorf("this presentation kind cannot be deleted from the product UI")
	}
	return path, nil
}

// handleAgentProfilePresentationDelete deletes a user-confirmed, generated
// presentation. The browser provides only the presentation ID, kind, and
// paths already rendered from that row; project ownership and the workspace
// root are always determined server-side from the signed-in user's project.
func (api *StreamingAPI) handleAgentProfilePresentationDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	var input AgentProfilePresentationDeleteRequest
	if err := decoder.Decode(&input); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid presentation deletion request: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid presentation deletion request: expected one JSON object")
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
	if profile.ID != "video-studio" {
		writeAgentProfileError(w, http.StatusMethodNotAllowed, "this product does not support deleting presentations")
		return
	}
	presentationID := strings.TrimSpace(mux.Vars(r)["presentationID"])
	if presentationID == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "presentation id is required")
		return
	}
	userID := productWorkspaceUserID(r.Context())
	binding, err := resolveProductConversationBinding(r.Context(), userID, profile, strings.TrimSpace(input.ConversationKey))
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	kind := strings.TrimSpace(input.Kind)
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
	rows, err := client.QueryAuthorizedWorkflowDB(r.Context(), workspace.QueryWorkflowDBParams{
		DBPath: presentations.DatabasePath(binding.WorkspacePath),
		SQL:    "SELECT payload_json FROM ui_presentations WHERE id = ? AND kind = ?",
		Params: []interface{}{presentationID, kind},
	})
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, "load presentation for deletion: "+err.Error())
		return
	}
	if len(rows.Rows) != 1 {
		writeAgentProfileError(w, http.StatusNotFound, "presentation was not found in this project")
		return
	}
	payloadJSON, _ := rows.Rows[0]["payload_json"].(string)
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, "stored presentation payload is invalid")
		return
	}
	rawPaths := []string{}
	switch kind {
	case "media.video":
		rawPaths = append(rawPaths, stringPayloadValue(payload, "path"))
	case "media.character":
		rawPaths = append(rawPaths, stringPayloadValue(payload, "image_path"), stringPayloadValue(payload, "spec_path"))
	default:
		writeAgentProfileError(w, http.StatusBadRequest, "this presentation kind cannot be deleted from the product UI")
		return
	}
	paths := make([]string, 0, len(rawPaths))
	for _, rawPath := range rawPaths {
		path, pathErr := cleanPresentedAssetPath(rawPath, kind)
		if pathErr != nil {
			writeAgentProfileError(w, http.StatusUnprocessableEntity, "stored presentation source is invalid: "+pathErr.Error())
			return
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		if _, err := client.DeleteWorkspaceFile(r.Context(), workspace.DeleteWorkspaceFileParams{Filepath: filepath.ToSlash(filepath.Join(binding.WorkspacePath, path))}); err != nil {
			// A stale presentation is exactly the case where the user most needs
			// this control: its generated file may already have been removed from
			// the Files panel. Still remove the durable card in that case rather
			// than trapping them behind a failed delete button.
			if strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "does not exist") {
				continue
			}
			writeAgentProfileError(w, http.StatusUnprocessableEntity, "delete presented asset: "+err.Error())
			return
		}
	}
	if err := presentations.Delete(r.Context(), client, binding.WorkspacePath, presentationID, kind); err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"deleted": true, "presentation_id": presentationID})
}

func stringPayloadValue(payload map[string]interface{}, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
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
	conversation, err := api.resolveAgentProfileConversation(r, profile, input.ConversationKey)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	input.interactive = true
	query, err := prepareProductConversationTurn(r.Context(), productWorkspaceUserID(r.Context()), profile, input, conversation)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
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
			knowledgebaseHTTPError(w, err)
			return
		}
		if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
			knowledgebaseHTTPError(w, err)
			return
		}
		if _, err = service.Call(r.Context(), knowledgebasePrincipal(r, GetUserFromContext(r.Context())), "list_knowledgebase_folders", map[string]any{"folder_path": folder, "depth": 1}); err != nil {
			knowledgebaseHTTPError(w, err)
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
	// product (for example "video-studio"), while a single-user deployment's
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
	conversation, err := api.resolveAgentProfileConversation(r, profile, input.ConversationKey)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, AgentProfileConversationResponse{
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
	binding, err := resolveProductConversationBinding(r.Context(), userID, profile, input.ConversationKey)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	conversation, err := defaultProductConversationRegistryStore().rotate(r.Context(), userID, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, AgentProfileConversationResponse{
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

func (api *StreamingAPI) resolveAgentProfileConversation(r *http.Request, profile agentprofiles.Profile, requestedKey string) (ProductConversationRecord, error) {
	userID := productWorkspaceUserID(r.Context())
	// Crew Run mode: opening resolves under whichever owner holds the
	// project. Reader bindings carry no manifest path, so the registry
	// below stays reader-scoped and project initialization (which
	// creates folders and databases) is skipped.
	binding, owned, err := resolveConversationBindingForUser(r.Context(), userID, profile, requestedKey)
	if err != nil {
		return ProductConversationRecord{}, err
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
	return record, nil
}

func shouldRebindWorkConversation(
	profile agentprofiles.Profile,
	binding productConversationBinding,
	record ProductConversationRecord,
	preferredSessionID string,
	verifiedForWorkspace bool,
) bool {
	if !isProjectProfileID(profile.ID) || !verifiedForWorkspace {
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
				visible = append(visible, profileWithAvailableProviders(profile))
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
		writeAgentProfileJSON(w, http.StatusOK, profileWithAvailableProviders(profile))
	}
}

// Product provider options remain visible; runtime/auth readiness is reported
// by the provider manifest rather than silently removing an engine.
func profileWithAvailableProviders(profile agentprofiles.Profile) agentprofiles.Profile {
	return profileWithProductDefault(context.Background(), profile)
}

func validateAgentProfileHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
		decoder.DisallowUnknownFields()
		var profile agentprofiles.Profile
		if err := decoder.Decode(&profile); err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "invalid agent profile: "+err.Error())
			return
		}
		// Validation is the user-profile contract. A client cannot claim built-in
		// authority or choose a different owner through this endpoint.
		profile.BuiltIn = false
		profile.OwnerID = GetUserIDFromContext(r.Context())
		if err := agentprofiles.Validate(profile); err != nil {
			writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{
			"valid":   true,
			"profile": profile,
		})
	}
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
	query, err := queryRequestForAgentProfileChat(profile, input, conversation)
	if err != nil {
		return QueryRequest{}, err
	}
	if err := constrainProductChatModel(ctx, &query); err != nil {
		return QueryRequest{}, err
	}
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
	target, found, err := resolveProductResumeTarget(userID, conversation)
	if err != nil {
		return QueryRequest{}, fmt.Errorf("resolve saved conversation: %w", err)
	}
	if found {
		if accountChanged && target != nil {
			// Keep the visible history; start a fresh CLI session on the new account.
			target.Runtime = nil
		}
		query.resolvedResumeTarget = target
	}
	if strings.TrimSpace(query.Provider) != "" {
		_, restart, err := defaultProductConversationRegistryStore().bindRuntimeConfiguration(ctx, userID, profile, conversation.ConversationKey, query.Provider, query.ModelID, query.ReasoningEffort, query.EnabledServers, query.SelectedSkills, query.WorkflowContextPaths, productConversationAccount{ConnectionID: query.ConnectionID, FillsUnrecorded: fillsUnrecorded})
		if err != nil {
			return QueryRequest{}, err
		}
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
	binding, err := resolveProductConversationBinding(r.Context(), userID, profile, input.ConversationKey)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	store := defaultProductConversationRegistryStore()
	current, hasCurrent, _, err := store.history(r.Context(), userID, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A stopped chat's pane is gone but the terminal store keeps listing it live until the watchdog confirms (PLAT-815),
	// and a finished chat keeps its coding CLI alive between turns; the app stops such a chat before it closes the tab,
	// while the MCP tools only call this route (PLAT-819). Both are let go here; a chat with a turn in flight is refused.
	if hasCurrent && api.sessionStillWorkingAfterIdleRelease(current.SessionID) {
		writeAgentProfileError(w, http.StatusConflict, "This chat is still working; stop it before closing the tab")
		return
	}
	if _, err := store.removeSlot(r.Context(), userID, profile, binding); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "close side chat: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
