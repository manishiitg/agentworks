package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// MCP connections at run time in a Code (docs/design/personal_mcp_attach.md):
// a Code is a place like a Crew, so its connections are place connections
// (attachedMCPServersForRoot) that reach the agent as runtime overrides
// carrying their whole config under an internal name u<id>__<name>. Every
// bridge call from a Code session is decided by resolveCodeMCPServer, which
// never falls back to the platform catalog by name.

// mergeServerLists appends extra names, dropping the "no servers" marker.
func mergeServerLists(selected, extra []string) []string {
	if len(extra) == 0 {
		return selected
	}
	out := make([]string, 0, len(selected)+len(extra))
	for _, name := range selected {
		if name != mcpclient.NoServers {
			out = append(out, name)
		}
	}
	return append(out, extra...)
}

// resolveMCPServer is the bridge's resolver: Code sessions first (decided
// only here, fail closed), then the existing report-run and workshop scopes.
func (api *StreamingAPI) resolveMCPServer(ctx context.Context, sessionID, server, tool string) (*executor.ResolvedMCPServer, error) {
	if authority, builder := ctx.Value(vaultBuilderKey{}).(vaultBuilderAuthority); builder && authority.Session != sessionID {
		return nil, fmt.Errorf("Vault builder scope does not match the requested MCP session")
	}
	if resolved, isCode, err := api.resolveCodeMCPServer(ctx, sessionID, server, tool); isCode {
		return resolved, err
	}
	// A connection attached to the session's own workflow, Relay, Crew or Code belongs to that place:
	// any session of it (a chat of anyone with access, a run, a step, a schedule) resolves it.
	if resolved, handled, err := api.resolvePlaceAttachedMCP(ctx, sessionID, server); handled {
		return resolved, err
	}
	resolved, err := api.resolveWorkshopMCPServer(ctx, sessionID, server, tool)
	if resolved != nil || err != nil {
		return resolved, err
	}
	person := ""
	if api.eventStore != nil {
		person = api.mcpSessionPerson(sessionID)
	}
	if sessionID == "" {
		person = mcpCaller(ctx)
	}
	return api.resolveGovernedMCP(ctx, person, server)
}

// resolveCodeMCPServer decides every MCP call from a Code session. isCode is
// false only for a session that is neither pinned nor marked as Code.
func (api *StreamingAPI) resolveCodeMCPServer(ctx context.Context, sessionID, server, tool string) (*executor.ResolvedMCPServer, bool, error) {
	pin, pinned, err := codeSessionPinFor(sessionID)
	if err != nil {
		return nil, true, fmt.Errorf("MCP scope unavailable for this Code chat")
	}
	if !pinned {
		// Marked as Code in memory but never pinned: refuse rather than
		// fall through to the platform catalog.
		if common.CodeSessionRoot(sessionID) != "" {
			return nil, true, fmt.Errorf("MCP scope unavailable for this Code chat")
		}
		return nil, false, nil
	}
	server = strings.TrimSpace(server)
	// One of this Code's own connections, by its internal name or by its
	// plain name (what the person sees); found is false when it is not one.
	place := func(name string) (*executor.ResolvedMCPServer, bool) {
		names, overrides := attachedMCPServersForRoot(context.WithValue(ctx, common.UserIDKey, pin.Person), pin.CodeRoot)
		for _, internal := range names {
			plain := placeMCPPlainName(internal)
			if internal != name && !strings.EqualFold(plain, name) {
				continue
			}
			if override, ok := overrides[internal]; ok && override.Server != nil {
				return &executor.ResolvedMCPServer{Name: internal, Config: *override.Server, ConnectionSessionID: internal}, true
			}
		}
		return nil, false
	}
	if isPlaceMCPInternalName(server) {
		if resolved, found := place(server); found {
			return resolved, true, nil
		}
		// Anyone else's connection, or one that is no longer here.
		return nil, true, fmt.Errorf("MCP server %q is not available in this chat", server)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, pin.CodeRoot)
	if err != nil || !found {
		return nil, true, fmt.Errorf("MCP scope unavailable for this Code chat")
	}
	// The Code's own connection under its plain name, unless a global server
	// the Code selected has that name (then the plain name is the global one).
	if !serverListHasName(runtimeMCPServers(manifest.Capabilities.SelectedServers), server) {
		if resolved, found := place(server); found {
			return resolved, true, nil
		}
	}
	// A global server the Code selected.
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil, true, fmt.Errorf("load current MCP configuration: %w", err)
	}
	resolved, err := api.resolveScopedGovernedMCP(ctx, catalog, runtimeMCPServers(manifest.Capabilities.SelectedServers), manifest.Capabilities.SelectedTools, pin.Person, server, tool)
	return resolved, true, err
}

// placeMCPPlainName is the name a connection has without its store prefix
// (u<32 hex>__<name> -> <name>).
func placeMCPPlainName(internal string) string {
	if isPlaceMCPInternalName(internal) {
		return internal[35:]
	}
	return internal
}

// isPlaceMCPInternalName reports whether name has the personal-server
// shape (u<32 hex>__<name>), whoever it belongs to.
func isPlaceMCPInternalName(name string) bool {
	if len(name) < 36 || name[0] != 'u' || name[33:35] != "__" {
		return false
	}
	for _, c := range name[1:33] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// serverListHasName reports whether names holds name, ignoring case.
func serverListHasName(names []string, name string) bool {
	for _, candidate := range names {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// placeRootForSession is the workflow, Relay, Crew or Code a session works in, taken only from data the server
// set when it started the session (never from anything the client sent); "" when it has none.
func placeRootForSession(sessionID string) string {
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil {
		return ""
	}
	for _, candidate := range []string{cfg.WorkflowPath, cfg.WorkingDir} {
		if root := placeRootOf(candidate); root != "" {
			return root
		}
	}
	return ""
}

// placeAttachedMatch finds, among the connections attached to the session's own place, the one named server
// (its internal name or its plain name). found is false when the place has no such connection, so the caller
// falls through to the platform and Vault servers; denied is true when it has one the session may not use.
func (api *StreamingAPI) placeAttachedMatch(ctx context.Context, sessionID, server string) (internal string, cfg mcpclient.MCPServerConfig, found, denied bool, err error) {
	server = strings.TrimSpace(server)
	root := placeRootForSession(sessionID)
	if root == "" || server == "" {
		return "", mcpclient.MCPServerConfig{}, false, false, nil
	}
	names, overrides := placeAttachedConfigs(root)
	var matches []string
	for _, name := range names {
		if name == server || strings.EqualFold(placeMCPPlainName(name), server) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return "", mcpclient.MCPServerConfig{}, false, false, nil
	}
	if len(matches) > 1 {
		return "", mcpclient.MCPServerConfig{}, true, true, fmt.Errorf("more than one connection of this place is named %q; use the exact name", server)
	}
	person := ""
	if api.eventStore != nil {
		person = api.mcpSessionPerson(sessionID)
	}
	if !placeMCPUsableBy(ctx, person, root) {
		return "", mcpclient.MCPServerConfig{}, true, true, fmt.Errorf("you do not have access to this place's connections")
	}
	override := overrides[matches[0]]
	if override.Server == nil {
		return "", mcpclient.MCPServerConfig{}, true, true, errPlaceMCPUnavailable
	}
	return matches[0], *override.Server, true, false, nil
}

// resolvePlaceAttachedMCP resolves a call against the session's own place (see placeAttachedMatch).
func (api *StreamingAPI) resolvePlaceAttachedMCP(ctx context.Context, sessionID, server string) (*executor.ResolvedMCPServer, bool, error) {
	internal, cfg, found, denied, err := api.placeAttachedMatch(ctx, sessionID, server)
	if !found {
		return nil, false, nil
	}
	if denied || err != nil {
		return nil, true, err
	}
	return &executor.ResolvedMCPServer{Name: internal, Config: cfg, ConnectionSessionID: internal}, true, nil
}
