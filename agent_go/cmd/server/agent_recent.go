package server

import (
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
// call paired with its result. Secrets are blanked and long text is cut.
func agentRecentItems(history []llmtypes.MessageContent, n int) []agentRecentItem {
	results := map[string]llmtypes.ToolCallResponse{}
	for _, message := range history {
		for _, part := range message.Parts {
			if r, ok := part.(llmtypes.ToolCallResponse); ok && r.ToolCallID != "" {
				results[r.ToolCallID] = r
			}
		}
	}
	var items []agentRecentItem
	for _, message := range history {
		role := strings.ToLower(string(message.Role))
		switch role {
		case "human":
			role = "user"
		case "ai":
			role = "assistant"
		}
		if role != "user" && role != "assistant" {
			continue
		}
		for _, part := range message.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				if text := continuityText(role, p.Text); text != "" && !isClaudeLocalCommandMessage(role, text) {
					items = append(items, agentRecentItem{Role: role, Text: agentRecentClip(text, agentRecentTextMax)})
				}
			case llmtypes.ToolCall:
				if p.FunctionCall == nil {
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
	if n = agentRecentLimit(n); len(items) > n {
		items = items[len(items)-n:]
	}
	return items
}
