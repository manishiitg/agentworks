package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

func crewSyncOn(id string) error {
	b, err := crewroot.ReadSync(id)
	if err != nil {
		return err
	}
	if b != nil && b.Mode == "off" {
		return fmt.Errorf("Cloud is off for this project; both copies are kept. Turn it on before syncing")
	}
	return nil
}

// Turning off never waits for a transfer lock, an idle agent or the peer.
// Keep the pair: it also describes where this project's files live.
func (api *StreamingAPI) desktopCrewSyncOff(w http.ResponseWriter, r *http.Request, roots crewroot.Roots, connection desktopConnection, action, mode string) bool {
	b, err := crewroot.ReadSync(roots.ID)
	if err != nil || b == nil {
		return false
	}
	if b.Side != "local" || b.ConnectionID != connection.ID {
		return false
	}
	off := action == "mode" && mode == "off"
	if !off && b.Mode != "off" {
		return false
	}
	if !off && action != "status" && action != "mode" {
		return false
	}
	if off {
		b, err = crewroot.SetSyncMode(roots.ID, b.PairID, "off")
		if err != nil {
			externalError(w, 409, "sync_unavailable", err.Error())
			return true
		}
		setCrewAutoStatus(roots.ID, "off", "Cloud is off. Both copies are kept.")
	}
	// A missed notification is retried only on the next explicit contact. No
	// queue or new worker: status and On always reconcile Off first.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	remote, peerErr := desktopCrewSyncCall(ctx, GetUserFromContext(r.Context()).UserID, &connection, b.PeerProjectID, crewSyncRequest{Action: "mode", PairID: b.PairID, Mode: "off"})
	cancel()
	if !off && action == "mode" {
		return false
	}
	notice := "Cloud is off. Both copies are kept. "
	copied := func(value string) string {
		if value == "" {
			return "time unavailable"
		}
		return value
	}
	notice += "Last completed file sync on this Mac: " + copied(b.LastCompletedAt) + ". "
	if peerErr != nil {
		notice += "The server could not be told; its copy may still be online."
	} else if remote.Binding != nil {
		notice += "Last completed file sync on the server: " + copied(remote.Binding.LastCompletedAt) + "."
	}
	notice += " A transfer already in progress may finish its current write."
	log.Printf("[CREW_SYNC] project=%s pair=%s action=mode mode=off peer_notified=%t", roots.ID, b.PairID, peerErr == nil)
	externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: b, Auto: crewAutoStatus(roots.ID), ConversationNotice: notice})
	return true
}
