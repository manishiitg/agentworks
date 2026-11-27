package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// handleSubmitHumanFeedback handles human feedback submission
func (api *StreamingAPI) handleSubmitHumanFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req HumanFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.UniqueID == "" {
		http.Error(w, "unique_id is required", http.StatusBadRequest)
		return
	}

	if req.Response == "" {
		http.Error(w, "response is required", http.StatusBadRequest)
		return
	}

	// Get human feedback store and submit response. Only a question the
	// caller may see can be answered; anything else reads as not found.
	feedbackStore := virtualtools.GetHumanFeedbackStore()
	allowed := false
	for _, pending := range feedbackStore.ListPending(time.Now()) {
		if pending.UniqueID == req.UniqueID {
			allowed = api.humanFeedbackVisibleTo(r.Context(), pending)
			break
		}
	}
	if !allowed {
		http.Error(w, "feedback request not found", http.StatusNotFound)
		return
	}
	if err := feedbackStore.SubmitResponse(req.UniqueID, req.Response); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Responses may contain OTPs or other private input; never include the value
	// in application logs.
	log.Printf("[HUMAN_FEEDBACK] Submitted response for unique_id %s", req.UniqueID)

	response := HumanFeedbackResponse{
		UniqueID: req.UniqueID,
		Status:   "success",
		Message:  "Human feedback submitted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleListPendingHumanFeedback exposes the authoritative in-memory queue so
// urgent prompts are visible even when their background session/tree is not
// currently mounted in the frontend.
func (api *StreamingAPI) handleListPendingHumanFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"requests": api.visibleHumanFeedback(r.Context(), virtualtools.GetHumanFeedbackStore().ListPending(time.Now())),
	})
}
