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
	// A session of a workflow, Relay, Crew or Code uses that place's connections and Vault; a connection added
	// to another place, or kept in a person's own store, is never reachable from it.
	if api.placeRootForSession(sessionID) != "" {
		ctx = context.WithValue(ctx, placeScopedKey{}, true)
	}
	if resolved, isCode, err := api.resolveCodeMCPServer(ctx, sessionID, server, tool); isCode {
		if err == nil && resolved != nil {
			if guardErr := api.guardPersonalSlackCall(ctx, sessionID, resolved.Name, tool); guardErr != nil {
				return nil, guardErr
			}
		}
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
	// A Crew's or Goal's Vault MCPs are its owner's for everyone who uses it (scopeAgentMCP); the Vault proxy keeps a lent
	// personal Slack to its send side for anyone else (lentVaultSlackRefusal).
	lender := person
	if root := api.placeRootForSession(sessionID); root != "" && person != "" {
		lender = api.projectSecretsOwner(person, root)
	}
	return api.resolveGovernedMCP(ctx, lender, server)
}

// resolveCodeMCPServer decides every MCP call from a Code session. isCode is
// false only for a session that is neither pinned nor marked as Code.
func (api *StreamingAPI) resolveCodeMCPServer(ctx context.Context, sessionID, server, tool string) (*executor.ResolvedMCPServer, bool, error) {
	pin, pinned, err := codeSessionPinFor(sessionID)
	if err != nil {
		return nil, true, fmt.Errorf("MCP scope unavailable for this Crew chat")
	}
	if !pinned {
		return nil, false, nil
	}
	server = strings.TrimSpace(server)
	// Exact Vault IDs do not require a project manifest or opt-in. Ownership
	// is pinned above, and resolveGovernedMCP checks the live user's inventory.
	if strings.HasPrefix(server, "vault_") {
		resolved, err := api.resolveGovernedMCP(ctx, pin.Person, server)
		return resolved, true, err
	}
	manifest, found, err := ReadWorkflowManifest(ctx, pin.CodeRoot)
	if err != nil || !found {
		return nil, true, fmt.Errorf("MCP scope unavailable for this Crew chat")
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

// placeScopedKey marks a context as belonging to a session of a workflow, Relay, Crew or Code: every MCP resolver
// below it then sees only that place's connections and Vault, never a person's connections from elsewhere.
type placeScopedKey struct{}

func placeScopedContext(ctx context.Context) bool {
	scoped, _ := ctx.Value(placeScopedKey{}).(bool)
	return scoped
}

// placeRootForSession is the workflow, Relay, Crew or Code a session works in, taken only from data the server
// recorded for the session (never from anything the client sent); "" when it has none (a chat outside any place).
func (api *StreamingAPI) placeRootForSession(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if pin, ok, _ := codeSessionPinFor(sessionID); ok {
		if root := placeRootOf(pin.CodeRoot); root != "" {
			return root
		}
	}
	if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
		for _, candidate := range []string{cfg.WorkflowPath, cfg.WorkingDir} {
			if root := placeRootOf(candidate); root != "" {
				return root
			}
		}
	}
	if api != nil {
		api.sessionWorkspaceMu.RLock()
		folder := api.sessionWorkspaceFolders[sessionID]
		api.sessionWorkspaceMu.RUnlock()
		if root := placeRootOf(folder); root != "" {
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
	root := api.mcpPlaceRoot(ctx, sessionID)
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
	person := GetUserIDFromContext(ctx)
	if api.eventStore != nil && sessionID != "" {
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

// Set only after the external transport resolves and authorizes a Crew ID.
type externalCrewMCPPlaceKey struct{}

func (api *StreamingAPI) mcpPlaceRoot(ctx context.Context, sessionID string) string {
	if root, _ := ctx.Value(externalCrewMCPPlaceKey{}).(string); root != "" {
		return root
	}
	return api.placeRootForSession(sessionID)
}
