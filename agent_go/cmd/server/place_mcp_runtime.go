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
	if root := api.placeRootForSession(sessionID); root != "" {
		manifest, found, e := ReadWorkflowManifest(ctx, root)
		if e != nil || !found || manifest == nil {
			return nil, fmt.Errorf("project MCP selection unavailable")
		}
		return api.resolveScopedGovernedMCP(ctx, nil, manifest.Capabilities.RuntimeServers(), manifest.Capabilities.SelectedTools, lender, server, tool)
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
	manifest, found, err := ReadWorkflowManifest(ctx, pin.CodeRoot)
	if err != nil || !found {
		return nil, true, fmt.Errorf("MCP scope unavailable for this Crew chat")
	}
	// A global server the Code selected.
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil, true, fmt.Errorf("load current MCP configuration: %w", err)
	}
	resolved, err := api.resolveScopedGovernedMCP(ctx, catalog, runtimeMCPServers(manifest.Capabilities.RuntimeServers()), manifest.Capabilities.SelectedTools, pin.Person, server, tool)
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

// placeScopedKey marks a context as belonging to a session of a workflow, Relay, Crew or Code: every MCP resolver
// below it then sees only that place's connections and Vault, never a person's connections from elsewhere.
type placeScopedKey struct{}

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

// Set only after the external transport resolves and authorizes a Crew ID.
type externalCrewMCPPlaceKey struct{}

func (api *StreamingAPI) mcpPlaceRoot(ctx context.Context, sessionID string) string {
	if root, _ := ctx.Value(externalCrewMCPPlaceKey{}).(string); root != "" {
		return root
	}
	return api.placeRootForSession(sessionID)
}
