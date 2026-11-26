package llmtypes

import (
	"encoding/json"
	"strings"
)

const (
	CodingProviderTransportTmux       = "tmux"
	CodingProviderTransportStructured = "structured"
	CodingProviderTransportAPI        = "api"
	CodingProviderTransportAppServer  = "app-server"
	CodingProviderTransportRPC        = "rpc"
	CodingProviderTransportACP        = "acp"
	CodingProviderTransportSDK        = "sdk"
	// CodingProviderTransportMSP is Muse's session protocol (`muse serve`).
	CodingProviderTransportMSP = "msp"

	CodingProviderSessionStatusIdle   = "idle"
	CodingProviderSessionStatusActive = "active"
	CodingProviderSessionStatusLost   = "lost"
	CodingProviderSessionStatusClosed = "closed"

	CodingProviderSessionHandleAdditionalKey    = "coding_provider_session_handle"
	CodingProviderLaunchOnlyMetadataKey         = "coding_provider_launch_only"
	CodingProviderLaunchSystemPromptMetadataKey = "coding_provider_launch_system_prompt"

	// AttachedSkillsMetadataKey threads the agent's attached skills into
	// the adapter via opts.Metadata.Custom. CLI adapters call
	// AttachedSkillsFromOptions(opts) at session launch to retrieve the
	// list and project SKILL.md folders to their native skill directory
	// (.claude/skills/, .agents/skills/). API adapters can ignore the
	// key — mcpagent's ensureSystemPrompt already surfaces the listing
	// in the outgoing system prompt for them.
	AttachedSkillsMetadataKey = "attached_skills"
	// CodingAgentSecretEnvironmentMetadataKey carries a per-turn, scoped secret
	// environment to native coding-agent processes. It is never prompt content.
	CodingAgentSecretEnvironmentMetadataKey = "coding_agent_secret_environment"
)

// CodingProviderSessionHandle is the provider-owned continuation state for a
// coding-agent session. Callers should persist and pass this as opaque data; the
// provider adapter owns interpreting native session IDs, tmux names, and project
// directories.
type CodingProviderSessionHandle struct {
	// Native storage format, set by the host after validating a native import.
	NativeFormat    string `json:"native_format,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Transport       string `json:"transport,omitempty"`
	NativeSessionID string `json:"native_session_id,omitempty"`
	TmuxSession     string `json:"tmux_session,omitempty"`
	WorkingDir      string `json:"working_dir,omitempty"`
	RuntimeDir      string `json:"runtime_dir,omitempty"`
	ProjectDirID    string `json:"project_dir_id,omitempty"`
	Model           string `json:"model,omitempty"`
	Status          string `json:"status,omitempty"`
}

func (h CodingProviderSessionHandle) Empty() bool {
	return strings.TrimSpace(h.Provider) == "" &&
		strings.TrimSpace(h.Transport) == "" &&
		strings.TrimSpace(h.NativeSessionID) == "" &&
		strings.TrimSpace(h.TmuxSession) == "" &&
		strings.TrimSpace(h.WorkingDir) == "" &&
		strings.TrimSpace(h.RuntimeDir) == "" &&
		strings.TrimSpace(h.ProjectDirID) == "" &&
		strings.TrimSpace(h.Model) == "" &&
		strings.TrimSpace(h.Status) == ""
}

// AttachCodingProviderSessionHandle stores the handle in both the typed
// GenerationInfo field and Additional for compatibility with older callers that
// forward Additional into event metadata.
func AttachCodingProviderSessionHandle(genInfo *GenerationInfo, handle CodingProviderSessionHandle) {
	if genInfo == nil || handle.Empty() {
		return
	}
	genInfo.CodingProviderSessionHandle = &handle
	if genInfo.Additional == nil {
		genInfo.Additional = map[string]interface{}{}
	}
	genInfo.Additional[CodingProviderSessionHandleAdditionalKey] = handle
}

func ExtractCodingProviderSessionHandle(genInfo *GenerationInfo) (CodingProviderSessionHandle, bool) {
	if genInfo == nil {
		return CodingProviderSessionHandle{}, false
	}
	if genInfo.CodingProviderSessionHandle != nil && !genInfo.CodingProviderSessionHandle.Empty() {
		return *genInfo.CodingProviderSessionHandle, true
	}
	if genInfo.Additional == nil {
		return CodingProviderSessionHandle{}, false
	}
	handle, ok := codingProviderSessionHandleFromValue(genInfo.Additional[CodingProviderSessionHandleAdditionalKey])
	if ok && !handle.Empty() {
		return handle, true
	}
	return CodingProviderSessionHandle{}, false
}

func ExtractCodingProviderSessionHandleFromResponse(resp *ContentResponse) (CodingProviderSessionHandle, bool) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return CodingProviderSessionHandle{}, false
	}
	return ExtractCodingProviderSessionHandle(resp.Choices[0].GenerationInfo)
}

func codingProviderSessionHandleFromValue(value interface{}) (CodingProviderSessionHandle, bool) {
	switch typed := value.(type) {
	case CodingProviderSessionHandle:
		return typed, !typed.Empty()
	case *CodingProviderSessionHandle:
		if typed == nil {
			return CodingProviderSessionHandle{}, false
		}
		return *typed, !typed.Empty()
	case map[string]interface{}:
		var handle CodingProviderSessionHandle
		data, err := json.Marshal(typed)
		if err != nil {
			return CodingProviderSessionHandle{}, false
		}
		if err := json.Unmarshal(data, &handle); err != nil {
			return CodingProviderSessionHandle{}, false
		}
		return handle, !handle.Empty()
	case string:
		if strings.TrimSpace(typed) == "" {
			return CodingProviderSessionHandle{}, false
		}
		var handle CodingProviderSessionHandle
		if err := json.Unmarshal([]byte(typed), &handle); err != nil {
			return CodingProviderSessionHandle{}, false
		}
		return handle, !handle.Empty()
	default:
		return CodingProviderSessionHandle{}, false
	}
}
