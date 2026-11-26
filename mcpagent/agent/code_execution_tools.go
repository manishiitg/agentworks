package mcpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache/openapi"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// handleGetAPISpec handles the get_api_spec virtual tool.
// Returns the full OpenAPI spec for the requested tool(s) on a server.
// tool_name is required — accepts a single string or an array of strings.
// Use search_tools for authorized discovery; this tool loads exact schemas.
func (a *Agent) handleGetAPISpec(ctx context.Context, args map[string]interface{}) (string, error) {
	// Optional: an omitted server_name means "resolve by tool name", which is the
	// contract everywhere else. It only narrows a real MCP tool name (as returned
	// by discovery) to one server; authorization never depends on it.
	serverName, _ := args["server_name"].(string)

	// Parse tool_name: accepts string or []string (JSON array)
	var toolNames []string
	if raw, exists := args["tool_name"]; exists && raw != nil {
		switch v := raw.(type) {
		case string:
			// A coding CLI may pass the array already serialized, so tool_name
			// arrives as the literal string `["a","b"]`. Treating that as one
			// tool name produced unknown=[["query_workflow_db", "mutate_workflow_db"]]
			// — a name no registry could ever hold.
			if decoded, ok := decodeJSONStringArray(v); ok {
				toolNames = append(toolNames, decoded...)
			} else if v != "" {
				toolNames = append(toolNames, v)
			}
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					toolNames = append(toolNames, s)
				}
			}
		}
	}
	if len(toolNames) == 0 {
		return "", fmt.Errorf("tool_name parameter is required (string or array of strings)")
	}

	// Tool names are the canonical address. Normalize and sort once so validation,
	// generation, and cache identity all consume the same request.
	sortedNames := make([]string, len(toolNames))
	copy(sortedNames, toolNames)
	sort.Strings(sortedNames)
	cacheKey := "tools:" + strings.Join(sortedNames, ",")
	if serverName != "" {
		// server_name can pick an MCP tool by its real name over a platform tool of that name.
		cacheKey += "@" + normalizeServerKey(serverName)
	}
	if serverName != "" && a.logger != nil {
		a.logger.Debug("get_api_spec: server_name is compatibility-only; resolving by tool name",
			loggerv2.String("server_name", serverName),
			loggerv2.Any("tool_names", sortedNames))
	}

	// Resolve and authorize every name before consulting the schema cache. A
	// cache hit is metadata reuse, never an authorization decision.
	registry, err := a.canonicalRegistry()
	if err != nil {
		return "", err
	}
	customToolsForSpec := make(map[string]openapi.CustomToolForOpenAPI)
	var virtualToolsForSpec []llmtypes.Tool
	mcpToolsByServer := make(map[string][]llmtypes.Tool)
	var unknown, notAllowed []string
	for _, name := range sortedNames {
		if !a.isToolAllowedForContext(ctx, name) || !codeexec.IsSessionToolAllowed(a.sessionID, name) {
			notAllowed = append(notAllowed, name)
			continue
		}
		// Discovery uses the existing authenticated, session-scoped virtual
		// HTTP bridge. Keep its route distinct from custom/MCP tool execution.
		if name == "search_tools" && serverName == "" {
			virtualToolsForSpec = append(virtualToolsForSpec, searchToolsDefinition())
			continue
		}
		registered, ok := registry.lookup(name)
		// MCP tools are named <alias>__<tool> (PLAT-519), but the tool index
		// lists each server's real tool names under its $MCP_MCP/<server>
		// route, so a real name resolves too: alone when only one server has
		// it and no platform tool takes it, or with server_name.
		if !ok || (registered.Kind == toolImplementationDirect && serverName != "") {
			if mcpTool, found := registry.lookupMCPByRealName(name, serverName); found {
				registered, ok = mcpTool, true
			}
		}
		if !ok || registered.Kind == toolImplementationVirtual {
			unknown = append(unknown, name)
			continue
		}
		if registered.Kind == toolImplementationDirect {
			customToolsForSpec[name] = openapi.CustomToolForOpenAPI{
				Definition: registered.Definition,
				Category:   registered.DisplayGroup,
			}
			continue
		}

		srvName := registered.Source
		if !a.serverIsAvailable(srvName) {
			notAllowed = append(notAllowed, name)
			continue
		}

		// The spec documents the bridge route. A tool whose host declared an alias
		// keeps the names search_tools gave; the server maps them back. Any other
		// route carries the real server and tool name.
		definition := registered.Definition
		if registered.MCPServerAlias == "" && definition.Function != nil && registered.realName() != definition.Function.Name {
			function := *definition.Function
			function.Name = registered.realName()
			definition.Function = &function
		}
		specServer := registered.agentServerName()
		mcpToolsByServer[specServer] = append(mcpToolsByServer[specServer], definition)
	}

	if len(unknown) > 0 || len(notAllowed) > 0 {
		return "", a.unavailableToolsError(ctx, registry, serverName, unknown, notAllowed)
	}

	a.openAPISpecCacheMu.RLock()
	if cached, exists := a.openAPISpecCache[cacheKey]; exists {
		a.openAPISpecCacheMu.RUnlock()
		return string(cached), nil
	}
	a.openAPISpecCacheMu.RUnlock()

	baseURL := a.getCodeExecutionAPIBaseURL()
	var sections []string
	if len(customToolsForSpec) > 0 {
		sections = append(sections, openapi.GenerateCustomToolsCompactSpec("custom", customToolsForSpec, baseURL))
	}
	if len(virtualToolsForSpec) > 0 {
		sections = append(sections, openapi.GenerateVirtualToolsCompactSpec(virtualToolsForSpec, baseURL))
	}
	serverNames := make([]string, 0, len(mcpToolsByServer))
	for name := range mcpToolsByServer {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)
	for _, name := range serverNames {
		sections = append(sections, openapi.GenerateCompactSpec(name, mcpToolsByServer[name], baseURL))
	}

	spec := strings.Join(sections, "\n")
	a.cacheSpec(cacheKey, []byte(spec))
	return spec, nil
}

// unavailableToolsError explains why a get_api_spec request could not be
// answered. The distinction it draws is the whole point: a name that resolves
// nowhere reads identically whether the model invented it or whether the server
// that owns it never came up, and the old wording asserted the first
// unconditionally. On 2026-08-01 a run asked google_sheets for five correct
// tool names while that server was failing to connect, was told
// "unknown=[batch_update get_sheet_formulas ...]", concluded its names were
// wrong, and spent the next call guessing new ones. No sequence of names could
// have worked, so the error has to say so.
//
// requestedServer is the model-supplied server_name. It never affects routing —
// only whether this message can name the failing server outright.
func (a *Agent) unavailableToolsError(ctx context.Context, registry *canonicalToolRegistry, requestedServer string, unknown, notAllowed []string) error {
	if len(unknown) == 0 {
		// Permission denials are already attributed correctly; leave them byte-identical.
		return fmt.Errorf("tools_unavailable: unknown=%v not_allowed=%v", unknown, notAllowed)
	}
	missing := a.serversMissingTools(registry)
	blamed := a.blameMissingServer(registry, requestedServer, missing)

	var b strings.Builder
	if len(blamed) > 0 {
		// Same omission the healthy branch below applies: an empty not_allowed
		// means nothing was permission-denied, and printing "not_allowed=[]"
		// invites the reader to wonder which tools were withheld when none
		// were. Leaving it here made the cleanup true in one path and not the
		// other, which is worse than not doing it at all -- a reader can no
		// longer tell whether its absence is meaningful.
		if len(notAllowed) > 0 {
			fmt.Fprintf(&b, "tools_unavailable: server_unavailable=%v requested=%v not_allowed=%v: ", blamed, unknown, notAllowed)
		} else {
			fmt.Fprintf(&b, "tools_unavailable: server_unavailable=%v requested=%v: ", blamed, unknown)
		}
		fmt.Fprintf(&b, "MCP server(s) %v are configured for this agent but currently have zero registered tools, so %s. ",
			blamed, a.missingServerCause(blamed))
		b.WriteString("The requested tool names are most likely correct — retrying with different or guessed tool names will NOT help. ")
		b.WriteString("Treat this as a server outage: report it and continue without those tools.")
		return errors.New(b.String())
	}

	// not_allowed= is omitted when empty: an empty list means nothing was
	// permission-denied, and printing it invited the reader to wonder which
	// tools were withheld when none were.
	if len(notAllowed) > 0 {
		fmt.Fprintf(&b, "tools_unavailable: unknown=%v not_allowed=%v: ", unknown, notAllowed)
	} else {
		fmt.Fprintf(&b, "tools_unavailable: unknown=%v: ", unknown)
	}
	b.WriteString("these names are not registered by any currently connected server. ")

	if len(missing) > 0 {
		// A configured server with zero tools may own the requested name, so the
		// name itself is not necessarily wrong. Deliberately does NOT list the
		// registered tools here: pairing "these are the tools you have" with an
		// outage invites substituting a different tool for the one that is
		// merely unreachable, which is the loop the outage wording exists to
		// prevent.
		fmt.Fprintf(&b, "Note: MCP server(s) %v are configured but currently have zero registered tools (failed to start or connect); "+
			"if a requested tool belongs to one of them, no tool name will work.", missing)
		return errors.New(b.String())
	}

	// Every configured server is healthy, so the name really is wrong. Name the
	// alternatives rather than pointing at the system prompt: telling a model to
	// "use the exact names from your tool index" costs a turn re-reading what it
	// already has, and a near-miss (diff_patch for diff_patch_workspace_file) is
	// one substring away from being resolved right here. When the capability is
	// genuinely absent, seeing the real surface is what stops the guessing.
	available := a.registeredToolNamesForContext(ctx, registry)
	if suggestions := nearestToolNames(unknown, available); len(suggestions) > 0 {
		fmt.Fprintf(&b, "Closest registered name(s): %v. ", suggestions)
	}
	if len(available) > 0 {
		fmt.Fprintf(&b, "Registered tools for this session: %v. ", available)
	} else {
		b.WriteString("This session has no registered tools at all. ")
	}
	b.WriteString("Call one of these exactly, or do the work with your own native tools if the capability you wanted is not here. ")
	b.WriteString("An MCP server that fails to start produces this same symptom, " +
		"so a tool you expected to exist may be missing for reasons unrelated to its name.")
	return errors.New(b.String())
}

// registeredToolNamesForContext lists every registered name the current turn
// can actually call. Error recovery must use the same authorization view as
// execution; otherwise get_api_spec can suggest a tool that the turn policy
// will reject on the very next call.
func (a *Agent) registeredToolNamesForContext(ctx context.Context, registry *canonicalToolRegistry) []string {
	if registry == nil {
		return nil
	}
	tools := registry.snapshot() // already sorted by name
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if a.isToolAllowedForContext(ctx, tool.Name) && codeexec.IsSessionToolAllowed(a.sessionID, tool.Name) &&
			(tool.Kind != toolImplementationMCP || a.serverIsAvailable(tool.Source)) {
			names = append(names, tool.Name)
		}
	}
	return names
}

// nearestToolNames returns registered names that plausibly match a requested
// one, so a truncated or mis-suffixed guess is corrected in this reply instead
// of costing another turn. Substring containment in either direction covers the
// realistic failure — a shortened name (diff_patch) or an over-qualified one
// (mcp__api-bridge__execute_shell_command for execute_shell_command).
func nearestToolNames(unknown, available []string) []string {
	var matches []string
	seen := map[string]struct{}{}
	for _, want := range unknown {
		want = strings.TrimSpace(strings.ToLower(want))
		if len(want) < 3 {
			continue
		}
		for _, name := range available {
			lower := strings.ToLower(name)
			if !strings.Contains(lower, want) && !strings.Contains(want, lower) {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			matches = append(matches, name)
		}
	}
	return matches
}

// blameMissingServer decides whether a server outage can be named as the cause
// rather than merely mentioned. Two cases are certain enough to assert:
// the model named a server that is configured yet holds no tools, or no MCP
// server registered a single tool, in which case no MCP name could resolve.
// Everything else stays a note, because a hallucinated name and a down server
// are genuinely indistinguishable once other servers are serving tools.
func (a *Agent) blameMissingServer(registry *canonicalToolRegistry, requestedServer string, missing []string) []string {
	if len(missing) == 0 {
		return nil
	}
	if requested := normalizeServerKey(requestedServer); requested != "" {
		for _, name := range missing {
			if normalizeServerKey(name) == requested {
				return []string{name}
			}
		}
	}
	if !registryHasMCPTools(registry) {
		return missing
	}
	return nil
}

// missingServerCause distinguishes "never connected" from "connected and
// advertised nothing". Both are server-availability problems, but naming the
// wrong one sends a human debugging in the wrong direction.
func (a *Agent) missingServerCause(missing []string) string {
	for _, name := range missing {
		if !a.hasMCPClient(name) {
			return "they failed to start or failed to connect"
		}
	}
	return "they connected but advertised no tools"
}

// serversMissingTools returns the MCP servers this agent was configured to use
// that own zero tools in the canonical registry. Connection results are not
// usable for this: a server that fails to connect is dropped from both
// a.clients and a.servers, so only the configured request still remembers it.
func (a *Agent) serversMissingTools(registry *canonicalToolRegistry) []string {
	withTools := make(map[string]struct{})
	for _, tool := range registry.snapshot() {
		if tool.Kind != toolImplementationMCP || tool.Source == "" {
			continue
		}
		withTools[normalizeServerKey(tool.Source)] = struct{}{}
	}

	var missing []string
	for _, name := range a.configuredMCPServers() {
		if _, ok := withTools[normalizeServerKey(name)]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// configuredMCPServers lists the MCP servers this agent asked for, as spelled by
// the caller. serverName holds the constructor's comma-separated request and
// selectedServers holds an explicit restriction; neither is rewritten by the
// connection result, which is what makes a failed server still visible here.
// "all" is deliberately not expanded — resolving it means re-reading the config
// from an error path, and asserting an outage from a stale read is worse than
// staying quiet.
func (a *Agent) configuredMCPServers() []string {
	var names []string
	seen := make(map[string]struct{})
	add := func(raw string) {
		name := strings.TrimSpace(raw)
		if name == "" || name == "all" || name == mcpclient.NoServers {
			return
		}
		key := normalizeServerKey(name)
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	for _, name := range strings.Split(a.serverName, ",") {
		add(name)
	}
	for _, name := range a.selectedServers {
		add(name)
	}
	sort.Strings(names)
	return names
}

func (a *Agent) hasMCPClient(serverName string) bool {
	key := normalizeServerKey(serverName)
	for name := range a.clients {
		if normalizeServerKey(name) == key {
			return true
		}
	}
	return false
}

func registryHasMCPTools(registry *canonicalToolRegistry) bool {
	for _, tool := range registry.snapshot() {
		if tool.Kind == toolImplementationMCP {
			return true
		}
	}
	return false
}

// normalizeServerKey collapses the spellings the same server travels under:
// config uses hyphens, HTTP routes and the tool index use underscores.
func normalizeServerKey(serverName string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(serverName), "-", "_"))
}

// serverIsAvailable checks if a server passes the tool filter.
func (a *Agent) serverIsAvailable(serverName string) bool {
	if a.toolFilter.ShouldIncludeServer(serverName) {
		return true
	}
	serverNameWithHyphen := strings.ReplaceAll(serverName, "_", "-")
	return a.toolFilter.ShouldIncludeServer(serverNameWithHyphen)
}

// cacheSpec stores a generated spec in the cache.
func (a *Agent) cacheSpec(key string, specBytes []byte) {
	a.openAPISpecCacheMu.Lock()
	if a.openAPISpecCache == nil {
		a.openAPISpecCache = make(map[string][]byte)
	}
	a.openAPISpecCache[key] = specBytes
	a.openAPISpecCacheMu.Unlock()
}

func (a *Agent) getCodeExecutionAPIBaseURL() string {
	baseURL := a.apiBaseURL
	if baseURL == "" {
		baseURL = os.Getenv("MCP_API_URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}

	if a.sessionID == "" {
		return strings.TrimRight(baseURL, "/")
	}

	sessionPrefix := "/s/" + a.sessionID
	if strings.Contains(baseURL, sessionPrefix) {
		return strings.TrimRight(baseURL, "/")
	}

	return strings.TrimRight(baseURL, "/") + sessionPrefix
}

// getAgentGeneratedDir returns the agent-specific generated directory
// Format: generated/agents/<trace_id>/
// Only creates the directory if code execution mode is enabled
func (a *Agent) getAgentGeneratedDir() string {
	baseDir := a.getGeneratedDir()
	agentDir := filepath.Join(baseDir, "agents", string(a.traceID))

	if a.useCodeExecutionMode {
		if err := os.MkdirAll(agentDir, 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for user-accessible directories
			if a.logger != nil {
				a.logger.Warn("Failed to create agent generated directory", loggerv2.String("agent_dir", agentDir), loggerv2.Error(err))
			}
		}
	}

	return agentDir
}

// BuildSafeEnvironment creates a minimal, safe environment for shell commands.
// Only includes essential variables, excludes all secrets.
// Exported so it can be used by workspace security and other packages.
func BuildSafeEnvironment() []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"USER=agent",
		"SHELL=/bin/sh",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
}

// decodeJSONStringArray reports whether a string is a JSON array of strings and
// returns its non-empty entries. Anything else is a plain tool name.
func decodeJSONStringArray(value string) ([]string, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return nil, false
	}
	var decoded []string
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, false
	}
	names := make([]string, 0, len(decoded))
	for _, name := range decoded {
		if trimmedName := strings.TrimSpace(name); trimmedName != "" {
			names = append(names, trimmedName)
		}
	}
	return names, len(names) > 0
}
