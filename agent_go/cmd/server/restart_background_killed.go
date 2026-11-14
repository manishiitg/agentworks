package server

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// An app restart ends the background commands and helpers of every chat, including chats between turns. A chat whose
// turn was open is continued by restart-continue; a chat between turns was skipped (turn_not_open), and since the
// command died no "background finished" wake ever came, so its agent sat idle for good. At startup such a chat now gets
// its dead background work marked stopped and one wake turn, through the same restart-continue queue (the stored
// chat settings are what a turn needs after a restart; the in-memory wake queue has no agent yet).
const restartBackgroundScanEvents = 1000

const restartBackgroundKilledMessage = "[Background command stopped] The app restarted and stopped a command you started earlier: %s. Check whether it finished (for example a remote verify keeps running on the build host) and carry on."

// restartKilledBackground lists the background commands and helpers the chat's journal still shows as running: the
// latest native activity of each is "started". It reads only the saved journal, so it works before any agent exists.
func (api *StreamingAPI) restartKilledBackground(sessionID string) []llmtypes.NativeRecord {
	if api.eventStore == nil {
		return nil
	}
	latest := map[string]llmtypes.NativeRecord{}
	order := []string{}
	// The saved tail is bounded; work started before it is not seen, and is left as it was.
	for _, event := range api.eventStore.LastEventsRaw(sessionID, restartBackgroundScanEvents) {
		if event.Type != string(agentevents.CodingAgentNativeActivity) || event.Data == nil {
			continue
		}
		raw, err := json.Marshal(event.Data.Data)
		if err != nil {
			continue
		}
		var activity struct {
			Record llmtypes.NativeRecord `json:"record"`
		}
		if json.Unmarshal(raw, &activity) != nil {
			continue
		}
		record := activity.Record
		if record.Kind != "helper" && record.Kind != "background_command" {
			continue
		}
		key := record.WorkID
		if key == "" {
			key = record.Provider + "\x00" + record.TaskID + "\x00" + record.TaskRunID
		}
		if key == "\x00\x00" {
			continue
		}
		if _, seen := latest[key]; !seen {
			order = append(order, key)
		}
		latest[key] = record
	}
	var running []llmtypes.NativeRecord
	for _, key := range order {
		record := latest[key]
		if (record.State == "started" || record.State == "progress" || record.State == "running") && record.EndedAt.IsZero() {
			running = append(running, record)
		}
	}
	return running
}

func restartKilledLabel(record llmtypes.NativeRecord) string {
	for _, label := range []string{record.CommandLabel, record.HelperDescription, record.Name, record.HelperType} {
		if label = strings.TrimSpace(label); label != "" {
			if r := []rune(label); len(r) > 120 {
				label = string(r[:120]) + "…"
			}
			return label
		}
	}
	return "a background task"
}

// markRestartKilledStopped closes the rows of the dead work, so the chat's background list does not show them running.
func (api *StreamingAPI) markRestartKilledStopped(sessionID string, killed []llmtypes.NativeRecord) {
	now := time.Now()
	for _, record := range killed {
		record.ID += ":restart-stopped"
		record.State, record.Timestamp, record.EndedAt = "stopped", now, now
		if err := api.saveNativeCodingActivity(sessionID, record); err != nil {
			log.Printf("[RESTART_CONTINUE] session=%s action=skip reason=background_stop_write_failed", sessionID)
		}
	}
}

// queueRestartBackgroundKilledWake queues the one wake for a chat whose background work died with the app. It obeys
// what the interrupted-turn continuation obeys: a person's Stop, a person's message already waiting, the restart
// loop limit and the follow-up chain limit. It returns the queue and whether it handled this chat.
func (api *StreamingAPI) queueRestartBackgroundKilledWake(userID, sessionID string, record *restartContinueRecord, kept []queuedConversationTurn) ([]queuedConversationTurn, bool) {
	if record.QueryID == "" || api.sessionTurnRunning(sessionID) {
		return kept, false
	}
	killed := api.restartKilledBackground(sessionID)
	if len(killed) == 0 {
		return kept, false
	}
	labels := make([]string, 0, len(killed))
	for _, work := range killed {
		labels = append(labels, restartKilledLabel(work))
	}
	// Whatever follows, the dead work is no longer running.
	api.markRestartKilledStopped(sessionID, killed)
	reason := ""
	switch {
	case api.isSessionMarkedStopped(sessionID):
		reason = "stopped_by_person"
	case waitingPersonTurn(kept, sessionID):
		reason = "person_message_queued"
	case len(recentRestartContinues(record.RestartContinues, time.Now())) >= restartContinueLimit:
		reason = "restart_loop_limit"
	case record.FollowUps >= backgroundFollowUpChainLimit:
		reason = "chain_limit"
	}
	for _, turn := range kept {
		if turn.SessionID == sessionID && turn.Request.TriggeredBy == "restart_continue" {
			reason = "continue_already_queued"
		}
	}
	if reason != "" {
		log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=skip reason=%s tasks=%d background_killed=true", sessionID, record.QueryID, reason, len(killed))
		return kept, true
	}
	now := time.Now().UTC()
	record.RestartContinues = append(recentRestartContinues(record.RestartContinues, now), now)
	record.FollowUps++
	backgroundFollowUps.restore(sessionID, record.FollowUps)
	request := record.Request
	request.Query = strings.Replace(restartBackgroundKilledMessage, "%s", strings.Join(labels, "; "), 1)
	kept = append(kept, queuedConversationTurn{ID: "restart-bg:" + record.QueryID, UserID: userID, SessionID: sessionID, Request: request, Principal: record.Principal, CreatedAt: now})
	log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=wake reason=background_killed tasks=%d", sessionID, record.QueryID, len(killed))
	return kept, true
}
