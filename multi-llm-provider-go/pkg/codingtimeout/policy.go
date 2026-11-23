package codingtimeout

import (
	"os"
	"strings"
	"time"
)

const (
	// EnvLongRunningMCPToolTimeout controls the final client/bridge safety
	// backstop for a silent MCP call. It is not a workflow or tmux turn
	// deadline; active workflow callers should own those deadlines.
	EnvLongRunningMCPToolTimeout = "CODING_AGENT_MCP_TOOL_TIMEOUT"

	DefaultLongRunningMCPToolTimeout = 90 * time.Minute
	DefaultBridgeHTTPTimeout         = 5 * time.Minute
	DefaultPersistentSessionIdle     = 3 * time.Hour
)

type ProviderPolicy struct {
	Provider              string
	MCPClientControl      string
	MCPClientTimeout      time.Duration
	MCPClientConfigurable bool
	TurnTimeout           time.Duration
	PersistentIdle        time.Duration
	Note                  string
}

// LongRunningMCPToolTimeout returns the shared silent-call backstop. Invalid,
// zero, and negative overrides are ignored because disabling this final guard
// would allow a permanently stuck bridge request to live forever.
func LongRunningMCPToolTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(EnvLongRunningMCPToolTimeout)); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return DefaultLongRunningMCPToolTimeout
}

// ActiveProviderPolicies documents the effective MCP-client timeout layer for
// every supported tmux coding provider. Cursor and Pi intentionally report no
// provider control: neither currently documents a supported MCP-call timeout
// setting, so Runloop relies on request cancellation plus the bridge backstop.
func ActiveProviderPolicies() []ProviderPolicy {
	timeout := LongRunningMCPToolTimeout()
	return []ProviderPolicy{
		{
			Provider:              "claude-code",
			MCPClientControl:      "CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT",
			MCPClientTimeout:      timeout,
			MCPClientConfigurable: true,
			PersistentIdle:        DefaultPersistentSessionIdle,
			Note:                  "Claude Code silent MCP-call watchdog (milliseconds)",
		},
		{
			Provider:              "codex-cli",
			MCPClientControl:      "mcp_servers.api-bridge.tool_timeout_sec",
			MCPClientTimeout:      timeout,
			MCPClientConfigurable: true,
			PersistentIdle:        DefaultPersistentSessionIdle,
			Note:                  "Codex api-bridge MCP tool timeout (seconds)",
		},
		{
			Provider:              "cursor-cli",
			MCPClientControl:      "unsupported",
			MCPClientConfigurable: false,
			PersistentIdle:        DefaultPersistentSessionIdle,
			Note:                  "No documented Cursor CLI MCP-call timeout control; request cancellation and bridge timeout apply",
		},
		{
			Provider:              "pi-cli",
			MCPClientControl:      "unsupported",
			MCPClientConfigurable: false,
			PersistentIdle:        DefaultPersistentSessionIdle,
			Note:                  "No documented Pi MCP-adapter call timeout control; request cancellation and bridge timeout apply",
		},
	}
}

func PolicyForProvider(provider string) (ProviderPolicy, bool) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	for _, policy := range ActiveProviderPolicies() {
		if policy.Provider == provider {
			return policy, true
		}
	}
	return ProviderPolicy{}, false
}
