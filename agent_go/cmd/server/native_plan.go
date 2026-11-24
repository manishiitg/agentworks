package server

import (
	"log"
	"net/http"
	"strings"
	"unicode"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// "/plan <task>": this one message runs in the coding program's own plan mode
// (Claude Code, Codex, Cursor). The program reads and plans without changing
// anything; Claude asks to approve its plan, and the next ordinary message runs
// in the chat's normal mode again. The saved message keeps its "/plan" text, so
// a queued /plan message is recognised again when it runs; only the agent's
// prompt loses the command word.

func parseNativePlanCommand(query string) (string, bool) {
	text := strings.TrimSpace(query)
	head, rest := text, ""
	if boundary := strings.IndexFunc(text, unicode.IsSpace); boundary >= 0 {
		head, rest = text[:boundary], text[boundary:]
	}
	if head != "/plan" {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// Program support is a backend fact, shared by admission and the composer's Plan switch.
func nativePlanProgramCapabilities() map[string]bool {
	out := make(map[string]bool, len(supportedLLMProviders))
	for _, provider := range supportedLLMProviders {
		out[provider] = nativePlanSupported(provider)
	}
	return out
}

func nativePlanSupported(provider string) bool {
	switch provider {
	case "claude-code", "codex-cli", "cursor-cli":
		return true
	}
	return false
}

// prepareNativePlanQuery runs after project, tab and account admission. Only a
// person's own message turns plan mode on: agent messages and automatic turns
// starting with "/plan" stay ordinary text.
func (api *StreamingAPI) prepareNativePlanQuery(w http.ResponseWriter, r *http.Request, req *QueryRequest, sessionID, provider string) (*http.Request, bool) {
	task, known := parseNativePlanCommand(req.Query)
	if req.IsAutoNotification || req.AgentMessageInbox != "" {
		return r, false
	}
	if !known && req.PlanMode {
		// The Plan switch: the whole message is the task.
		task, known = strings.TrimSpace(req.Query), true
	}
	if !known || req.RequestedSkillID != "" {
		return r, false
	}
	if !nativePlanSupported(provider) || !isToolBackedChatMode(req.AgentMode) {
		log.Printf("[PLAN_MODE] session=%s provider=%s action=skipped reason=unsupported_program", sessionID, provider)
		api.answerNativeGoalCommand(w, sessionID, "Plan mode works in Claude Code, Codex and Cursor chats. Send the message without /plan, or switch the chat to one of them.")
		return r, true
	}
	if task == "" {
		api.answerNativeGoalCommand(w, sessionID, "Type /plan and what to plan, for example: /plan add a dark mode setting")
		return r, true
	}
	// A running turn would take this as live input in its own mode; it waits as its own plan turn.
	req.DisableLiveInputDelivery = true
	log.Printf("[PLAN_MODE] session=%s provider=%s action=plan_turn", sessionID, provider)
	return r.WithContext(llmtypes.WithNativePlanMode(r.Context(), sessionID)), false
}

// nativePlanPrompt is the agent's prompt for a plan turn: the task without the command word.
func nativePlanPrompt(query string) string {
	if task, ok := parseNativePlanCommand(query); ok && task != "" {
		return task
	}
	return query
}
