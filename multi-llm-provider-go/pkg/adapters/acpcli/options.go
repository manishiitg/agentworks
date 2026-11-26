package acpcli

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Metadata keys for acp-cli CallOptions.
const (
	MetadataKeyWorkingDir           = "acp_working_dir"
	MetadataKeyInteractiveSessionID = "acp_interactive_session_id"
	MetadataKeyResumeSessionID      = "acp_resume_session_id"
	// MetadataKeyMCPConfig is the chat's {"mcpServers":{name:{command,args,env}}}
	// document; its stdio servers are passed to the agent in session/new.
	MetadataKeyMCPConfig       = "acp_mcp_config"
	MetadataKeyNativeToolsMode = "acp_native_tools_mode"
)

func setMetadata(key, value string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		if opts.Metadata == nil {
			opts.Metadata = &llmtypes.Metadata{}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = map[string]interface{}{}
		}
		opts.Metadata.Custom[key] = value
	}
}

// WithWorkingDir sets the agent's working directory (the session's cwd).
func WithWorkingDir(dir string) llmtypes.CallOption { return setMetadata(MetadataKeyWorkingDir, dir) }

// WithInteractiveSessionID names the chat that owns the agent process; the
// process stays warm between this chat's turns.
func WithInteractiveSessionID(id string) llmtypes.CallOption {
	return setMetadata(MetadataKeyInteractiveSessionID, id)
}

// WithResumeSessionID continues the agent's own session with this id
// (session/load) when the agent supports loading sessions.
func WithResumeSessionID(id string) llmtypes.CallOption {
	return setMetadata(MetadataKeyResumeSessionID, id)
}

// WithMCPConfig passes the chat's MCP servers to the agent.
func WithMCPConfig(configJSON string) llmtypes.CallOption {
	return setMetadata(MetadataKeyMCPConfig, configJSON)
}

// WithNativeToolsMode applies the coding-agent tool setting: "mcp_only" asks
// the person before the agent's own tools run, "full" (confined launches only)
// lets them run without asking.
func WithNativeToolsMode(mode string) llmtypes.CallOption {
	return setMetadata(MetadataKeyNativeToolsMode, mode)
}

func stringMetadata(opts *llmtypes.CallOptions, key string) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	s, _ := opts.Metadata.Custom[key].(string)
	return strings.TrimSpace(s)
}
