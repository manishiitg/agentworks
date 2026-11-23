package mcpagent

// BridgeTokenForSession, when set by the consuming application, returns the
// bearer token a coding-agent bridge running as sessionID uses to call the
// executor. The consumer verifies it against the session each request names,
// so one agent cannot call tools as another session. Nil keeps the single
// process-wide APIToken / MCP_API_TOKEN.
var BridgeTokenForSession func(sessionID string) string

// ProtectManagedProjectionWrites is installed by hosts that provide a
// workspace folder guard. It runs for every coding-agent bridge, including
// delegated and workflow sessions.
var ProtectManagedProjectionWrites func(sessionID, workingDir string)
