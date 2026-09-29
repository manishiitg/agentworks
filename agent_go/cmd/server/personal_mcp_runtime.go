package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// Personal MCP servers at run time (docs/design/code_private_mcp.md).
//
// A Code chat's MCP set is the Code's global selection (platform servers, as
// in a Crew) plus the pinned person's own servers switched on for this Code.
// Personal servers reach the agent as runtime overrides carrying their whole
// config under a per-person internal name; every bridge call from a Code
// session is decided by resolveCodeMCPServer, which never falls back to the
// platform catalog by name.

// personalMCPServersForTurn returns the internal names and overrides for the
// person's servers switched on in codeRoot. The internal name u<id>__<name>
// is the override, connection-pool and tool-cache key: turn-start
// connections are pooled per server name under the shared "global" session,
// so a plain name would let two people's "supabase" share one client and
// login. Only what the model reads shows the plain name (the bridge maps it
// back, resolveCodeMCPServer). A server that cannot be built (a missing
// personal secret, say) is skipped and logged, not fatal.
func personalMCPServersForTurn(person, codeRoot string) ([]string, mcpclient.RuntimeOverrides) {
	enabled, err := personalMCPEnabled(person, codeRoot)
	if err != nil || len(enabled) == 0 {
		if err != nil {
			log.Printf("[PERSONAL_MCP] enabled servers for %s: %v", codeRoot, err)
		}
		return nil, nil
	}
	names := make([]string, 0, len(enabled))
	overrides := mcpclient.RuntimeOverrides{}
	for _, name := range enabled {
		internal, cfg, err := personalMCPServerConfig(person, name)
		if err != nil {
			log.Printf("[PERSONAL_MCP] skipping %s for this turn: %v", name, err)
			continue
		}
		config := cfg
		names = append(names, internal)
		overrides[internal] = mcpclient.RuntimeConfigOverride{Server: &config}
	}
	return names, overrides
}

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
	if resolved, isCode, err := api.resolveCodeMCPServer(ctx, sessionID, server, tool); isCode {
		return resolved, err
	}
	return api.resolveWorkshopMCPServer(ctx, sessionID, server, tool)
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
	// The person's own server switched on for this Code; ok is false when
	// plain is not one of them.
	personal := func(plain string) (*executor.ResolvedMCPServer, bool, error) {
		enabled, err := personalMCPEnabled(pin.Person, pin.CodeRoot)
		if err != nil {
			return nil, true, fmt.Errorf("MCP scope unavailable for this Code chat")
		}
		for _, name := range enabled {
			if name != plain {
				continue
			}
			internal, cfg, err := personalMCPServerConfig(pin.Person, plain)
			if err != nil {
				return nil, true, err
			}
			return &executor.ResolvedMCPServer{Name: internal, Config: cfg, ConnectionSessionID: "global"}, true, nil
		}
		return nil, false, nil
	}
	if plain, ok := personalMCPPlainName(pin.Person, server); ok {
		resolved, found, err := personal(plain)
		if !found && err == nil {
			err = fmt.Errorf("MCP server %q is not switched on in this Code", plain)
		}
		return resolved, true, err
	}
	// Anyone else's personal server is never reachable.
	if isPersonalMCPInternalName(server) {
		return nil, true, fmt.Errorf("MCP server %q is not available in this chat", server)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, pin.CodeRoot)
	if err != nil || !found {
		return nil, true, fmt.Errorf("MCP scope unavailable for this Code chat")
	}
	// The person's own server under its plain name, unless a global server
	// the Code selected has that name (then the plain name is the global one).
	if !serverListHasName(runtimeMCPServers(manifest.Capabilities.SelectedServers), server) {
		if resolved, found, err := personal(server); found {
			return resolved, true, err
		}
	}
	// A global server the Code selected.
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil, true, fmt.Errorf("load current MCP configuration: %w", err)
	}
	resolved, err := resolveSelectedMCPServer(catalog, runtimeMCPServers(manifest.Capabilities.SelectedServers), manifest.Capabilities.SelectedTools, pin.Person, server, tool)
	return resolved, true, err
}

// isPersonalMCPInternalName reports whether name has the personal-server
// shape (u<32 hex>__<name>), whoever it belongs to.
func isPersonalMCPInternalName(name string) bool {
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

// withPersonalSecrets adds the person's own secrets to a Code chat's
// secrets; a personal secret wins over a Code secret of the same name.
func withPersonalSecrets(person string, secrets []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}) []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
} {
	names, err := listPersonalSecretNames(person)
	if err != nil {
		log.Printf("[PERSONAL_MCP] personal secrets unavailable for this turn: %v", err)
		return secrets
	}
	for _, name := range names {
		value, err := personalSecretValue(person, name)
		if err != nil {
			log.Printf("[PERSONAL_MCP] skipping personal secret %s: %v", name, err)
			continue
		}
		replaced := false
		for i := range secrets {
			if secrets[i].Name == name {
				secrets[i].Value, replaced = value, true
			}
		}
		if !replaced {
			secrets = append(secrets, struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{Name: name, Value: value})
		}
	}
	return secrets
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
