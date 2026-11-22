package server

import (
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// agent_status returns an agent's recent chat (#3532): messages plus tool calls with their results, newest last,
// so another agent can see what it is working on without asking it. Default 5 items; the caller may ask for more.
const (
	agentRecentDefault  = 5
	agentRecentMax      = 50
	agentRecentTextMax  = 1500
	agentRecentToolArgs = 400
	agentRecentToolOut  = 800
)

type agentRecentItem struct {
	Role   string `json:"role"`           // "user", "assistant" or "tool"
	Text   string `json:"text,omitempty"` // a message, or the tool's name and arguments
	Result string `json:"result,omitempty"`
	Error  bool   `json:"error,omitempty"`
}

func agentRecentLimit(n int) int {
	if n <= 0 {
		return agentRecentDefault
	}
	return min(n, agentRecentMax)
}

func agentRecentClip(text string, limit int) string {
	text = strings.TrimSpace(redaction.Text(text))
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

// agentRecentItems is the last n visible items of a saved history: what people see in the chat, with each tool
// call paired with its result. Secrets are blanked and long text is cut. It walks back from the newest message and
// stops at n, so a long chat is not redacted whole on every read; a call's result comes after it, so it is seen first.
func agentRecentItems(history []llmtypes.MessageContent, n int) []agentRecentItem {
	n = agentRecentLimit(n)
	results := map[string]llmtypes.ToolCallResponse{}
	var items []agentRecentItem
	for i := len(history) - 1; i >= 0 && len(items) < n; i-- {
		message := history[i]
		role := strings.ToLower(string(message.Role))
		switch role {
		case "human":
			role = "user"
		case "ai":
			role = "assistant"
		}
		visible := role == "user" || role == "assistant"
		for j := len(message.Parts) - 1; j >= 0 && len(items) < n; j-- {
			switch p := message.Parts[j].(type) {
			case llmtypes.ToolCallResponse:
				if p.ToolCallID != "" {
					results[p.ToolCallID] = p
				}
			case llmtypes.TextContent:
				if !visible {
					continue
				}
				if text := continuityText(role, p.Text); text != "" && !isClaudeLocalCommandMessage(role, text) {
					items = append(items, agentRecentItem{Role: role, Text: agentRecentClip(text, agentRecentTextMax)})
				}
			case llmtypes.ToolCall:
				if !visible || p.FunctionCall == nil {
					continue
				}
				item := agentRecentItem{Role: "tool", Text: agentRecentClip(p.FunctionCall.Name+" "+p.FunctionCall.Arguments, agentRecentToolArgs)}
				if r, ok := results[p.ID]; ok {
					item.Result, item.Error = agentRecentClip(r.Content, agentRecentToolOut), r.IsError
				}
				items = append(items, item)
			}
		}
	}
	slices.Reverse(items)
	return items
}
