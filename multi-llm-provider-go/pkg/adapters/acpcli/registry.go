// Package acpcli runs any coding agent that speaks the Agent Client Protocol
// (newline-delimited JSON-RPC 2.0 on the agent's stdio) as one provider,
// "acp-cli". The agent comes from the model id: "<agentID>/<agent model id>",
// or "<agentID>" alone for that agent's default model.
package acpcli

import (
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ProviderID is the provider name this adapter reports.
const ProviderID = "acp-cli"

// Agent is one ACP agent the provider can launch.
type Agent struct {
	ID    string
	Label string
	// Binary and Args launch the agent's ACP server on stdio. FullToolsArgs,
	// when set, replaces Args for a chat whose native tools mode is "full"
	// (the agent then runs its tools without asking).
	Binary        string
	Args          []string
	FullToolsArgs []string
	// InstallCommand installs the agent's CLI; empty when not confirmed.
	InstallCommand string
	// LoginCommand is what a person runs to sign in to the agent.
	LoginCommand string
	// InitMeta and CancelMeta are sent as _meta on initialize and session/cancel.
	InitMeta   map[string]any
	CancelMeta map[string]any
	// APIKeyEnv lists environment variables that carry the agent's own API
	// key. When one is set, authenticate uses APIKeyAuthMethod; otherwise
	// StoredAuthMethod (empty: rely on the agent's stored login silently).
	APIKeyEnv        []string
	APIKeyAuthMethod string
	StoredAuthMethod string
	// FullToolsConfig is a session config option set to On for "full" native
	// tools and Off otherwise, when the agent offers it (Kiro's autopilot).
	FullToolsConfig *ConfigToggle
	// QuestionForm marks agents that ask questions as a qN / qN_custom
	// elicitation form (OpenCode).
	QuestionForm bool

	// Provider, when set, is the provider id of a provider with its own name
	// that runs this agent through the connector (Generate). Such an agent is
	// not offered as an acp-cli model, and its model ids are the agent's own.
	Provider string
	// MetaPrefix names the response and stream metadata keys
	// (<prefix>_session_id); "acp" when empty. ResponseMeta is added as is.
	MetaPrefix   string
	ResponseMeta map[string]any
	// SessionMeta, when set, carries the chat's instructions as _meta on
	// session/new and session/load instead of ahead of the first prompt.
	SessionMeta func(instructions string) map[string]any
	// ModelLevels are thought levels a model id may end with
	// ("<model>-<level>"): the agent gets <model>, and <level> is the thought
	// level unless the chat chose an effort.
	ModelLevels []string
	// ModelConfigID and ThoughtConfigID are the config option ids used when
	// the agent has not listed its config options (session/load answers).
	ModelConfigID   string
	ThoughtConfigID string
	// SteersWithPrompt, given the version the agent reports at initialize, says whether a session/prompt sent while
	// one runs is a message into the running turn (see steer.go) rather than a queued or cancelling prompt.
	SteersWithPrompt func(version string) bool
	// StrictResume fails a turn whose session/load fails instead of opening
	// a new conversation.
	StrictResume bool
	// Launch, when set, prepares the agent's process (arguments, environment,
	// confinement, private files); acpcli still owns the protocol. The
	// agent's own package sets it with SetLaunch.
	Launch func(LaunchRequest) (*LaunchSpec, error)
}

// ConfigToggle is a select config option with two values.
type ConfigToggle struct{ ID, On, Off string }

// LaunchRequest is what an agent's Launch hook gets for one process.
type LaunchRequest struct {
	Opts      *llmtypes.CallOptions
	Owner     string
	WorkDir   string
	ToolsMode string // "", "mcp_only" or "full"
	MCPConfig string
}

// LaunchSpec is an agent's own process setup.
type LaunchSpec struct {
	Args []string
	Env  []string
	// Confine wraps the command in the agent's sandbox before it starts; it
	// replaces the default confinement. Its cleanup runs when the process exits.
	Confine func(cmd *exec.Cmd) (func(), error)
	// Release runs once the process has exited.
	Release func()
	// AutoApprove answers every permission request allowed (the agent's own
	// hook decides what may run).
	AutoApprove bool
	// QuestionIDPrefix marks the agent's own questions to the person: it asks them as permission requests
	// whose tool call id starts with this, and their options are answers, not approvals. They always go to
	// the person, even when AutoApprove answers real permission requests.
	QuestionIDPrefix string
	// NoSessionMCP sends no MCP servers in session/new (the agent reads its
	// own MCP catalogue).
	NoSessionMCP bool
	// Outcome classifies turn failures; nil uses the stop reason only.
	Outcome Outcome
}

// Outcome classifies an agent's turn failures beyond its stop reason.
type Outcome interface {
	// BeginTurn is called before a prompt; the returned check runs after it
	// with the stop reason and returns the turn's failure, if any.
	BeginTurn(sessionID string) func(model, stopReason string) error
	// RPCFailure maps a failed request (prompt or model selection).
	RPCFailure(model, sessionID string, err error) error
}

// FinishedCheck is an optional part of an Outcome. BeginFinished is called before a prompt; the returned check
// reports whether the agent's own record shows that turn's reply finished. A turn whose prompt has gone silent
// ends with its reply when it does.
type FinishedCheck interface {
	BeginFinished(sessionID string) func() bool
}

var launchHooks = struct {
	sync.Mutex
	m map[string]func(LaunchRequest) (*LaunchSpec, error)
}{m: map[string]func(LaunchRequest) (*LaunchSpec, error){}}

// SetLaunch installs an agent's process setup hook (see Agent.Launch).
func SetLaunch(agentID string, launch func(LaunchRequest) (*LaunchSpec, error)) {
	launchHooks.Lock()
	defer launchHooks.Unlock()
	launchHooks.m[strings.ToLower(agentID)] = launch
}

// lookupAny finds any registered agent, including those other providers run.
func lookupAny(id string) (Agent, bool) {
	agent, ok := registry[strings.ToLower(strings.TrimSpace(id))]
	if !ok {
		return Agent{}, false
	}
	launchHooks.Lock()
	if launch := launchHooks.m[agent.ID]; launch != nil {
		agent.Launch = launch
	}
	launchHooks.Unlock()
	return agent, true
}

func (a Agent) splitModel(model, effort string) (string, string) {
	for _, level := range a.ModelLevels {
		if strings.HasSuffix(model, "-"+level) {
			if effort == "" {
				effort = level
			}
			return strings.TrimSuffix(model, "-"+level), effort
		}
	}
	return model, effort
}

func (a Agent) providerID() string {
	if a.Provider != "" {
		return a.Provider
	}
	return ProviderID
}

func (a Agent) metaKey(name string) string {
	prefix := a.MetaPrefix
	if prefix == "" {
		prefix = "acp"
	}
	return prefix + "_" + name
}

var registry = map[string]Agent{
	"opencode": {
		ID: "opencode", Label: "OpenCode",
		Binary: "opencode", Args: []string{"acp"},
		InstallCommand: "npm install -g opencode-ai",
		LoginCommand:   "opencode auth login",
		QuestionForm:   true,
	},
	"grok": {
		ID: "grok", Label: "Grok",
		Binary: "grok",
		// argv overrides Grok's own config, so a supervised chat cannot
		// inherit an always-approve setting.
		Args:           []string{"--permission-mode", "default", "agent", "stdio"},
		FullToolsArgs:  []string{"agent", "--always-approve", "stdio"},
		InstallCommand: "npm install -g @xai-official/grok",
		LoginCommand:   "grok login",
		// Grok's generic client type silently denies auto refusals; the
		// extension type asks instead.
		InitMeta: map[string]any{"clientType": "extension"},
		// Ctrl+C semantics stop stale background wakes until the next turn.
		CancelMeta:       map[string]any{"cancelTrigger": "ctrl_c"},
		APIKeyEnv:        []string{"XAI_API_KEY", "GROK_API_KEY"},
		APIKeyAuthMethod: "xai.api_key",
		StoredAuthMethod: "cached_token",
	},
	"kiro": {
		ID: "kiro", Label: "Kiro",
		Binary: "kiro-cli",
		// The default engine is a v2 server that does not speak this
		// protocol; --auth-method=cli keeps access tokens inside Kiro.
		Args: []string{"acp", "--agent-engine=v3", "--auth-method=cli"},
		// TODO: confirm the official kiro-cli install command.
		InstallCommand:  "",
		LoginCommand:    "kiro-cli login",
		FullToolsConfig: &ConfigToggle{ID: "autopilot", On: "on", Off: "off"},
	},
	// Antigravity runs as its own provider (agy-cli); package agycli sets
	// its launch (private home, tool-mode hook, sandbox, outcome reading).
	"agy": {
		ID: "agy", Label: "Antigravity",
		Binary: "agy", Args: []string{"acp", "--disable-telemetry"},
		Provider: "agy-cli", MetaPrefix: "agy", ResponseMeta: map[string]any{"agy_cli_mode": "acp"},
		LoginCommand: "agy",
		// The service's Gemini key, when the chat's account gives one, signs
		// in; otherwise agy uses its stored login.
		APIKeyEnv:        []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		APIKeyAuthMethod: "gemini-api-key",
		SessionMeta: func(instructions string) map[string]any {
			return map[string]any{"antigravity": map[string]any{"systemPrompt": map[string]any{"append": instructions}}}
		},
		// "gemini-3.8-flash-high" is model gemini-3.8-flash at thought level high.
		ModelLevels:     []string{"low", "medium", "high", "xhigh"},
		ModelConfigID:   "model",
		ThoughtConfigID: "thought_level",
		StrictResume:    true,
		// agy acp 1.5.0 takes a prompt sent mid-turn into the running turn; the 1.4 harness queues it.
		SteersWithPrompt: func(version string) bool { return versionAtLeast(version, 1, 5, 0) },
	},
}

// LookupAgent returns the acp-cli agent with this id.
func LookupAgent(id string) (Agent, bool) {
	agent, ok := lookupAny(id)
	if !ok || agent.Provider != "" {
		return Agent{}, false
	}
	return agent, true
}

// Agents lists the acp-cli agents, sorted by id.
func Agents() []Agent {
	out := make([]Agent, 0, len(registry))
	for _, agent := range registry {
		if agent.Provider == "" {
			out = append(out, agent)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// SplitModelID splits "<agentID>/<model>" into the agent id and the agent's
// own model id ("" means the agent's default model).
func SplitModelID(modelID string) (agentID, model string) {
	agentID, model, _ = strings.Cut(strings.TrimSpace(modelID), "/")
	return strings.ToLower(strings.TrimSpace(agentID)), strings.TrimSpace(model)
}

func (a Agent) launchArgs(fullTools bool) []string {
	if fullTools && len(a.FullToolsArgs) > 0 {
		return append([]string(nil), a.FullToolsArgs...)
	}
	return append([]string(nil), a.Args...)
}
