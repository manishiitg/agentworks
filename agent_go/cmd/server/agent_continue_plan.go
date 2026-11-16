package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// An agent that ends a normal turn while its own plan still has open steps has usually forgotten the rest.
// The chat gets one more turn per distinct open plan: continue, or say in one line what it is waiting for.
// Nothing polls: this runs at the end of a turn, shares the follow-up chain limit with the other automatic
// continuations, and the continuation turn only repeats if the plan changes and is still open.
const planOpenContinueMessage = "[AGENT_CONTINUE] Your plan still has steps that are not done:\n%s\n\nContinue with them. If you are waiting for a person, an answer or a command, say in one line what you are waiting for, then stop."

var openPlanContinued = struct {
	mu   sync.Mutex
	seen map[string]string
}{seen: map[string]string{}}

type openPlanItem struct{ text, status string }

// latestTurnOpenPlan returns the open steps of the newest plan of the chat's last turn, but only when that turn
// ended normally. A Stop, an error or a cancel ends it differently and is never continued.
func (api *StreamingAPI) latestTurnOpenPlan(sessionID string) (open []openPlanItem, ended bool) {
	if api.eventStore == nil {
		return nil, false
	}
	rows := api.eventStore.LastEventsRaw(sessionID, 300)
	completed := false
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		switch {
		case row.Type == "user_message":
			return nil, false // no plan in the turn that just ended
		case row.Type == "unified_completion":
			// A turn saves unified_completion and then conversation_end; every end row of the newest turn counts.
			if !completionWasNormal(row) {
				return nil, false
			}
			completed = true
		case storeevents.IsTurnEndType(row.Type) && row.Type != "conversation_end":
			return nil, false // error, cancel or Stop
		case completed && row.Type == "coding_agent_plan":
			return openPlanItems(row), true
		}
	}
	return nil, false
}

func completionWasNormal(row storeevents.Event) bool {
	if row.Data == nil {
		return true
	}
	raw, err := json.Marshal(row.Data.Data)
	if err != nil {
		return false
	}
	var payload struct {
		Status   string                 `json:"status"`
		Metadata map[string]interface{} `json:"metadata"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	return (payload.Status == "" || payload.Status == "completed") && payload.Metadata["source"] != "app_restart"
}

func openPlanItems(row storeevents.Event) []openPlanItem {
	if row.Data == nil {
		return nil
	}
	raw, err := json.Marshal(row.Data.Data)
	if err != nil {
		return nil
	}
	var outer struct {
		Items []struct{ Text, Status string } `json:"items"`
		Data  *struct {
			Items []struct{ Text, Status string } `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &outer) != nil {
		return nil
	}
	items := outer.Items
	if outer.Data != nil && len(outer.Data.Items) > 0 {
		items = outer.Data.Items
	}
	var open []openPlanItem
	for _, item := range items {
		if item.Status != "completed" && strings.TrimSpace(item.Text) != "" {
			open = append(open, openPlanItem{item.Text, item.Status})
		}
	}
	return open
}

func (api *StreamingAPI) continueOpenPlan(sessionID string) {
	if api.isSessionMarkedStopped(sessionID) || chatTerminalTurnRefusal(sessionID) != "" || api.isSessionBusyForAutoNotification(sessionID) {
		return
	}
	if api.continueAgentBridgeTask(sessionID) {
		return
	}
	open, ended := api.latestTurnOpenPlan(sessionID)
	if !ended || len(open) == 0 {
		log.Printf("[AGENT_CONTINUE] session=%s action=skip reason=no_open_plan ended=%t tail=%s", sessionID, ended, api.eventTail(sessionID, 8))
		return
	}
	list := openPlanList(open)
	if !rememberOpenPlanContinued(sessionID, list) {
		log.Printf("[AGENT_CONTINUE] session=%s action=skip reason=same_open_plan open=%d", sessionID, len(open))
		return
	}
	if reason := backgroundFollowUps.skipReason(sessionID, false); reason != "" {
		log.Printf("[AGENT_CONTINUE] session=%s action=skip reason=%s open=%d", sessionID, reason, len(open))
		return
	}
	message := fmt.Sprintf(planOpenContinueMessage, list)
	presentation := storeevents.BackgroundWakePresentation{Kind: "plan_open"}
	if !api.executeSyntheticTurnWithContext(sessionID, message, "", func(err error) {
		log.Printf("[AGENT_CONTINUE] session=%s action=%s reason=plan_open", sessionID, map[bool]string{true: "failed", false: "done"}[err != nil])
	}, nil, presentation) {
		log.Printf("[AGENT_CONTINUE] session=%s action=skip reason=turn_not_started open=%d", sessionID, len(open))
		return
	}
	api.noteAutomaticFollowUpStarted(sessionID)
	log.Printf("[AGENT_CONTINUE] session=%s action=started reason=plan_open open=%d", sessionID, len(open))
}

func openPlanList(open []openPlanItem) string {
	lines := make([]string, 0, len(open))
	for _, item := range open {
		lines = append(lines, "- "+item.text)
	}
	return strings.Join(lines, "\n")
}

// rememberOpenPlanContinued returns false when this chat was already continued for exactly this open plan.
func rememberOpenPlanContinued(sessionID, list string) bool {
	openPlanContinued.mu.Lock()
	defer openPlanContinued.mu.Unlock()
	if openPlanContinued.seen[sessionID] == list {
		return false
	}
	openPlanContinued.seen[sessionID] = list
	return true
}

// eventTail names the newest event types of a chat, so a skipped check shows what it saw.
func (api *StreamingAPI) eventTail(sessionID string, n int) string {
	rows := api.eventStore.LastEventsRaw(sessionID, n)
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Type)
	}
	return strings.Join(names, ",")
}
