package server

import (
	"encoding/json"
	"errors"
	"fmt"
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
	fail := func(status int, reason string) {
		log.Printf("[AGENT_FORK] project=%q fork=%q operation=%q event=refused status=%d reason=%q", project, id, op, status, reason)
		externalError(w, status, "agent_fork_unavailable", reason)
	}
	if !crewTeamClaims(claims, project, r.Method != http.MethodGet) {
		fail(403, "Sign in to use agent forks.")
		return
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		fail(400, "Choose a valid project.")
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	if err != nil || !team.On || !team.member(claims.UserID) || ownerOfCrewProjectID(project) != team.OwnerID || !crewTeamAccountActive(team.OwnerID) {
		fail(403, "This project's Team is not available to you.")
		return
	}
	now := time.Now().UTC()
	if id == "" && r.Method == http.MethodGet {
		offers, err := listCrewAgentForks(project, claims.UserID, now)
		if err != nil {
			fail(409, "Could not read pending forks.")
			return
		}
		for i := range offers {
			offers[i] = publicCrewAgentFork(offers[i])
		}
		externalJSON(w, map[string]any{"forks": offers})
		return
	}
	macSource := crewAgentForkRunsOnMac(team, claims.UserID)
	if id == "" {
		var in struct {
			AgentID   string `json:"agent_id"`
			Recipient string `json:"recipient_id"`
			Operation string `json:"operation"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in) != nil {
			fail(400, "Choose an agent and teammate.")
			return
		}
		if in.Operation == "self" {
			if !crewTeamMacPairAdmitted(project, claims.UserID, r.URL.Query().Get("pair")) {
				fail(403, "Use your own paired computer for this project.")
				return
			}
			offer, err := api.selfForkCrewAgent(team, claims.UserID, in.AgentID, now)
			if err != nil {
				var refusal crewAgentCopyRefusal
				if errors.As(err, &refusal) {
					fail(403, refusal.Error())
				} else {
					log.Printf("[AGENT_FORK] project=%s actor=%s event=self_fork_failed reason=source_unavailable", project, claims.UserID)
					fail(409, "Could not read this agent's saved copy. Try again after syncing finishes.")
				}
				return
			}
			externalJSON(w, publicCrewAgentFork(offer))
			return
		}
		if macSource {
			if !crewTeamMacPairAdmitted(project, claims.UserID, r.URL.Query().Get("pair")) {
				fail(403, "Use your own paired computer for this project.")
				return
			}
			// A Mac agent's copy comes from the chat the server already keeps for it once it is shared (#3218).
			offer, err := api.sendCrewAgentFromSyncedCopy(team, claims.UserID, in.Recipient, in.AgentID, now)
			if err != nil {
				var refusal crewAgentCopyRefusal
				if errors.As(err, &refusal) {
					fail(403, refusal.Error())
				} else {
					log.Printf("[AGENT_FORK] project=%s actor=%s event=send_failed reason=source_unavailable", project, claims.UserID)
					fail(409, "Could not read this agent's saved copy. Try again in a minute.")
				}
				return
			}
			externalJSON(w, publicCrewAgentFork(offer))
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
		log.Printf("[AGENT_FORK] project=%s fork=%s source=%s copy=%s event=created", project, offer.ID, offer.Agent.ID, offer.CopyAgentID)
		if !macSource {
			// Native export may be slow. Do not hold the membership lock during
			// capture; recheck current admission when committing the snapshot.
			mu.Unlock()
			payload, captureErr := api.captureCrewAgentFork(r.Context(), claims, offer)
			mu.Lock()
			// Cancellation during export must not resurrect the pending record.
			pending, pendingErr := readCrewAgentFork(project, offer.ID)
			if captureErr == nil {
				captureErr = pendingErr
			}
			if captureErr == nil && (pending.Ready || pending.CopyAgentID != offer.CopyAgentID) {
				captureErr = fmt.Errorf("this fork changed during capture")
			}
			current, readErr := readCrewTeam(project)
			if captureErr == nil {
				captureErr = readErr
			}
			if captureErr == nil {
				captureErr = stageCrewAgentFork(current, offer, claims.UserID, payload, time.Now().UTC())
			}
			if captureErr != nil {
				_ = removeCrewAgentFork(offer)
				log.Printf("[AGENT_FORK] project=%s fork=%s event=capture_failed reason=source_unavailable", project, offer.ID)
				fail(409, "Could not copy this agent's saved conversation. Open it where it runs and try again.")
				return
			}
			offer.Ready = true
			publishCrewPanelChanged(project)
			log.Printf("[AGENT_FORK] project=%s fork=%s event=ready", project, offer.ID)
		}
		externalJSON(w, publicCrewAgentFork(offer))
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
	if op == "accept" {
		copy, err := prepareCrewAgentForkCopy(team, offer, claims.UserID, now)
		if err != nil {
			log.Printf("[AGENT_FORK] project=%s fork=%s actor=%s event=prepare_failed reason=%q", project, id, claims.UserID, crewAgentForkFailureReason(err))
			fail(409, "Could not prepare your own private copy of this agent.")
			return
		}
		log.Printf("[AGENT_FORK] project=%s fork=%s copy=%s actor=%s event=accept_started", project, id, copy.ID, claims.UserID)
		if !macSource {
			mu.Unlock()
			destination, importErr := api.acceptCrewAgentForkOnServer(r, offer)
			mu.Lock()
			current, readErr := readCrewTeam(project)
			if importErr == nil {
				importErr = readErr
			}
			if importErr == nil {
				importErr = completeCrewAgentFork(current, offer, claims.UserID, time.Now().UTC())
			}
			if importErr != nil {
				log.Printf("[AGENT_FORK] project=%s fork=%s actor=%s event=accept_failed reason=%q", project, id, claims.UserID, crewAgentForkFailureReason(importErr))
				var refusal crewAgentForkRefusal
				if errors.As(importErr, &refusal) {
					fail(409, string(refusal))
					return
				}
				fail(409, "Could not finish your private copy. Try accepting again.")
				return
			}
			api.noteForkCopyAnswered(r.Context(), offer, "accepted")
			publishCrewPanelChanged(project)
			externalJSON(w, map[string]any{"accepted": true, "copy": copy, "conversation_key": destination.ConversationKey, "session_id": destination.SessionID})
			return
		}
		externalJSON(w, map[string]any{"copy": copy, "fork_id": id})
		return
	}
	if op == "complete" {
		if err = completeCrewAgentFork(team, offer, claims.UserID, now); err != nil {
			log.Printf("[AGENT_FORK] project=%s fork=%s actor=%s event=refused reason=copy_not_imported", project, id, claims.UserID)
			fail(409, "Finish importing your own private copy before accepting this fork.")
			return
		}
		api.noteForkCopyAnswered(r.Context(), offer, "accepted")
		publishCrewPanelChanged(project)
		externalJSON(w, map[string]bool{"accepted": true})
		return
	}
	if op == "cancel" {
		if err = cancelCrewAgentFork(team, offer, claims.UserID, now); err != nil {
			fail(409, "Could not remove this fork.")
			return
		}
		if claims.UserID == offer.Recipient {
			api.noteForkCopyAnswered(r.Context(), offer, "declined")
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
			log.Printf("[AGENT_FORK] project=%s fork=%s actor=%s event=downloaded", project, id, claims.UserID)
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
	fail(400, "Choose download, accept, complete or cancel.")
}
