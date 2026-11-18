package server

import (
	"sort"
	"time"

	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// startupResumeLimit is how many interrupted chats startup recovery resumes at
// once. Each resume can cold-start a native CLI and its bridge; starting every
// chat in the same second swapped a laptop. The rest stay in the durable queue
// and start one by one as resumed turns end.
const startupResumeLimit = 2

// startupResumeCap bounds how long one resumed chat holds its slot. The slot
// guards the cold start, not the whole turn: it frees at the first model event
// (reason=started) or after this cap (reason=cap), whichever is first, so a long
// background turn does not leave the other interrupted chats idle. Tests shorten it.
var startupResumeCap = 75 * time.Second

// startupResumeModelEvent reports whether an event comes from the running CLI or
// model. Events the server emits before the CLI is up (user_message) do not count.
func startupResumeModelEvent(eventType unifiedevents.EventType) bool {
	switch eventType {
	case unifiedevents.LLMGenerationStart, unifiedevents.StreamingStart, unifiedevents.StreamingChunk,
		unifiedevents.ToolCallStart, unifiedevents.ConversationThinking, unifiedevents.CodingAgentNativeActivity,
		unifiedevents.TokenUsage:
		return true
	}
	return false
}

// startupResumeTurnEvent frees the chat's slot at its first model event.
func (api *StreamingAPI) startupResumeTurnEvent(session string, eventType unifiedevents.EventType) {
	if startupResumeModelEvent(eventType) {
		api.releaseStartupResume(session, "started")
	}
}

// holdStartupResume caps a slot just given to session.
func (api *StreamingAPI) holdStartupResume(session string) {
	time.AfterFunc(startupResumeCap, func() { api.releaseStartupResume(session, "cap") })
}

// startRecoveredChats kicks the oldest recovered chats up to the limit and
// defers the rest. Called once by recoverConversationTurnQueue.
func (api *StreamingAPI) startRecoveredChats(oldest map[string]time.Time) {
	ordered := make([]string, 0, len(oldest))
	for session := range oldest {
		ordered = append(ordered, session)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !oldest[ordered[i]].Equal(oldest[ordered[j]]) {
			return oldest[ordered[i]].Before(oldest[ordered[j]])
		}
		return ordered[i] < ordered[j]
	})
	start := make([]string, 0, startupResumeLimit)
	api.conversationTurnQueueMu.Lock()
	if api.startupResumeRunning == nil {
		api.startupResumeRunning = make(map[string]bool)
	}
	for _, session := range ordered {
		if len(api.startupResumeRunning) < startupResumeLimit {
			api.startupResumeRunning[session] = true
			start = append(start, session)
		} else {
			api.startupResumeWaiting = append(api.startupResumeWaiting, session)
		}
	}
	waiting := len(api.startupResumeWaiting)
	api.conversationTurnQueueMu.Unlock()
	for _, session := range ordered[len(start):] {
		logTurnQueue("startup resume deferred session %s limit=%d waiting=%d", session, startupResumeLimit, waiting)
	}
	for _, session := range start {
		logTurnQueue("startup resume admitted session %s reason=startup limit=%d waiting=%d", session, startupResumeLimit, waiting)
		api.holdStartupResume(session)
		api.kickConversationTurnQueue(session)
	}
}

// admitDeferredStartupResume lets a deferred chat start at once when anything
// other than the gate kicks it. It does not take a slot.
func (api *StreamingAPI) admitDeferredStartupResume(session string) {
	api.conversationTurnQueueMu.Lock()
	index := -1
	for i, waiting := range api.startupResumeWaiting {
		if waiting == session {
			index = i
			break
		}
	}
	if index < 0 {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	api.startupResumeWaiting = append(api.startupResumeWaiting[:index], api.startupResumeWaiting[index+1:]...)
	waiting := len(api.startupResumeWaiting)
	api.conversationTurnQueueMu.Unlock()
	logTurnQueue("startup resume admitted session %s reason=new_activity waiting=%d", session, waiting)
}

// releaseStartupResume frees a resumed chat's slot and kicks the next deferred
// chat. A chat that holds no slot is ignored, so started, cap and turn_ended
// releases of the same chat free it once.
func (api *StreamingAPI) releaseStartupResume(session, reason string) {
	api.conversationTurnQueueMu.Lock()
	if !api.startupResumeRunning[session] {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	delete(api.startupResumeRunning, session)
	next := ""
	if len(api.startupResumeWaiting) > 0 {
		next = api.startupResumeWaiting[0]
		api.startupResumeWaiting = api.startupResumeWaiting[1:]
		api.startupResumeRunning[next] = true
	}
	waiting := len(api.startupResumeWaiting)
	api.conversationTurnQueueMu.Unlock()
	logTurnQueue("startup resume released session %s reason=%s waiting=%d", session, reason, waiting)
	if next != "" {
		logTurnQueue("startup resume admitted session %s reason=slot_free waiting=%d", next, waiting)
		api.holdStartupResume(next)
		api.kickConversationTurnQueue(next)
	}
}
