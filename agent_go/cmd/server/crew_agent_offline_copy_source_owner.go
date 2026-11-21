package server

import (
	"fmt"
	"log"
)

// Upload/handback authority is ownership of this current source row, not the
// audience of a home Main. The canonical gate also revokes removed makers.
func crewOfflineSourceOwner(t crewTeam, person string, a crewTeamRegisteredAgent) error {
	if person == "" || a.OwnerID != person || a.ID == "" || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return fmt.Errorf("source owner unavailable")
	}
	current, ok := t.Agents[a.ID]
	if !ok || current.OwnerID != person || current.ChatKey != a.ChatKey {
		return fmt.Errorf("source row changed")
	}
	if !crewTeamSourceRowAdmits(t, person, current) {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s actor=%s event=refused reason=maker_removed", t.ProjectID, a.ID, person)
		return fmt.Errorf("this source maker is no longer saved on the Team")
	}
	return nil
}

// Read the SAME saved pair only after owned-row admission. Generic Team/Mac
// discovery keeps its existing Team-on rule; this is not a new relay offer.
func crewOfflineMakerPair(t crewTeam, a crewTeamRegisteredAgent) (string, bool) {
	if crewOfflineSourceOwner(t, a.OwnerID, a) != nil {
		return "", false
	}
	if a.OwnerID == t.OwnerID {
		return crewMacPairFor(a.OwnerID, t.ProjectID)
	}
	for _, p := range t.People {
		if p.UserID == a.OwnerID && !p.Invited && p.Where == "mac" && p.MacPair != "" {
			return p.MacPair, true
		}
	}
	return "", false
}
