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

func (api *StreamingAPI) carryAgentSessionsIntoSave(userID, sessionID, workspacePath string, next *ChatHistoryAgentRuntime, history []llmtypes.MessageContent) {
	if next == nil {
		return
	}
	next.SeenMessages = len(history)
	next.SeenLastKey = continuityMessageKey(history)
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

// continuityMessageKey identifies the last message an agent saw, so the
// "since you left" recap still lines up when only the chat's tail is loaded.
func continuityMessageKey(history []llmtypes.MessageContent) string {
	if len(history) == 0 {
		return ""
	}
	last := history[len(history)-1]
	for _, part := range last.Parts {
		switch p := part.(type) {
		case llmtypes.ToolCall:
			return string(last.Role) + ":call:" + p.ID
		case llmtypes.ToolCallResponse:
			return string(last.Role) + ":result:" + p.ToolCallID
		case llmtypes.TextContent:
			return string(last.Role) + ":text:" + shortenContinuityTurn(strings.Join(strings.Fields(p.Text), " "), 160)
		}
	}
	return string(last.Role)
}

// historySince returns what happened after the agent last saw the chat, and
// whether that point was found.
func historySince(history []llmtypes.MessageContent, seen int, lastKey string) ([]llmtypes.MessageContent, bool) {
	if lastKey != "" {
		for i := len(history); i > 0; i-- {
			if continuityMessageKey(history[:i]) == lastKey {
				return history[i:], true
			}
		}
	}
	if seen > 0 && seen <= len(history) && lastKey == "" {
		return history[seen:], true
	}
	return history, false
}

// prependSwitchBackNotice tells a resumed agent what other agents did while
// it was away. Nothing is added when nothing happened.
func prependSwitchBackNotice(logCtx serverLogContext, from string, saved *ChatHistoryAgentRuntime, query, conversationPath string, history []llmtypes.MessageContent) string {
	since, found := historySince(history, saved.SeenMessages, saved.SeenLastKey)
	if !found {
		// The point it left could not be found: give the full recap instead.
		logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=switch_back from=%s to=%s action=full_recap seen_point_found=false", from, runtimeProvider(saved))
		return prependCodingAgentContinuityNotice(logCtx, "switch_back_full", query, conversationPath, history)
	}
	selected, omitted, files, stats := continuityHandoff(history, len(history)-len(since), continuityHandoffBytes)
	if stats.Items == 0 {
		logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=switch_back from=%s to=%s action=resumed_no_recap new_messages=%d", from, runtimeProvider(saved), len(since))
		return cleanChatHistoryQuery(query)
	}
	notice := continuityNoticeOpen + "\nYou are resuming your own earlier session of this chat. While you were away, another agent (" + from + ") continued it. " +
		fmt.Sprintf("%d of the %d items since you last saw it follow whole, oldest first, each headed by its line in the saved history. Files may have changed since you last read them.\n", stats.Included, stats.Items) +
		renderContinuityItems(selected) + "\n[end of what happened while you were away]\n" + continuityOmittedLine(omitted) + continuityChangedFilesLine(files) + continuityReadRule(conversationPath != "", len(omitted) > 0) + continuityNoticeClose
	logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=switch_back from=%s to=%s new_messages=%d", from, runtimeProvider(saved), len(since))
	logContinuityHandoff(logCtx, "switch_back", "resumed_with_recap", stats)
	return notice + sessionModeSplit + cleanChatHistoryQuery(query)
}
