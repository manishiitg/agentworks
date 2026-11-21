package server

import (
	"encoding/json"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// What the Team tab shows of another person's agents while they work (owner, 6 Nov: "when their agent is
// online/offline/working/loading I want to see that"): "running" while a turn is going. A Mac agent's turn is read from
// the events its Mac relays (crewRelayStream.running), a server agent's from the server's own turn queue.

var crewActivityStart = map[string]bool{"user_message": true, "conversation_start": true, "llm_generation_start": true, "streaming_start": true}

// crewRelayBatchRunning reads one batch of relayed events: whether its last lifecycle event starts or ends a turn;
// known is false when it holds none.
func crewRelayBatchRunning(body []byte) (running, known bool) {
	var envelope struct {
		Events []struct {
			Type string `json:"type"`
			Data struct {
				Data struct {
					Metadata map[string]interface{} `json:"metadata"`
				} `json:"data"`
			} `json:"data"`
		} `json:"events"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false, false
	}
	for _, event := range envelope.Events {
		switch {
		case crewActivityStart[event.Type]:
			running, known = true, true
		case events.IsTurnEndType(event.Type):
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
	case stream.running && time.Since(stream.activityAt) < crewActivityStale:
		return "running", stream.since
	}
	return "", time.Time{}
}

// crewServerChatRunning: a turn is going in this server chat now.
func crewServerChatRunning(sessionID string) bool {
	return runningServerAPI != nil && sessionID != "" && runningServerAPI.sessionTurnRunning(sessionID)
}

// One live-state rule for every Team projection. The maker retains their own
// pending question indefinitely; teammates stop seeing a wait after 30 minutes.
func crewTeamLiveActivity(activity string, since time.Time, mine bool, now time.Time) string {
	if activity == "waiting" && !mine && (since.IsZero() || !now.Before(since.Add(30*time.Minute))) {
		return ""
	}
	return activity
}

// Metadata only from the existing relay lifecycle. Idle housekeeping is not
// work: use the last transition, not the socket's last-seen/heartbeat time.
func crewMacConversationLastWorkedAt(owner, project, conversation string) time.Time {
	if conversation == "" {
		return time.Time{}
	}
	crewRelayStreams.Lock()
	defer crewRelayStreams.Unlock()
	stream := crewRelayStreams.streams[owner+"/"+project+"/"+conversation]
	if stream == nil {
		return time.Time{}
	}
	if stream.running || stream.waiting {
		return stream.activityAt
	}
	return stream.since
}

func crewServerChatLastWorkedAt(session string) time.Time {
	api := runningServerAPI
	if api == nil || session == "" {
		return time.Time{}
	}
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	if row := api.activeSessions[session]; row != nil {
		return row.LastWorkedAt
	}
	return time.Time{}
}
