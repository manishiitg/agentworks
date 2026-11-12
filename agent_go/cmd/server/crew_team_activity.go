package server

import (
	"context"
	"encoding/json"
	"time"
)

// What the Team tab shows of another person's agents while they work (owner, 6 Nov: "when their agent is
// online/offline/working/loading I want to see that"): "running" while a turn is going. A Mac agent's turn is read from
// the events its Mac relays (crewRelayStream.running), a server agent's from the server's own turn queue.

var crewActivityStart = map[string]bool{"user_message": true, "conversation_start": true, "llm_generation_start": true, "streaming_start": true}
var crewActivityEnd = map[string]bool{"llm_generation_end": true, "unified_completion": true, "agent_end": true, "conversation_end": true, "conversation_error": true, "context_cancelled": true}

// crewRelayBatchRunning reads one batch of relayed events: whether its last lifecycle event starts or ends a turn;
// known is false when it holds none.
func crewRelayBatchRunning(body []byte) (running, known bool) {
	var envelope struct {
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false, false
	}
	for _, event := range envelope.Events {
		switch {
		case crewActivityStart[event.Type]:
			running, known = true, true
		case crewActivityEnd[event.Type]:
			running, known = false, true
		}
	}
	return running, known
}

// crewActivityStale: a turn the Mac never reported the end of (it was closed or lost its connection) stops counting.
const crewActivityStale = 20 * time.Minute

// crewMacConversationActivity is what this Mac conversation is doing now, from its Mac's reports and relayed events:
// "running", "waiting" (for its person) or "".
func crewMacConversationActivity(owner, project, conversation string) string {
	activity, _ := crewMacConversationActivitySince(owner, project, conversation)
	return activity
}

func crewMacConversationActivitySince(owner, project, conversation string) (string, time.Time) {
	if conversation == "" {
		return "", time.Time{}
	}
	crewRelayStreams.Lock()
	defer crewRelayStreams.Unlock()
	stream := crewRelayStreams.streams[owner+"/"+project+"/"+conversation]
	switch {
	case stream == nil:
		return "", time.Time{}
	case stream.waiting:
		return "waiting", stream.since
	case stream.running && time.Since(stream.since) < crewActivityStale:
		return "running", stream.since
	}
	return "", time.Time{}
}

// crewServerChatRunning: a turn is going in this server chat now.
func crewServerChatRunning(sessionID string) bool {
	return runningServerAPI != nil && sessionID != "" && runningServerAPI.sessionTurnRunning(sessionID)
}

// crewStandbyServedRunning: the server is answering for this Mac agent and a turn of its own chats here is going.
func crewStandbyServedRunning(owner, project, tab string) bool {
	if rows, err := readCrewStandby(project, owner); err != nil || rows[tab].Writer != "server" {
		return false
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(context.Background(), productConversationRegistryPath(owner))
	if err != nil {
		return false
	}
	for _, record := range doc.Entries {
		if record.ProfileID == crewProfileID && crewResourceProjectID(record.ConversationKey) == project && crewStandbyTabForKey(project, record.ConversationKey) == tab && crewServerChatRunning(record.SessionID) {
			return true
		}
	}
	return false
}

// One live-state rule for every Team projection. The maker retains their own
// pending question indefinitely; teammates stop seeing a wait after 30 minutes.
func crewTeamLiveActivity(activity string, since time.Time, mine bool, now time.Time) string {
	if activity == "waiting" && !mine && (since.IsZero() || !now.Before(since.Add(30*time.Minute))) {
		return ""
	}
	return activity
}
