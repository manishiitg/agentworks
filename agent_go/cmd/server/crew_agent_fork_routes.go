package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

// All accept surfaces read this one list. Native bytes are available only via
// an authenticated pair for the named recipient's copy of this same Crew.
func (api *StreamingAPI) handleCrewAgentFork(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project, id, op := mux.Vars(r)["project"], mux.Vars(r)["fork"], mux.Vars(r)["op"]
	fail := func(status int, reason string) { externalError(w, status, "agent_transfer_unavailable", reason) }
	if claims == nil {
		fail(403, "Sign in to use agent forks.")
		return
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		fail(400, "Choose a valid Crew.")
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	if err != nil || !team.On || !team.member(claims.UserID) {
		fail(403, "This Crew's Team is not available to you.")
		return
	}
	now := time.Now().UTC()
	if id == "" && r.Method == http.MethodGet {
		offers, err := listCrewAgentForks(project, claims.UserID, now)
		if err != nil {
			fail(409, "Could not read pending forks.")
			return
		}
		externalJSON(w, map[string]any{"forks": offers})
		return
	}
	if !crewTeamMessagePairAdmitted(project, claims.UserID, r.URL.Query().Get("pair")) {
		fail(403, "Use your own paired computer for this Crew.")
		return
	}
	if id == "" {
		var in struct {
			AgentID   string `json:"agent_id"`
			Recipient string `json:"recipient_id"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in) != nil {
			fail(400, "Choose an agent and teammate.")
			return
		}
		offer, err := newCrewAgentFork(team, claims.UserID, in.Recipient, in.AgentID, now)
		if err != nil {
			fail(403, err.Error())
			return
		}
		if err = saveCrewAgentFork(offer); err != nil {
			fail(409, "Could not save this fork.")
			return
		}
		externalJSON(w, offer)
		return
	}
	offer, err := readCrewAgentFork(project, id)
	if err != nil {
		fail(404, "This fork is not available.")
		return
	}
	if !now.Before(offer.Expires) {
		_ = removeCrewAgentFork(offer)
		fail(410, "This fork expired. Ask its maker to send it again.")
		return
	}
	download := op == "download" || op == "chunks" && r.Method == http.MethodPost
	if err = offer.admit(team, claims.UserID, download, now); err != nil {
		fail(403, err.Error())
		return
	}
	if op == "cancel" {
		if err = cancelCrewAgentFork(team, offer, claims.UserID, now); err != nil {
			fail(409, "Could not remove this fork.")
			return
		}
		publishCrewPanelChanged(project)
		externalJSON(w, map[string]bool{"removed": true})
		return
	}
	if download {
		payload, err := downloadCrewAgentFork(team, offer, claims.UserID, now)
		if err != nil {
			fail(409, "Could not read this fork's conversation.")
			return
		}
		if op == "download" {
			externalJSON(w, payload)
			return
		}
		store, err := crewAgentForkChunkStore(offer)
		if err != nil {
			fail(409, "Could not read this fork's files.")
			return
		}
		serveChunks(w, r, store, func() (map[string]bool, error) {
			allowed := map[string]bool{}
			for _, file := range payload.NativeFiles {
				for _, chunk := range file.Chunks {
					allowed[chunk.ID] = true
				}
			}
			return allowed, nil
		})
		return
	}
	if claims.UserID != offer.Sender || offer.Ready {
		fail(403, "Only the maker can upload this fork before it is ready.")
		return
	}
	store, err := crewAgentForkChunkStore(offer)
	if err != nil {
		fail(409, "Private fork storage is unavailable.")
		return
	}
	if op == "chunks" && r.Method == http.MethodPut {
		count, _, err := receiveChunks(r.Body, store)
		if err != nil {
			fail(400, "The session files did not arrive whole. Retry the upload.")
			return
		}
		externalJSON(w, map[string]int{"stored": count})
		return
	}
	var payload crewAgentForkPayload
	if json.NewDecoder(io.LimitReader(r.Body, crewHandoffHistoryLimit+1)).Decode(&payload) != nil {
		fail(400, "Send this agent's conversation and native file description.")
		return
	}
	if err = validateCrewAgentForkPayload(payload); err != nil {
		fail(400, "This conversation or native session description is invalid.")
		return
	}
	switch op {
	case "plan":
		files := []chunkedFile{}
		for _, file := range payload.NativeFiles {
			files = append(files, file)
		}
		externalJSON(w, map[string]any{"missing": missingChunks(store, files)})
	case "commit":
		if err = stageCrewAgentFork(team, offer, claims.UserID, payload, now); err != nil {
			log.Printf("[AGENT_FORK] project=%s fork=%s event=stage_failed", project, id)
			fail(409, "The session upload is incomplete. Retry it.")
			return
		}
		log.Printf("[AGENT_FORK] project=%s fork=%s event=ready agent=%s", project, id, offer.Agent.ID)
		publishCrewPanelChanged(project)
		externalJSON(w, map[string]any{"ready": true, "transfer_id": id})
	default:
		fail(400, "Choose plan, chunks, commit, download or cancel.")
	}
}
