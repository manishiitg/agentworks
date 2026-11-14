package server

import (
	"context"
	"encoding/json"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The SDK stamps the generation's selected account before streaming starts.
// Never substitute the next menu selection, current session handle or a guessed
// server default when a delayed event arrives after an account switch.
func (api *StreamingAPI) observeAccountLimits(session string, event storeevents.Event) {
	if event.Type != "status_line" || event.Data == nil || event.Timestamp.IsZero() || api.eventStore == nil {
		return
	}
	var status struct {
		Provider     string                 `json:"provider"`
		ConnectionID string                 `json:"connection_id"`
		Metadata     map[string]interface{} `json:"metadata"`
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil || json.Unmarshal(raw, &status) != nil || status.ConnectionID == "" || !providerEnabled(status.Provider) {
		return
	}
	windows := (&llmtypes.StatusLine{Metadata: status.Metadata}).RateLimitWindows()
	if len(windows) == 0 {
		return
	}
	owner := api.eventStore.GetSessionOwner(session)
	if owner == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), chatUsageProbeTimeout)
	defer cancel()
	// Re-check the event's account admission; resolving its environment does not
	// run a provider or expose credentials. Revoked accounts cannot update quotas.
	if _, allowed := api.chatUsageProbeAccount(ctx, owner, status.Provider, status.ConnectionID); !allowed {
		return
	}
	var usage llmtypes.AccountUsage
	for _, window := range windows {
		percent := window.UsedPercent
		usage.AddWindow(window.Name, &percent, window.ResetsAt)
	}
	subscriptionLimits.put(status.Provider, status.ConnectionID, "event", event.Timestamp, usage.Windows)
}
