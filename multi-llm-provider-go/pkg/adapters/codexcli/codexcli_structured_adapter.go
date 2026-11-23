package codexcli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Package-level vars rather than consts so tests can shrink them.
var (
	// codexNaturalExitGrace is how long codex is given to exit on its own after
	// a terminal event before procshutdown starts signalling.
	codexNaturalExitGrace = 3 * time.Second
	// codexRolloutPollInterval is how often the rollout is checked for a native
	// task_complete while stdout has not reported turn.completed.
	codexRolloutPollInterval = 2 * time.Second
	// codexReapGrace bounds the final cmd.Wait. It sits above the full
	// procshutdown ladder (3 + 10 + 10 + 5s) so a teardown already in flight
	// always wins the race and this stays a genuine last resort.
	codexReapGrace = 45 * time.Second
)

// codexExecEvent is one JSONL line from `codex exec --json`. Verified live
// against the actually-installed codex-cli 0.145.0 (the doc's example format
// in docs/coding_sdk_structured_contract.md is stale — the real shape is
// simpler: thread.started -> turn.started -> item.started/item.completed
// (agent_message | command_execution) -> turn.completed).
type codexExecEvent struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id,omitempty"`
	Item     *codexExecItem  `json:"item,omitempty"`
	Usage    *codexExecUsage `json:"usage,omitempty"`
	// `error` events carry a top-level message; `turn.failed` carries {"error":{"message":...}}.
	Message string          `json:"message,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type codexExecItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"` // "agent_message" | "command_execution" | "mcp_tool_call" | "web_search"
	Text             string `json:"text,omitempty"`
	Command          string `json:"command,omitempty"`
	AggregatedOutput string `json:"aggregated_output,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Status           string `json:"status,omitempty"`
	// mcp_tool_call fields — verified live against codex-cli 0.145.0.
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
	// web_search field — verified live: item.started carries an empty query,
	// item.completed carries the real one (codex fills it in once the search
	// actually runs).
	Query string `json:"query,omitempty"`
	// web_search action ({"type":"search","query","queries"} or an opened
	// page's {"type":"open_page","url"}) and, on item.completed, the result
	// list ({"title","url","domain","snippet"}). See codexcli_web_search.go.
	Action            json.RawMessage        `json:"action,omitempty"`
	Results           []codexWebSearchResult `json:"results,omitempty"`
	Changes           json.RawMessage        `json:"changes,omitempty"`
	NativeArguments   string                 `json:"-"`
	SenderThreadID    string                 `json:"sender_thread_id,omitempty"`
	ReceiverThreadIDs []string               `json:"receiver_thread_ids,omitempty"`
	Prompt            string                 `json:"prompt,omitempty"`
	AgentsStates      json.RawMessage        `json:"agents_states,omitempty"`
}

// isCodexToolItem reports whether an item type represents a real tool
// invocation worth streaming as ToolCallStart/ToolCallEnd — native shell
// (command_execution), MCP bridge calls (mcp_tool_call), and codex's native
// web_search all count. web_search was missing here entirely until PLAT-247's
// live SearchWeb verification caught it: content streamed correctly (codex's
// final agent_message still fires normally) but no tool-call signal ever
// reached callers relying on the stream to know a search happened.
func isCodexToolItem(itemType string) bool {
	return itemType == "command_execution" || itemType == "mcp_tool_call" || itemType == "web_search" || itemType == "file_change" || itemType == "collab_tool_call"
}

// codexToolItemLabel renders a human-readable label for a tool-call chunk.
func codexToolItemLabel(item *codexExecItem) string {
	if item.Type == "mcp_tool_call" {
		return item.Server + "." + item.Tool
	}
	if item.Type == "web_search" {
		return item.Query
	}
	return item.Command
}

// codexToolItemName returns the semantic tool name consumed by downstream
// ToolCallStart/ToolCallEnd events. Content is display text only; putting the
// label there without setting StreamChunk.ToolName makes the app fall back to
// the literal name "tool".
func codexToolItemName(item *codexExecItem) string {
	if item == nil {
		return ""
	}
	if item.Type == "mcp_tool_call" {
		return strings.TrimSpace(item.Tool)
	}
	if item.Type == "command_execution" {
		return "exec_command"
	}
	if item.Type == "web_search" {
		return "web_search"
	}
	if item.Type == "file_change" {
		return "apply_patch"
	}
	if item.Type == "collab_tool_call" {
		return item.Tool
	}
	return ""
}

func codexToolItemArgs(item *codexExecItem) string {
	if item == nil {
		return ""
	}
	if item.Type == "file_change" {
		if item.NativeArguments != "" {
			return item.NativeArguments
		}
		return compactCodexJSON(item.Changes)
	}
	if item.Type == "collab_tool_call" {
		raw, _ := json.Marshal(map[string]interface{}{"receiver_thread_ids": item.ReceiverThreadIDs, "prompt": item.Prompt})
		return string(raw)
	}
	if item.Type == "mcp_tool_call" {
		return compactCodexJSON(item.Arguments)
	}
	if item.Type == "command_execution" && item.Command != "" {
		args, err := json.Marshal(map[string]string{"command": item.Command})
		if err == nil {
			return string(args)
		}
	}
	if item.Type == "web_search" {
		return codexWebSearchArgs(item.Query, item.Action)
	}
	return ""
}

func codexToolItemResult(item *codexExecItem) string {
	if item == nil {
		return ""
	}
	if item.Type == "file_change" {
		raw, _ := json.Marshal(map[string]interface{}{"status": item.Status, "changes": json.RawMessage(item.Changes)})
		return string(raw)
	}
	if item.Type == "collab_tool_call" {
		return compactCodexJSON(item.AgentsStates)
	}
	if item.Type == "command_execution" {
		return item.AggregatedOutput
	}
	if item.Type == "web_search" {
		if text := codexWebSearchResultText(item.Query, item.Action, item.Results); text != "" {
			return text
		}
	}
	if result := compactCodexJSON(item.Result); result != "" {
		return result
	}
	return compactCodexJSON(item.Error)
}

func compactCodexJSON(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return string(raw)
	}
	return compact.String()
}

type codexExecUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	CacheWriteInputTokens int `json:"cache_write_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

// buildCodexStructuredPrompt concatenates the FULL non-system history — unlike
// buildCodexInteractivePrompt (tmux), a structured call is a fresh process
// each time with no persistent session to hold prior turns, so every call must
// carry the whole conversation itself.
func buildCodexStructuredPrompt(messages []llmtypes.MessageContent) string {
	var b strings.Builder
	for _, m := range messages {
		text := extractTextFromMessage(m)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case llmtypes.ChatMessageTypeHuman:
			b.WriteString("User: ")
		case llmtypes.ChatMessageTypeAI:
			b.WriteString("Assistant: ")
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func intPtrIfNonZero(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func buildCodexStructuredEnv(apiKey string) []string {
	env := os.Environ()
	if strings.TrimSpace(apiKey) != "" {
		env = append(env, "OPENAI_API_KEY="+strings.TrimSpace(apiKey))
	}
	return env
}

func policyMode(opts *llmtypes.CallOptions) llmtypes.CLISecurityMode {
	if opts == nil || opts.CLISecurity == nil {
		return llmtypes.CLISecurityModeCompatibility
	}
	return opts.CLISecurity.Mode
}
