package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type toolImplementationKind string

const (
	toolImplementationMCP     toolImplementationKind = "mcp"
	toolImplementationDirect  toolImplementationKind = "direct"
	toolImplementationVirtual toolImplementationKind = "virtual"
)

// registeredTool is the single internal record used for discovery, schema
// resolution, authorization, and execution routing. Source and DisplayGroup
// are internal metadata; Name is the only model-facing address.
type registeredTool struct {
	Name         string
	Definition   llmtypes.Tool
	Kind         toolImplementationKind
	Source       string
	DisplayGroup string
	Executor     ToolExecutor
	Timeout      time.Duration
	// MCPToolName is the name the MCP server registered (Kind mcp only). Name
	// is the model-facing <alias>__<tool> (PLAT-519); bridge routes and
	// generated specs use Source + MCPToolName.
	MCPToolName string
	// MCPServerAlias is the alias a host declared for Source, shown to agents
	// in place of it (Kind mcp only).
	MCPServerAlias string
}

// realName is the name the tool's implementation knows it by.
func (t registeredTool) realName() string {
	if t.Kind == toolImplementationMCP && t.MCPToolName != "" {
		return t.MCPToolName
	}
	return t.Name
}

type canonicalToolRegistry struct {
	mu     sync.RWMutex
	byName map[string]registeredTool
}

func newCanonicalToolRegistry() *canonicalToolRegistry {
	return &canonicalToolRegistry{byName: make(map[string]registeredTool)}
}

func (r *canonicalToolRegistry) register(tool registeredTool) error {
	if tool.Name == "" {
		return fmt.Errorf("cannot register tool with empty name")
	}
	if tool.Definition.Function == nil {
		return fmt.Errorf("tool %q has no function definition", tool.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, exists := r.byName[tool.Name]; exists {
		if existing.Kind != tool.Kind || existing.Source != tool.Source {
			return fmt.Errorf(
				"tool name %q is owned by %s %q and cannot also be owned by %s %q",
				tool.Name,
				existing.Kind,
				existing.Source,
				tool.Kind,
				tool.Source,
			)
		}
		if tool.Kind == toolImplementationDirect && existing.DisplayGroup != tool.DisplayGroup {
			return fmt.Errorf(
				"tool name %q is already registered in category %q and cannot be registered in category %q",
				tool.Name,
				existing.DisplayGroup,
				tool.DisplayGroup,
			)
		}
	}
	r.byName[tool.Name] = tool
	return nil
}

// removeMCP drops an MCP tool's record (never a direct tool's), so a direct tool of the same name can take its place.
// With every MCP tool prefixed (PLAT-519) this only fires for a platform tool named like a prefixed MCP tool.
func (r *canonicalToolRegistry) removeMCP(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.byName[name]
	if !exists || existing.Kind != toolImplementationMCP {
		return false
	}
	delete(r.byName, name)
	return true
}

func (r *canonicalToolRegistry) lookup(name string) (registeredTool, bool) {
	if r == nil {
		return registeredTool{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.byName[name]
	return tool, ok
}

// lookupMCPByRealName finds an MCP tool by the name its server registered,
// optionally narrowed to one server (hyphens and underscores alike). It
// reports false when no tool or more than one matches.
func (r *canonicalToolRegistry) lookupMCPByRealName(realName, server string) (registeredTool, bool) {
	if r == nil || realName == "" {
		return registeredTool{}, false
	}
	server = normalizeServerKey(server)
	key := mcpclient.NormalizeToolName(realName)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var exact, loose []registeredTool
	for _, tool := range r.byName {
		if tool.Kind != toolImplementationMCP {
			continue
		}
		if server != "" && normalizeServerKey(tool.Source) != server && normalizeServerKey(tool.MCPServerAlias) != server {
			continue
		}
		if tool.realName() == realName {
			exact = append(exact, tool)
		} else if mcpclient.NormalizeToolName(tool.realName()) == key || mcpclient.NormalizeToolName(tool.Name) == key {
			loose = append(loose, tool)
		}
	}
	if len(exact) > 0 {
		loose = exact
	}
	if len(loose) != 1 {
		return registeredTool{}, false
	}
	return loose[0], true
}

// agentServerName is the server name an agent sees for this MCP tool.
func (t registeredTool) agentServerName() string {
	if t.MCPServerAlias != "" {
		return t.MCPServerAlias
	}
	return strings.ReplaceAll(t.Source, "-", "_")
}

func (r *canonicalToolRegistry) snapshot() []registeredTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]registeredTool, 0, len(r.byName))
	for _, tool := range r.byName {
		result = append(result, tool)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *canonicalToolRegistry) directSnapshot() []registeredTool {
	all := r.snapshot()
	direct := make([]registeredTool, 0, len(all))
	for _, tool := range all {
		if tool.Kind == toolImplementationDirect {
			direct = append(direct, tool)
		}
	}
	return direct
}

func (a *Agent) initializeCanonicalToolRegistry(mcpTools []llmtypes.Tool, toolToServer map[string]string) error {
	registry := newCanonicalToolRegistry()
	for _, definition := range mcpTools {
		if definition.Function == nil {
			continue
		}
		name := definition.Function.Name
		server := toolToServer[name]
		if server == "" {
			return fmt.Errorf("MCP tool %q has no owning server", name)
		}
		if err := registry.register(registeredTool{
			Name:           name,
			Definition:     definition,
			Kind:           toolImplementationMCP,
			Source:         server,
			MCPToolName:    a.realMCPToolName(name, server),
			MCPServerAlias: a.mcpServerAliases[server],
		}); err != nil {
			return err
		}
	}
	a.canonicalRegistryMu.Lock()
	a.toolRegistry = registry
	a.canonicalRegistryMu.Unlock()
	return nil
}

func (a *Agent) canonicalRegistry() (*canonicalToolRegistry, error) {
	a.canonicalRegistryMu.Lock()
	defer a.canonicalRegistryMu.Unlock()
	if a.toolRegistry != nil {
		return a.toolRegistry, nil
	}

	registry := newCanonicalToolRegistry()
	definitions := append([]llmtypes.Tool(nil), a.allMCPToolDefs...)
	definitions = append(definitions, a.tools...)
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.Function == nil {
			continue
		}
		name := definition.Function.Name
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		server := a.toolToServer[name]
		if server == "" || server == "custom" {
			continue
		}
		if err := registry.register(registeredTool{Name: name, Definition: definition, Kind: toolImplementationMCP, Source: server, MCPToolName: a.realMCPToolName(name, server), MCPServerAlias: a.mcpServerAliases[server]}); err != nil {
			return nil, err
		}
	}
	a.toolRegistry = registry
	return registry, nil
}

func (a *Agent) lookupDirectTool(name string) (registeredTool, bool) {
	registry, err := a.canonicalRegistry()
	if err != nil {
		return registeredTool{}, false
	}
	tool, ok := registry.lookup(name)
	if !ok || tool.Kind != toolImplementationDirect {
		return registeredTool{}, false
	}
	return tool, true
}

func (a *Agent) directToolSnapshot() []registeredTool {
	registry, err := a.canonicalRegistry()
	if err != nil {
		return nil
	}
	return registry.directSnapshot()
}

func (a *Agent) directToolExecutors() map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	for _, tool := range a.directToolSnapshot() {
		executor := tool.Executor
		if a.directToolExecutionEvents {
			executor = a.observedDirectToolExecutor(tool.Name, executor)
		}
		executors[tool.Name] = executor
	}
	return executors
}

// observedDirectToolExecutor is the execution-side half of the tool-call
// contract. It runs inside the bridge registry, so it observes a call only
// when the host handler truly runs and always has the actual result/error.
func (a *Agent) observedDirectToolExecutor(name string, executor ToolExecutor) ToolExecutor {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		// A wrapped Run already receives the CLI transcript's canonical tool
		// intent/result pair. Bridge receipts are the missing source only for a
		// retained turn delivered directly to the provider between Runs. Emitting
		// both sources during an active Run would render every ordinary tool twice.
		if a.isTurnInFlight() {
			return executor(ctx, args)
		}
		ctx = a.attachActiveTurnLifecycle(ctx)
		arguments, err := json.Marshal(args)
		if err != nil {
			arguments = []byte(`{"_serialization_error":"could not encode tool arguments"}`)
		}
		callID := "direct-" + uuid.NewString()
		start := events.NewToolCallStartEvent(0, name, events.ToolParams{Arguments: string(arguments)}, "direct_execution", "")
		start.ToolCallID = callID
		a.emitTypedEvent(ctx, start)

		started := time.Now()
		result, callErr := executor(ctx, args)
		duration := time.Since(started)
		if callErr != nil {
			failure := events.NewToolCallErrorEvent(0, name, callErr.Error(), "direct_execution", duration)
			failure.ToolCallID = callID
			a.emitTypedEvent(ctx, failure)
			return result, callErr
		}
		end := events.NewToolCallEndEvent(0, name, result, "direct_execution", duration, "")
		end.ToolCallID = callID
		a.emitTypedEvent(ctx, end)
		return result, nil
	}
}

// attachActiveTurnLifecycle carries the session's currently active turn
// identity into ctx when ctx does not already have one. observedDirectToolExecutor
// runs with a bare HTTP-request-derived context (the bridge handler passes
// r.Context() straight through, all the way from
// executor/handlers.go's custom-tool handler) -- it never carries the
// canonicalTurnLifecycle that Session.Send attaches to the context IT builds
// for delivering the message, because that context and this one are
// unrelated: the tool call arrives over HTTP from the coding CLI's own
// subprocess, on a completely different call stack. Without this,
// emitTypedEvent's canonicalTurnLifecycleFromContext finds nil and never
// stamps turn_id at all, so a retained turn's tool_call_start/tool_call_end
// events (the only ones actually emitted for a retained delivery -- see the
// isTurnInFlight branch above) carry no turn_id whatsoever, not merely the
// wrong one (PLAT-180).
func (a *Agent) attachActiveTurnLifecycle(ctx context.Context) context.Context {
	if canonicalTurnLifecycleFromContext(ctx) != nil {
		return ctx
	}
	session, ok := LookupSession(a.sessionID)
	if !ok || session == nil {
		return ctx
	}
	lifecycle := session.currentTurnLifecycle()
	if lifecycle == nil {
		return ctx
	}
	return withCanonicalTurnLifecycle(ctx, lifecycle)
}
