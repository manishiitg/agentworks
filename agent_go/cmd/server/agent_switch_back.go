package server

import (
	"fmt"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Switching a chat back to an agent it used before resumes that agent's own
// native session (#3140): it still remembers its part of the chat, and gets a
// recap of only what happened since it last saw it. Each chat keeps the last
// session of every coding agent it used in its saved runtime.
const maxOtherAgentSessions = 6

// lastSavedAgentRuntimes caches each chat's last saved runtime (user+session),
// so carrying the other agents' sessions forward does not read the file every
// turn. A miss after a restart reads the saved conversation once.
var lastSavedAgentRuntimes sync.Map

func runtimeProvider(rt *ChatHistoryAgentRuntime) string {
	if rt == nil {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(rt.Provider))
	if provider == "" && rt.AgentSessionHandle != nil {
		provider = strings.ToLower(strings.TrimSpace(rt.AgentSessionHandle.Provider.Provider))
	}
	return provider
}

// pickSavedRuntime returns the saved session to resume for provider. A chat
// switching back to an agent it used before gets that agent's own session,
// marked with the agent it is coming from.
func pickSavedRuntime(rt *ChatHistoryAgentRuntime, provider string) *ChatHistoryAgentRuntime {
	chosen, back := runtimeForProvider(rt, provider)
	if back {
		chosen.switchedBackFrom = runtimeProvider(rt)
	}
	return chosen
}

// runtimeForProvider returns the saved session of the agent the chat runs now:
// the current one, or one it used earlier. switchedBack reports the latter.
func runtimeForProvider(rt *ChatHistoryAgentRuntime, provider string) (*ChatHistoryAgentRuntime, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if rt == nil || provider == "" || runtimeProvider(rt) == provider {
		return rt, false
	}
	other := rt.OtherAgentSessions[provider]
	if other == nil || other.Kind != "coding_agent" {
		return rt, false
	}
	chosen := *other
	chosen.OtherAgentSessions = nil
	return &chosen, true
}

// carryOtherAgentSessions keeps every earlier agent's last session on the
// runtime being saved. When the agent changed, the previous one joins them.
func carryOtherAgentSessions(prev, next *ChatHistoryAgentRuntime) {
	if next == nil {
		return
	}
	current := runtimeProvider(next)
	others := map[string]*ChatHistoryAgentRuntime{}
	if prev != nil {
		for provider, rt := range prev.OtherAgentSessions {
			if rt != nil && provider != current {
				others[provider] = rt
			}
		}
		if p := runtimeProvider(prev); p != "" && p != current && prev.Kind == "coding_agent" {
			stashed := *prev
			stashed.OtherAgentSessions = nil
			others[p] = &stashed
		}
	}
	for len(others) > maxOtherAgentSessions {
		oldest := ""
		for provider, rt := range others {
			if oldest == "" || rt.CapturedAt < others[oldest].CapturedAt {
				oldest = provider
			}
		}
		delete(others, oldest)
	}
	if len(others) > 0 {
		next.OtherAgentSessions = others
	}
}

// base: saved messages before history when memory holds only a recent window.
func (api *StreamingAPI) carryAgentSessionsIntoSave(userID, sessionID, workspacePath string, next *ChatHistoryAgentRuntime, history []llmtypes.MessageContent, base int) {
	if next == nil {
		return
	}
	next.SeenLine = base + continuitySavedLines(history)
	key := userID + "\x00" + sessionID
	var prev *ChatHistoryAgentRuntime
	if cached, ok := lastSavedAgentRuntimes.Load(key); ok {
		prev = cached.(*ChatHistoryAgentRuntime)
	} else if rt, ok, err := ReadChatHistoryRuntimeForSession(userID, sessionID, workspacePath); err == nil && ok {
		prev = rt
	}
	before := len(next.OtherAgentSessions)
	carryOtherAgentSessions(prev, next)
	if p := runtimeProvider(prev); p != "" && p != runtimeProvider(next) {
		logfWithContext(newServerLogContext("", "", "", userID, "", sessionID), "[CONVERSATION_HANDOFF] action=kept_previous_session from=%s to=%s kept_sessions=%d", p, runtimeProvider(next), len(next.OtherAgentSessions))
	} else if before != len(next.OtherAgentSessions) && len(next.OtherAgentSessions) > 0 {
		logfWithContext(newServerLogContext("", "", "", userID, "", sessionID), "[CONVERSATION_HANDOFF] action=carried_sessions kept_sessions=%d", len(next.OtherAgentSessions))
	}
	lastSavedAgentRuntimes.Store(key, next)
}

// continuitySavedLines is how many lines the saved history has: one per
// message, system prompts not counted. The log only grows, so a line number
// names the same message for the life of the chat.
func continuitySavedLines(history []llmtypes.MessageContent) int {
	lines := 0
	for _, message := range history {
		if message.Role != llmtypes.ChatMessageTypeSystem {
			lines++
		}
	}
	return lines
}

// continuityAfterLine returns the index in history of the first message after
// saved line seen, and whether that line exists.
func continuityAfterLine(history []llmtypes.MessageContent, seen int) (int, bool) {
	if seen <= 0 {
		return 0, false
	}
	lines := 0
	for i, message := range history {
		if message.Role == llmtypes.ChatMessageTypeSystem {
			continue
		}
		if lines == seen {
			return i, true
		}
		lines++
	}
	return len(history), lines == seen
}

// prependSwitchBackNotice tells a resumed agent what other agents did while
// it was away. Nothing is added when nothing happened.
func prependSwitchBackNotice(logCtx serverLogContext, from string, saved *ChatHistoryAgentRuntime, query, conversationPath string, history []llmtypes.MessageContent, budget int) string {
	start, found := continuityAfterLine(history, saved.SeenLine)
	if !found {
		// The point it left could not be found: give the full recap instead.
		logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=switch_back from=%s to=%s action=full_recap seen_line=%d saved_lines=%d", from, runtimeProvider(saved), saved.SeenLine, continuitySavedLines(history))
		return prependCodingAgentContinuityNotice(logCtx, "switch_back_full", query, conversationPath, history, budget)
	}
	selected, omitted, files, stats := continuityHandoff(history, start, budget)
	if stats.Items == 0 {
		logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=switch_back from=%s to=%s action=resumed_no_recap new_messages=%d", from, runtimeProvider(saved), len(history)-start)
		return cleanChatHistoryQuery(query)
	}
	notice := continuityNoticeOpen + "\nYou are resuming your own earlier session of this chat. While you were away, another agent (" + from + ") continued it. " +
		fmt.Sprintf("%d of the %d items since you last saw it follow whole, oldest first, each headed by its line in the saved history. Files may have changed since you last read them.\n", stats.Included, stats.Items) +
		renderContinuityItems(selected) + "\n[end of what happened while you were away]\n" + continuityOmittedLine(omitted) + continuityChangedFilesLine(files) + continuityReadRule(conversationPath != "", len(omitted) > 0) + continuityNoticeClose
	logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=switch_back from=%s to=%s new_messages=%d", from, runtimeProvider(saved), len(history)-start)
	logContinuityHandoff(logCtx, "switch_back", "resumed_with_recap", stats)
	return notice + sessionModeSplit + cleanChatHistoryQuery(query)
}
