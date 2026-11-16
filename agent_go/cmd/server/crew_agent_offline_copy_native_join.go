package server

import (
	"context"
	"log"
	"sync"
	"time"
)

// Join and producer hooks only. Discovery and ordinary asks never provision.
var crewOfflineNativePreparing sync.Map

func (api *StreamingAPI) queueCrewOfflineNativeHome(ctx context.Context, t crewTeam, a crewTeamRegisteredAgent) {
	if a.Kind != "terminal" || a.Terminal == nil {
		return
	}
	key := t.ProjectID + "/" + a.ID
	if _, busy := crewOfflineNativePreparing.LoadOrStore(key, true); busy {
		return
	}
	go func() {
		defer crewOfflineNativePreparing.Delete(key)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		current, err := readCrewTeam(t.ProjectID)
		row, found := current.Agents[a.ID]
		reason := "source_changed"
		if err == nil && found && row.OwnerID == a.OwnerID && row.ChatKey == a.ChatKey && row.Terminal != nil && row.NamedAgentID == a.NamedAgentID && row.Terminal.Provider == a.Terminal.Provider && crewOfflineSourceOwner(current, GetUserIDFromContext(ctx), row) == nil {
			_, err = currentCrewTeamTerminalWithPair(ctx, current, row, func() (string, bool) { return crewOfflineMakerPair(current, row) })
			if err == nil {
				_, err = api.ensureCrewTeamOnlineHome(ctx, current.ProjectID, "native_capture", row.ID)
				reason = "home_unavailable"
				if err == nil {
					log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=native_home_ready", current.ProjectID, row.ID, row.ChatKey)
					api.refreshCrewOfflineCopy(current.ProjectID, row.ID)
					return
				}
			}
		}
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=refused reason=%s", t.ProjectID, a.ID, a.ChatKey, reason)
	}()
}
