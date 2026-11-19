// Package acpcli runs any coding agent that speaks the Agent Client Protocol
// (newline-delimited JSON-RPC 2.0 on the agent's stdio) as one provider,
// "acp-cli". The agent comes from the model id: "<agentID>/<agent model id>",
// or "<agentID>" alone for that agent's default model.
package acpcli

import (
	"sort"
	"strings"
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
}

// ConfigToggle is a select config option with two values.
type ConfigToggle struct{ ID, On, Off string }

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
}

// LookupAgent returns the registered agent with this id.
func LookupAgent(id string) (Agent, bool) {
	agent, ok := registry[strings.ToLower(strings.TrimSpace(id))]
	return agent, ok
}

// Agents lists the registered agents, sorted by id.
func Agents() []Agent {
	out := make([]Agent, 0, len(registry))
	for _, agent := range registry {
		out = append(out, agent)
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
