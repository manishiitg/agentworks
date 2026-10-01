// Package agentsession is a reusable runtime that gives a coding-agent (Claude
// Code, Codex, Cursor, ...) access to app-specific custom tools through the
// mcpagent MCP bridge — the same mechanism AgentWorks workflows use to expose
// execute_shell_command.
//
// It encapsulates the wiring that the examples/claude-code-chat template spells
// out by hand: ensure the mcpbridge binary, generate a minimal MCP config,
// stand up the executor HTTP server, create the agent (bridge-only + code
// execution mode via the provider integration appenders), and register the
// caller's custom tools into the session-scoped codeexec registry so the bridge
// can resolve /tools/custom/{name} calls back to Go handlers running in THIS
// process.
//
// Agent and executor server run in the same process by construction — that is
// the whole point: RegisterCustomTool publishes handlers into a registry keyed
// by session id, and the executor server resolves them via the X-Session-ID
// header the bridge injects.
package agentsession

import (
	"context"
	"fmt"
	_ "github.com/manishiitg/coding-agent-loop/agent_go/pkg/common" // register coding-agent bridge token and managed-file guard
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Tool is one app-specific custom tool exposed to the agent through the bridge.
type Tool struct {
	Name        string
	Description string
	Category    string
	Params      map[string]interface{}
	Handler     func(ctx context.Context, args map[string]interface{}) (string, error)
}

// Message is one conversation turn.
type Message struct {
	Role string // "user" | "assistant"
	Text string
}

// Handle is a provider-native continuation handle (mcpagent's AgentSessionHandle)
// — for Claude Code it carries the CLI's own `--resume` session UUID. Persist it
// as opaque JSON per conversation and hand it back via Config.SessionHandle on the
// next turn: that is what lets a coding-agent restore full prior context after a
// process restart WITHOUT replaying the transcript (the CLI reloads it from its
// own on-disk session store). This is exactly how AgentWorks survives restarts.
type Handle = mcpagent.AgentSessionHandle

// Config parameterizes a Session. Only Provider, WorkingDir and Tools are
// really required for a useful session.
type Config struct {
	Provider llm.Provider // e.g. llm.ProviderClaudeCode
	ModelID  string       // "" -> llm.GetDefaultModel(provider)
	// ReasoningEffort, when set ("low"|"medium"|"high"|"max"), sets the model's
	// reasoning/thinking effort for providers that support it (Claude Code does).
	// Empty leaves the provider/model default. Plumbed through
	// RuntimeConfig.Generation.LLM as the primary model's Options["reasoning_effort"].
	ReasoningEffort string
	// APIKeys, when set, carry the server's provider-account resolver; the
	// model then goes through the same server-account admission as a turn.
	APIKeys      *llm.ProviderAPIKeys
	WorkingDir   string // scope root (Family/parent). "" -> process cwd
	SystemPrompt string // agent persona / instructions
	Tools        []Tool // app-specific custom tools
	// Skills are Anthropic-format skill bundles projected into the coding
	// agent's native skill directory for this session.
	Skills   []*llmtypes.Skill
	Logger   loggerv2.Logger
	MaxTurns int // 0 -> provider default
	// SessionID, when set, makes turns RESUME the coding agent's own session
	// (warm tmux/session resume) instead of cold-starting a fresh one. Use a
	// stable id per conversation (e.g. the conversation id). Empty -> fresh
	// throwaway session each turn (full-history replay).
	SessionID string
	// SessionHandle, when non-nil, restores the coding agent's provider-native
	// continuation state (for Claude Code: its `--resume` session UUID) BEFORE
	// the turn runs — so the CLI reloads full prior context from its own on-disk
	// session store instead of us replaying the transcript. This is the durable,
	// cross-restart context mechanism (the warm tmux session is only a
	// same-process speed path and dies on restart). Capture the fresh handle
	// after each turn via Session.Handle(), persist it per conversation, and pass
	// it back here on the next turn. Exactly the AgentWorks model. Nil on the very
	// first turn of a brand-new conversation (nothing to resume yet).
	SessionHandle *Handle
	// BridgeRoutingInstructions, when non-nil, overrides mcpagent's default
	// per-provider bridge-tool-routing system-prompt text (see
	// RuntimeConfig.Coding.BridgeRoutingInstructions and
	// docs/core/mcp_bridge_layer.md) — nil keeps mcpagent's default
	// (unconditionally applied for every provider); a pointer to "" suppresses
	// the block entirely; a non-empty string replaces it with this app's own
	// wording. Left unset (nil) for now — the default is left unchanged
	// everywhere this Config is built; this field only exists so a caller can
	// opt into custom wording later without further agentsession changes.
	BridgeRoutingInstructions *string
	// StreamCallback, when non-nil, is invoked with each content fragment as
	// the model generates its reply (real token/chunk streaming, not just a
	// cosmetic "working on it" status label) — via Turn.StreamingCallback,
	// which only ever delivers content fragments (never tool-call/terminal
	// chunks). Registering this callback enables the provider adapter's transcript
	// streaming option. Nil is a no-op: the turn behaves exactly as before,
	// with the reply available only once Ask returns.
	StreamCallback func(text string)
	// Observers receive mcpagent's normalized events. In particular, callers
	// can opt into DirectToolExecutionEvents to receive the bridge-side receipt
	// for every tool that really ran, including its arguments and result/error.
	Observers                 []mcpagent.AgentEventListener
	DirectToolExecutionEvents bool
	// Transport, when set, overrides the provider contract's declared process
	// transport for this one session — llm.CodingAgentTransportStructured runs
	// the CLI's one-shot JSON mode (no tmux pane, no live steering — Deliver
	// only queues) instead of the default llm.CodingAgentTransportTmux. Empty
	// keeps the contract's default (tmux, for every coding-agent provider
	// today). This exists to A/B the two transports from a live conversation;
	// see RuntimeConfig.Coding.Transport for the tradeoff.
	Transport llm.CodingAgentTransport
	// ClaudeCodeOAuthToken scopes this session to one user's own Claude Code
	// subscription (`claude setup-token`) instead of whatever login happens to
	// be on the machine. Reaches the adapter through
	// RuntimeConfig.Generation.APIKeys.
	//
	// Empty is NOT neutral: the provider falls back to the CLI's saved login and
	// logs "using the CLI saved login", so a missing token silently bills and
	// authenticates as whoever set up the terminal. Set RequireProviderToken to
	// refuse the session instead.
	ClaudeCodeOAuthToken string
	// RequireProviderToken refuses to start a session for a coding-agent
	// provider that has no token, rather than letting it fall back to the
	// machine's saved login. The check belongs here, at session construction:
	// the CLI session either starts authenticated as this user or does not
	// start at all.
	RequireProviderToken bool
}

func definitionFromConfig(cfg Config) mcpagent.AgentDefinition {
	directTools := make([]mcpagent.ToolDefinition, 0, len(cfg.Tools))
	for _, tool := range cfg.Tools {
		group := strings.TrimSpace(tool.Category)
		if group == "" {
			group = "family_tools"
		}
		directTools = append(directTools, mcpagent.ToolDefinition{
			Name:         tool.Name,
			Description:  tool.Description,
			InputSchema:  tool.Params,
			Execute:      tool.Handler,
			DisplayGroup: group,
		})
	}
	return mcpagent.AgentDefinition{
		Instructions: cfg.SystemPrompt,
		Skills:       cfg.Skills,
		Tools: mcpagent.ToolSet{
			Direct: directTools,
			MCP:    []mcpagent.MCPToolSource{{Name: "exa-search"}},
		},
	}
}

// Session bundles a live agent with its in-process executor server. Not safe
// for concurrent Ask calls; create one Session per conversation turn (cheap for
// a low-QPS local app) or serialize access.
type Session struct {
	agent    *mcpagent.Agent
	runtime  *mcpagent.Session
	logger   loggerv2.Logger
	shutdown func()
	closed   bool
	// holdsPriorContext reports whether the coding agent can ACTUALLY
	// reconstruct this conversation's history on its own — either a live warm
	// tmux session for this id already exists in this process, or a valid
	// provider-native SessionHandle was restored. Only then is it safe for Ask
	// to send just the newest message.
	//
	// This is deliberately NOT the same as "a SessionID was configured". A
	// SessionID expresses INTENT to keep the session warm going forward; it
	// says nothing about whether prior context exists YET. Conflating the two
	// silently dropped the entire conversation on every genuine cold start —
	// the first turn after a process restart (warm tmux gone, no handle
	// captured yet), a first turn whose handle failed to persist, or an engine
	// switch (the stored handle belongs to the old provider and is correctly
	// rejected). In all of those the CLI has nothing to resume from, so
	// truncating to the last message left the model with no history at all.
	holdsPriorContext bool
}

// WarmSharedBridge starts the process-global executor/MCP bridge (see
// ensureSharedBridge) if it has not started already, without creating a
// coding-agent session. Call this once at process startup so the first real
// turn does not pay for the bridge binary lookup and executor start. The
// bridge's address and token reach agents through explicit configuration,
// never the process environment, so there is no startup-order race to
// protect against any more.
func WarmSharedBridge(logger loggerv2.Logger) error {
	if logger == nil {
		logger = loggerv2.NewNoop()
	}
	_, err := ensureSharedBridge(logger)
	return err
}

// New builds a per-turn Session. Following the AgentWorks model, it reuses the
// process-global executor / MCP bridge (started once, on first use) and creates
// only the lightweight coding-agent for this turn. The bridge is long-lived and
// shared; the Session is cheap and disposable. The caller must Close() it, which
// closes ONLY the per-turn agent — never the shared bridge, and never the
// provider-owned interactive (tmux) session, so a warm-resume conversation stays
// warm across turns. Create one Session per turn (as AgentWorks rebuilds its
// per-turn agent wrapper); a stable SessionID makes the provider resume the same
// coding-agent CLI from its own owner registry.
func New(ctx context.Context, cfg Config) (*Session, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = loggerv2.NewNoop()
	}

	// Reuse the one shared executor/bridge (binary + MCP config + executor HTTP
	// server + env), started once for the whole process.
	b, err := ensureSharedBridge(logger)
	if err != nil {
		return nil, err
	}

	// Create the agent. The provider integration appenders apply bridge-only
	// access automatically at generation time; WithCodeExecutionMode(true) also
	// builds the tool index. WithSessionID scopes the custom-tool registry the
	// bridge resolves against.
	modelID := cfg.ModelID
	if strings.TrimSpace(modelID) == "" {
		modelID = llm.GetDefaultModel(cfg.Provider)
	}
	if err := llmguard.RequireCodingAgentProvider(string(cfg.Provider)); err != nil {
		return nil, err
	}
	model, err := llm.InitializeLLM(llmguard.WithServerAccountAdmission(llm.Config{
		Provider: cfg.Provider,
		ModelID:  modelID,
		Logger:   logger,
		Context:  ctx,
		APIKeys:  cfg.APIKeys,
	}))
	if err != nil {
		return nil, fmt.Errorf("initialize LLM: %w", err)
	}

	// A stable SessionID resumes the coding agent's own session across turns;
	// otherwise use a throwaway id (fresh session each turn).
	resume := strings.TrimSpace(cfg.SessionID) != ""
	sessionID := strings.TrimSpace(cfg.SessionID)
	if sessionID == "" {
		sessionID = "agentsession-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	// Whether the CLI can actually reconstruct history itself — see
	// Session.holdsPriorContext. Checked BEFORE rememberInteractiveOwner below
	// registers this turn's owner, so a first turn correctly reads "no warm
	// session yet" rather than seeing the entry it is about to create. The
	// provider must match too: a warm session (or handle) belonging to a
	// different engine cannot give this one context.
	handleRestored := cfg.SessionHandle != nil && !cfg.SessionHandle.Empty()
	holdsPriorContext := resume && (handleRestored || hasWarmInteractiveOwner(sessionID, cfg.Provider))

	// Authenticate as this user, or do not start. Without a token the provider
	// quietly uses the CLI's saved login, so a missing token is not a degraded
	// session — it is somebody else's account.
	oauthToken := strings.TrimSpace(cfg.ClaudeCodeOAuthToken)
	if cfg.RequireProviderToken && oauthToken == "" {
		return nil, fmt.Errorf("no Claude Code token configured for this session: add one (claude setup-token) before starting")
	}
	generation := mcpagent.GenerationRuntimeConfig{Provider: cfg.Provider, MaxTurns: cfg.MaxTurns}
	if oauthToken != "" {
		generation.APIKeys = &mcpagent.AgentAPIKeys{ClaudeCodeOAuthToken: &oauthToken}
	}

	runtime := mcpagent.RuntimeConfig{
		Model: model, MCPConfigPath: b.mcpConfigPath, ResumeHandle: cfg.SessionHandle,
		Generation: generation,
		Tools:      mcpagent.ToolRuntimeConfig{CodeExecution: true, Discovery: true},
		Coding:     mcpagent.CodingRuntimeConfig{Transport: cfg.Transport, BridgeRoutingInstructionsOverride: cfg.BridgeRoutingInstructions, BridgeBinary: b.bridgePath},
		MCP:        b.runtimeMCPConfig(sessionID),
		Workspace:  mcpagent.WorkspaceRuntimeConfig{CodingAgentWorkingDir: cfg.WorkingDir},
		Observability: mcpagent.ObservabilityRuntimeConfig{
			Logger:                    logger,
			Observers:                 cfg.Observers,
			DirectToolExecutionEvents: cfg.DirectToolExecutionEvents,
		},
	}
	if effort := strings.TrimSpace(cfg.ReasoningEffort); effort != "" {
		// Set the primary model's reasoning/thinking effort. GetLLMModelConfig
		// returns LLMConfig.Primary when its Provider is set, so specify the full
		// model here (provider + id) alongside the Options.
		runtime.Generation.LLM = mcpagent.AgentLLMConfiguration{
			Primary: mcpagent.LLMModel{
				Provider: string(cfg.Provider),
				ModelID:  modelID,
				Options:  map[string]interface{}{"reasoning_effort": effort},
			},
		}
	}
	if resume && cfg.Transport != llm.CodingAgentTransportStructured {
		// Keep the coding agent's interactive (tmux) session alive so the next
		// turn resumes it with full context instead of cold-starting. The
		// provider owns that session in its registry and reaps it on idle.
		// Meaningless under structured transport (no tmux process to keep warm
		// — each turn is a one-shot CLI invocation resumed via SessionHandle).
		switch cfg.Provider {
		case llm.ProviderClaudeCode:
			runtime.Coding.PersistentClaudeCode = true
		case llm.ProviderCodexCLI:
			runtime.Coding.PersistentCodex = true
		case llm.ProviderCursorCLI:
			runtime.Coding.PersistentCursor = true
		case llm.ProviderPiCLI:
			runtime.Coding.PersistentPi = true
		case llm.ProviderMuseCLI:
			runtime.Coding.PersistentMuse = true
		}
	}
	if cfg.StreamCallback != nil {
		runtime.Observability.StreamingCallback = func(chunk llmtypes.StreamChunk) {
			// Only forward genuine reply-content chunks. For interactive/tmux
			// providers the "content" stream is derived from tailing the raw
			// pane, and other chunk types (tool_call/tool_call_start/tool_call_end/
			// terminal/status_line) can carry literal terminal bytes — ANSI
			// escapes, SSH-agent/spinner noise, control chars — confirmed live:
			// without this check the app streamed raw pane scrollback straight
			// into the chat bubble instead of the model's actual reply.
			if chunk.Type != llmtypes.StreamChunkTypeContent {
				return
			}
			cfg.StreamCallback(chunk.Content)
		}
	}
	if len(cfg.Tools) > 0 {
		// Expose every app-registered custom tool as a NATIVE bridge tool for
		// THIS agent — scoped to this session only, never touching mcpagent's
		// shared package-level bridgeTools list (which stays fixed across
		// every consumer of that module; see docs/core/mcp_bridge_layer.md).
		names := make([]string, 0, len(cfg.Tools))
		for _, t := range cfg.Tools {
			names = append(names, t.Name)
		}
		runtime.Tools.AdditionalBridge = names
	}

	if resume {
		// Parent and child (any activity) always share the SAME physical
		// workspace directory as their coding-agent working dir — and an
		// interactive CLI's own per-session setup (Cursor's .cursor/hooks/,
		// Codex/Claude's own project-scoped config) writes shared files INTO
		// that directory, torn down again when ITS OWNER session closes.
		// Confirmed live: Cursor's deny-builtin-tools hook script vanished out
		// from under a still-running turn — the child side's (or a stale
		// prior) warm session was reaped and deleted the very hook script
		// this turn's tool calls depended on, and Cursor's failClosed hook
		// config denies everything when the hook command can't be found —
		// "I can't reach the workspace tools", with zero tool calls actually
		// attempted. Only one warm interactive session may exist at a time
		// for this workspace, full stop — closing any OTHER owner here before
		// creating this one is what actually prevents that collision, not
		// just documenting it.
		closeOtherInteractiveSessions(sessionID)
	}
	// The app's current activity directory is authoritative. A persisted handle
	// may contain an older shared root, so correct it before construction applies
	// the opaque continuation state.
	if cfg.SessionHandle != nil && !cfg.SessionHandle.Empty() && strings.TrimSpace(cfg.WorkingDir) != "" {
		cfg.SessionHandle.Provider.WorkingDir = cfg.WorkingDir
	}
	agent, err := mcpagent.NewAgentFromDefinition(ctx, definitionFromConfig(cfg), runtime)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	runtimeSession, err := agent.Start(ctx)
	if err != nil {
		agent.Close()
		return nil, fmt.Errorf("start agent session: %w", err)
	}

	// Track the warm-resume owner so /api/reset can proactively close its tmux
	// session (the provider otherwise reaps it on idle).
	if resume {
		rememberInteractiveOwner(sessionID, cfg.Provider)
	}

	s := &Session{
		agent:             agent,
		runtime:           runtimeSession,
		logger:            logger,
		holdsPriorContext: holdsPriorContext,
		shutdown:          func() { _ = agent.Close() }, // per-turn agent only; shared bridge + tmux persist
	}
	return s, nil
}

// ---------- process-global executor / MCP bridge + warm-owner tracking ----------
//
// AgentWorks runs ONE executor / MCP bridge for the whole process (its bridge is
// the main server's own route set, wired once at startup) and keeps warm
// coding-agent (tmux) sessions in the provider's owner registry, reaped by an
// idle timeout — there is no LRU or size cap. SparkQuill mirrors that: a single
// shared bridge (below), per-turn Sessions, and warm resume owned by the
// provider. We keep only a set of owner ids so reset can close their tmux.

// sharedBridge is the process-global executor/MCP bridge, created once.
//
// Its address, token and binary are handed to every agent EXPLICITLY (see
// runtimeMCPConfig) rather than exported into the process environment. The
// environment is shared by everything in the process — including, if this
// package is ever hosted inside the main AgentWorks server, that server's own
// executor — so publishing credentials there was a single-tenancy trap
// (docs/design/reusable_vertical_product_platform.md, "The root cause worth
// naming"). mcpagent prefers explicit configuration over MCP_* variables.
type sharedBridge struct {
	mcpConfigPath string
	bridgePath    string // mcpbridge executable
	hostURL       string // executor server, host-reachable
	apiToken      string // bearer for the executor
	shutdown      func() // executor + config cleanup; only run at process exit
}

// runtimeMCPConfig is what an agent needs to reach this bridge, expressed
// as explicit mcpagent configuration. Both URLs point at the one executor:
// custom tools run on the host, so there is no separate in-container URL.
func (b *sharedBridge) runtimeMCPConfig(sessionID string) mcpagent.MCPRuntimeConfig {
	return mcpagent.MCPRuntimeConfig{
		SessionID:        sessionID,
		APIBaseURL:       b.hostURL,
		APIToken:         b.apiToken,
		BridgeAPIBaseURL: b.hostURL,
	}
}

var (
	bridgeOnce sync.Once
	bridge     *sharedBridge
	bridgeErr  error

	ownerMu    sync.Mutex
	warmOwners = map[string]llm.Provider{}
	// warmOwnerLastUsed tracks when each warmOwners entry last actually ran a
	// turn — see HasOtherWarmInteractiveSession for why staleness here matters.
	warmOwnerLastUsed = map[string]time.Time{}
)

// warmOwnerFreshness bounds how long a warmOwners entry counts as "someone is
// using this" for HasOtherWarmInteractiveSession. Entries are otherwise NEVER
// removed on their own (only an explicit close deletes one — see
// closeOtherInteractiveSessions / CloseAllInteractiveSessions), so without this
// bound a single turn run once and never revisited again would read as
// "active" forever, permanently blocking Pulse from ever running (confirmed
// live: whole days with zero completed Pulse cycles, just repeated deferrals).
// Kept comfortably under tmux_sweep.go's 45-minute periodic reap threshold so
// this never says "still active" about a session that reap has already killed.
const warmOwnerFreshness = 30 * time.Minute

// ensureSharedBridge starts the process-global executor / MCP bridge exactly
// once and returns it on every later call. Following AgentWorks — whose bridge
// is the main server's own route set, wired once at startup — the bridge binary,
// MCP config and executor HTTP server are set up a single time and shared by
// every conversation and skill run; each agent is told where they are. The persistent
// coding-agent CLIs call back into this always-alive endpoint, so a resumed turn
// never hits a dead bridge. It is deliberately never torn down per turn.
func ensureSharedBridge(logger loggerv2.Logger) (*sharedBridge, error) {
	bridgeOnce.Do(func() {
		bridgePath, err := ensureBridgeBinary(logger)
		if err != nil {
			bridgeErr = err
			return
		}

		// No upstream MCP servers — all tools are custom and resolved in-process.
		mcpConfigPath, cleanupConfig, err := writeMinimalMCPConfig()
		if err != nil {
			bridgeErr = err
			return
		}

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			cleanupConfig()
			bridgeErr = fmt.Errorf("allocate executor listener: %w", err)
			return
		}
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		hostURL := "http://127.0.0.1:" + port

		// Custom tools run on the host, so the in-Docker URL and the bridge (host)
		// URL both point at this one executor server. Nothing is exported to
		// the process environment: see sharedBridge.
		apiToken := executor.GenerateAPIToken()

		execShutdown, err := startExecutorServer(logger, mcpConfigPath, listener, apiToken)
		if err != nil {
			listener.Close()
			cleanupConfig()
			bridgeErr = fmt.Errorf("start executor server: %w", err)
			return
		}
		time.Sleep(300 * time.Millisecond) // let it begin serving before first turn

		bridge = &sharedBridge{
			mcpConfigPath: mcpConfigPath,
			bridgePath:    bridgePath,
			hostURL:       hostURL,
			apiToken:      apiToken,
			shutdown: func() {
				execShutdown()
				cleanupConfig()
			},
		}
	})
	return bridge, bridgeErr
}

// rememberInteractiveOwner records a warm-resume owner (conversation id + its
// provider) so CloseAllInteractiveSessions can proactively close its tmux
// session on reset. This is just a set of ids, not a session cache.
func rememberInteractiveOwner(sessionID string, provider llm.Provider) {
	ownerMu.Lock()
	warmOwners[sessionID] = provider
	warmOwnerLastUsed[sessionID] = time.Now()
	ownerMu.Unlock()
}

// hasWarmInteractiveOwner reports whether THIS process already started a warm
// interactive session for sessionID under the same provider — i.e. whether a
// live CLI session plausibly still holds this conversation's context. Used to
// decide whether history may be truncated to the newest message (see
// Session.holdsPriorContext).
//
// The provider must match: after an engine switch the warm session belongs to
// the previous engine and cannot supply context to the new one, so this
// correctly reports false and the full thread is replayed.
//
// This is an optimistic signal, not proof of liveness — the provider reaps
// sessions on idle, so an entry can outlive its tmux session. That failure mode
// is benign and self-correcting: the restored SessionHandle covers it (the
// provider mints a fresh session and `--resume`s from it), which is the same
// path used after a process restart.
func hasWarmInteractiveOwner(sessionID string, provider llm.Provider) bool {
	ownerMu.Lock()
	defer ownerMu.Unlock()
	p, ok := warmOwners[sessionID]
	return ok && p == provider
}

// HasOtherWarmInteractiveSession reports whether some session OTHER than
// exceptSessionID is currently warm — i.e. whether starting a new session now
// would force-close someone else's still-active one (see closeOtherInteractiveSessions).
// For a deliberate, direct user action (opening parent or child chat) that's
// an acceptable cold-start cost. For an automatic background process like
// Pulse it is NOT: Pulse has no idea whether the child is mid-conversation
// when its own cadence happens to fire, so it should defer to the next cycle
// instead of silently evicting her live session out from under her the
// instant she stops actively typing. Callers that must not evict should check
// this FIRST and skip their turn if it's true, rather than calling New() and
// letting closeOtherInteractiveSessions do it unconditionally.
func HasOtherWarmInteractiveSession(exceptSessionID string) bool {
	ownerMu.Lock()
	defer ownerMu.Unlock()
	for id := range warmOwners {
		if id != exceptSessionID && time.Since(warmOwnerLastUsed[id]) < warmOwnerFreshness {
			return true
		}
	}
	return false
}

// CloseAllInteractiveSessions closes every warm coding-agent (tmux) session we
// have started, via the provider's owner-scoped close. Use on reset/shutdown for
// a clean slate; absent this call the provider reaps them on idle anyway. There
// is no LRU or size cap here — matching AgentWorks.
func CloseAllInteractiveSessions() {
	ownerMu.Lock()
	owners := make(map[string]llm.Provider, len(warmOwners))
	for id, p := range warmOwners {
		owners[id] = p
	}
	warmOwners = map[string]llm.Provider{}
	warmOwnerLastUsed = map[string]time.Time{}
	ownerMu.Unlock()

	for id, p := range owners {
		closeInteractiveOwner(id, p, "reset")
	}
}

// closeOtherInteractiveSessions closes every warm owner EXCEPT keepSessionID —
// see this function's call site in New() for why this must run before a new
// session is created, not just document the invariant.
func closeOtherInteractiveSessions(keepSessionID string) {
	ownerMu.Lock()
	toClose := make(map[string]llm.Provider)
	for id, p := range warmOwners {
		if id == keepSessionID {
			continue
		}
		toClose[id] = p
		delete(warmOwners, id)
		delete(warmOwnerLastUsed, id)
	}
	ownerMu.Unlock()

	for id, p := range toClose {
		closeInteractiveOwner(id, p, "another session needs this shared workspace")
	}
}

// interactiveOwnerClosers binds every persistent provider to its
// owner-scoped close. A provider missing here would silently skip its
// close while the caller already dropped its bookkeeping; the package
// variable keeps the binding injectable for dispatch tests.
var interactiveOwnerClosers = map[llm.Provider]func(id, reason string){
	llm.ProviderClaudeCode: llmproviders.CloseClaudeCodeInteractiveSessionForOwner,
	llm.ProviderCodexCLI:   llmproviders.CloseCodexCLIInteractiveSessionForOwner,
	llm.ProviderCursorCLI:  llmproviders.CloseCursorCLIInteractiveSessionForOwner,
	llm.ProviderPiCLI:      llmproviders.ClosePiCLIInteractiveSessionForOwner,
	llm.ProviderMuseCLI:    llmproviders.CloseMuseCLIInteractiveSessionForOwner,
	llm.ProviderAgyCLI:     llmproviders.CloseAgyCLIInteractiveSessionForOwner,
}

// closeInteractiveOwner dispatches to the provider-specific owner-scoped close.
func closeInteractiveOwner(id string, p llm.Provider, reason string) {
	if close, ok := interactiveOwnerClosers[p]; ok {
		close(id, reason)
	}
}

// Ask runs one turn over the supplied history and returns the assistant reply.
// When the coding agent demonstrably holds prior context itself — a live warm
// tmux session in this process, or a restored SessionHandle's `--resume` state
// after a restart — only the newest message is sent. This mirrors AgentWorks:
// the persistent/resumed CLI reconstructs history from its own store, and the
// provider adapter only forwards the latest message in that mode, so replaying
// the full thread would be dropped anyway.
//
// Otherwise the FULL thread is sent, because there is nothing to reconstruct
// from. That covers every genuine cold start — first turn after a restart, a
// handle that never persisted, or an engine switch that correctly rejected the
// previous provider's handle. Gating this on "a SessionID was configured"
// instead (the previous behavior) discarded the whole conversation in exactly
// those cases: intent to stay warm is not evidence that prior context exists.
func (s *Session) Ask(ctx context.Context, history []Message) (string, error) {
	if s.holdsPriorContext && len(history) > 0 {
		history = history[len(history)-1:]
	}
	msgs := make([]llmtypes.MessageContent, 0, len(history))
	for _, m := range history {
		role := llmtypes.ChatMessageTypeHuman
		if strings.EqualFold(m.Role, "assistant") || strings.EqualFold(m.Role, "ai") {
			role = llmtypes.ChatMessageTypeAI
		}
		msgs = append(msgs, llmtypes.MessageContent{
			Role:  role,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: m.Text}},
		})
	}
	result, err := s.runtime.Run(ctx, mcpagent.Turn{History: msgs})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sanitizeReply(result.Text)), nil
}

// Send injects input into the active turn without exposing the concrete Agent.
func (s *Session) Send(ctx context.Context, input string) error {
	_, err := s.runtime.Send(ctx, input)
	return err
}

// sanitizeReply strips internal CLI/transport notices that occasionally bleed
// into the captured assistant text. The coding CLI prints a line like
// "Shell cwd was reset to <dir>" when a command leaves the working directory
// changed; it is machine chatter, never meant for the parent, so drop it.
func sanitizeReply(reply string) string {
	if !strings.Contains(reply, "cwd was reset") {
		return reply
	}
	lines := strings.Split(reply, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		if strings.Contains(ln, "cwd was reset") {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

// Agent exposes the underlying agent for advanced callers (event listeners,
// usage stats). May be nil after Close.
func (s *Session) Agent() *mcpagent.Agent { return s.agent }

// Resumed reports whether this session continues an existing coding-agent
// session — a warm tmux owner in this process, or a restored provider-native
// --resume handle — rather than cold-starting the CLI.
//
// Exposed for latency attribution. A cold start pays the coding CLI's entire
// launch cost (spawn tmux, boot the CLI, load its own context) before the model
// sees a single token, and in a turn's total duration that is indistinguishable
// from a slow model unless it's recorded separately. See turntrace.go.
func (s *Session) Resumed() bool {
	if s == nil {
		return false
	}
	return s.holdsPriorContext
}

// Handle returns the coding agent's latest provider-native continuation handle
// (Claude Code's `--resume` UUID, etc.), captured from the just-completed turn.
// Persist it per conversation and pass it back via Config.SessionHandle next turn
// so context survives a process restart. Returns nil if the provider produced no
// resumable handle (e.g. a throwaway non-resume session). Call after Ask, before
// Close (it reads live agent state).
func (s *Session) Handle() *Handle {
	if s == nil || s.agent == nil {
		return nil
	}
	return s.runtime.Snapshot()
}

// Close disposes the per-turn agent. Safe to call more than once. It closes ONLY
// this turn's agent — never the process-global bridge and never the provider's
// interactive (tmux) session, which is owned by the provider registry and
// persists so a warm-resume conversation stays warm across turns.
func (s *Session) Close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.runtime != nil {
		_ = s.runtime.Close()
	}
	if s.shutdown != nil {
		s.shutdown()
	}
}

// ---------- helpers ----------

// ensureBridgeBinary resolves the mcpbridge binary, building it into
// ~/go/bin/mcpbridge from the sibling mcpagent module if necessary.
func ensureBridgeBinary(logger loggerv2.Logger) (string, error) {
	if envPath := strings.TrimSpace(os.Getenv("MCP_BRIDGE_BINARY")); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}
	if path, err := exec.LookPath("mcpbridge"); err == nil {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	goBin := filepath.Join(home, "go", "bin", "mcpbridge")
	if _, err := os.Stat(goBin); err == nil {
		return goBin, nil
	}
	// Attempt to build from the mcpagent module root.
	root := findMcpagentRoot()
	if root == "" {
		return "", fmt.Errorf("mcpbridge binary not found and mcpagent source not located; build it: go build -o ~/go/bin/mcpbridge ./cmd/mcpbridge/")
	}
	logger.Info("Building mcpbridge", loggerv2.String("root", root), loggerv2.String("out", goBin))
	cmd := exec.Command("go", "build", "-o", goBin, "./cmd/mcpbridge/")
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build mcpbridge: %w", err)
	}
	return goBin, nil
}

func findMcpagentRoot() string {
	dir, _ := os.Getwd()
	for i := 0; i < 6 && dir != "" && dir != "/"; i++ {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "mcpbridge")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	for _, c := range []string{"../mcpagent", "../../mcpagent", "../../../mcpagent"} {
		if _, err := os.Stat(filepath.Join(c, "cmd", "mcpbridge")); err == nil {
			return c
		}
	}
	return ""
}

// writeMinimalMCPConfig writes the MCP server config mcpagent connects to on
// this Agent's behalf (NOT the coding CLI directly — see get_api_spec /
// execute_shell_command in mcpagent's bridge-routing prompt, which discovers
// and calls these servers through the bridge, the same mechanism AgentWorks
// already uses in production via BaseOrchestrator.mcpConfigPath). Currently
// exa-search: Exa's free, hosted, no-auth web-search MCP server
// (https://mcp.exa.ai/mcp) — search-only, no code-execution capability, so it
// does not introduce a bridge-only containment escape hatch the way a
// code-exec-capable server would (see BuildBridgeMCPConfig's containment
// caveat in mcpagent).
func writeMinimalMCPConfig() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "agentsession-mcp-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp MCP config: %w", err)
	}
	config := `{"mcpServers":{"exa-search":{"url":"https://mcp.exa.ai/mcp"}}}`
	if _, err := f.WriteString(config); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("write temp MCP config: %w", err)
	}
	f.Close()
	name := f.Name()
	return name, func() { os.Remove(name) }, nil
}

// startExecutorServer stands up the per-tool executor HTTP server on the given
// listener. Custom tool resolution flows through the session-scoped codeexec
// registry populated by RegisterCustomTool.
func startExecutorServer(logger loggerv2.Logger, mcpConfigPath string, listener net.Listener, apiToken string) (func(), error) {
	handlers := executor.NewExecutorHandlers(mcpConfigPath, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)

	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/tools/mcp/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"success":false,"error":"invalid path"}`, http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolMCPRequest(w, r, parts[0], parts[1])
	})
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		tool := strings.TrimPrefix(r.URL.Path, "/tools/custom/")
		if tool == "" {
			http.Error(w, `{"success":false,"error":"missing tool"}`, http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolCustomRequest(w, r, tool)
	})
	mux.HandleFunc("/tools/virtual/", func(w http.ResponseWriter, r *http.Request) {
		tool := strings.TrimPrefix(r.URL.Path, "/tools/virtual/")
		if tool == "" {
			http.Error(w, `{"success":false,"error":"missing tool"}`, http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolVirtualRequest(w, r, tool)
	})

	srv := &http.Server{
		Handler:           executor.AuthMiddleware(apiToken)(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("executor server error", err)
		}
	}()

	return func() {
		sCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sCtx)
	}, nil
}

// serialize guards process-global MCP env vars while a Session is being built.
// Callers running concurrent Sessions should hold this via NewSerialized.
var serialize sync.Mutex

// NewSerialized is New wrapped in a package mutex, for callers that may build
// Sessions concurrently (the executor env vars are process-global).
func NewSerialized(ctx context.Context, cfg Config) (*Session, error) {
	serialize.Lock()
	defer serialize.Unlock()
	return New(ctx, cfg)
}
