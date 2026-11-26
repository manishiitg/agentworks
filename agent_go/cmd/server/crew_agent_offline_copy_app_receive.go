package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
)

func crewOfflineRequestOwner(r *http.Request, t crewTeam, agent string, b crewOfflineBinding, name string) error {
	if b.Provider == "agentworks" {
		return crewOfflineAppOwner(r, t, agent, b)
	}
	return crewOfflineTerminalOwner(r.Context(), t, agent, b, name)
}

func crewOfflineRequestSource(r *http.Request, t crewTeam, agent string, b crewOfflineBinding, name string) error {
	if b.Provider != "agentworks" {
		return crewOfflineSource(r.Context(), t, agent, b, name)
	}
	if err := crewOfflineAppOwner(r, t, agent, b); err != nil {
		return err
	}
	a := t.Agents[agent]
	wanted, err := crewOfflineAppWanted(t, a, b)
	if err != nil || !wanted {
		return fmt.Errorf("source app copy not requested")
	}
	return nil
}

// Ownership/pair proof is independent of whether online intent exists yet.
func crewOfflineAppOwner(r *http.Request, t crewTeam, agent string, b crewOfflineBinding) error {
	claims := GetUserFromContext(r.Context())
	a, ok := t.Agents[agent]
	// The normal desktop sign-in + current exact Mac pair can speak for its
	// own source. No model/scoped token can claim this path by choosing a key.
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" || !ok || a.OwnerID != claims.UserID || a.Kind == "terminal" || crewOfflineSourceOwner(t, claims.UserID, a) != nil || !crewTeamAccountActive(claims.UserID) || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) || !b.valid() {
		return fmt.Errorf("source app owner unavailable")
	}
	pair, paired := crewOfflineMakerPair(t, a)
	sent := r.Header.Get(crewMacPairHeader)
	if !paired || sent == "" || subtle.ConstantTimeCompare([]byte(pair), []byte(sent)) != 1 {
		return fmt.Errorf("source app pair changed")
	}
	return nil
}

// Sharing with the Team turns on automatic history sync; a joined terminal
// session syncs because joining is its capture choice. Only me agents keep no
// online copy (crew_agent_synced_chat_cleanup.go deletes what is no longer
// wanted). This never creates a home, enables Team or uploads other chats.
func crewOfflineAppWanted(t crewTeam, a crewTeamRegisteredAgent, _ ...crewOfflineBinding) (bool, error) {
	if a.Kind == "terminal" && a.Terminal != nil {
		return true, nil
	}
	return t.On && a.InTeam, nil
}

func (api *StreamingAPI) handleCrewOfflineAppStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		crewOfflineBinding
		SourceTab string `json:"source_tab"`
		// Reset drops this source's copy first (an Edit and resend on the Mac, #3707).
		Reset bool `json:"reset,omitempty"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil || in.Provider != "agentworks" || !in.crewOfflineBinding.valid() {
		externalError(w, 400, "invalid_source", "Choose a current source app conversation.")
		return
	}
	project := mux.Vars(r)["project"]
	t, err := readCrewTeam(project)
	claims := GetUserFromContext(r.Context())
	if err != nil || claims == nil {
		externalError(w, 403, "source_unavailable", "This source project is unavailable.")
		return
	}
	var source crewTeamRegisteredAgent
	for _, a := range t.Agents {
		if a.OwnerID == claims.UserID && a.Tab == in.SourceTab && a.Kind != "terminal" {
			if source.ID != "" {
				externalError(w, 409, "source_changed", "This source agent is ambiguous.")
				return
			}
			source = a
		}
	}
	if source.ID == "" {
		externalJSON(w, map[string]bool{"wanted": false})
		return
	}
	wanted, err := crewOfflineAppWanted(t, source, in.crewOfflineBinding)
	if err != nil {
		externalError(w, 503, "copy_unavailable", "Copy status is unavailable.")
		return
	}
	// Even a 'not wanted' response must not confirm a foreign source's identity.
	if !wanted {
		externalJSON(w, map[string]bool{"wanted": false})
		return
	}
	if crewOfflineRequestSource(r, t, source.ID, in.crewOfflineBinding, "") != nil {
		externalError(w, 403, "source_unavailable", "The source app or pair changed.")
		return
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		externalError(w, 503, "copy_unavailable", "Copy storage is unavailable.")
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	current, err := readCrewTeam(project)
	if err != nil || crewOfflineRequestSource(r, current, source.ID, in.crewOfflineBinding, "") != nil {
		externalError(w, 403, "source_changed", "The source app or sharing changed.")
		return
	}
	dir, err = crewOfflineWritableDirectory(project, source.ID)
	if err == nil && in.Reset {
		err = resetCrewOfflineCopy(project, source.ID, dir, in.crewOfflineBinding)
	}
	var h crewOfflineHistory
	if err == nil {
		h, err = readCrewOfflineHistory(dir, in.crewOfflineBinding)
	}
	if err != nil {
		externalError(w, 409, "copy_unavailable", "This source history binding changed.")
		return
	}
	externalJSON(w, map[string]any{"wanted": true, "agent": source.ID, "revision": h.Revision, "offset": h.Offset, "tail_hash": h.TailHash, "reset": in.Reset})
}
