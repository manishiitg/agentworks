package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Relay calls use the existing function trigger and scheduler run store. The
// stable run ID remains pollable after a server restart, unlike the external
// function-call conversation handle.
type relayRunRequest struct {
	Function       string                 `json:"function"`
	Version        string                 `json:"version,omitempty"`
	Input          map[string]interface{} `json:"input"`
	IdempotencyKey string                 `json:"idempotency_key"`
}

func (api *StreamingAPI) relayForRunRequest(w http.ResponseWriter, r *http.Request) (string, *WorkflowManifest, *UserClaims, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || !userAllowedWorkflowID(claims, mux.Vars(r)["id"]) || (claims.AccessToken != nil && (!claims.AccessToken.Allows("runs:execute") || !claims.AccessToken.AllowsWorkflow(mux.Vars(r)["id"]))) {
		http.Error(w, "Relay not found", http.StatusNotFound)
		return "", nil, nil, false
	}
	workspace, manifest, err := findWorkflowManifestByID(r.Context(), mux.Vars(r)["id"])
	if err != nil || manifest == nil || manifest.Kind != "relay" {
		http.Error(w, "Relay not found", http.StatusNotFound)
		return "", nil, nil, false
	}
	access := workflowAccessForManifest(claims, manifest)
	if access != WorkflowAccessOwner && access != WorkflowAccessWrite {
		http.Error(w, "Relay not found", http.StatusNotFound)
		return "", nil, nil, false
	}
	return workspace, manifest, claims, true
}

func relayCallPayloadMatches(raw string, function string, input map[string]interface{}, userID string) bool {
	var saved WorkflowWebhookDelivery
	if json.Unmarshal([]byte(raw), &saved) != nil {
		return false
	}
	var payload struct {
		Function string                 `json:"function"`
		Args     map[string]interface{} `json:"args"`
		Caller   string                 `json:"relay_caller"`
	}
	if json.Unmarshal(saved.Payload, &payload) != nil || payload.Function != function || payload.Caller != userID {
		return false
	}
	want, _ := json.Marshal(map[string]interface{}{"INPUT": input})
	got, _ := json.Marshal(payload.Args)
	return bytes.Equal(want, got)
}

func (api *StreamingAPI) handleStartRelayRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspace, manifest, claims, ok := api.relayForRunRequest(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request relayRunRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid Relay request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "expected one JSON object", http.StatusBadRequest)
		return
	}
	request.Function = strings.TrimSpace(request.Function)
	request.Version = strings.TrimSpace(request.Version)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.Function == "" || request.Input == nil || len(request.IdempotencyKey) == 0 || len(request.IdempotencyKey) > 128 || strings.ContainsAny(request.IdempotencyKey, "\x00\r\n") {
		http.Error(w, "function, input object and idempotency_key (1-128 characters) are required", http.StatusBadRequest)
		return
	}
	caller := triggerCaller{Type: triggerCallerUser, ID: claims.UserID}
	args := map[string]interface{}{"INPUT": request.Input}
	deliveryID := claims.UserID + "\x00" + request.IdempotencyKey
	// Search old and published bindings before choosing today's active version.
	// An idempotency key must still find its original run after a republish.
	candidates := []struct {
		workspace string
		manifest  *WorkflowManifest
		version   string
	}{{workspace, manifest, ""}}
	if releases, listErr := listRelayReleases(r.Context(), workspace); listErr == nil {
		for _, release := range releases {
			releaseWorkspace := relayReleaseWorkspace(workspace, release.Version)
			published, found, readErr := ReadWorkflowManifest(r.Context(), releaseWorkspace)
			if readErr == nil && found {
				candidates = append(candidates, struct {
					workspace string
					manifest  *WorkflowManifest
					version   string
				}{releaseWorkspace, published, release.Version})
			}
		}
	}
	for _, candidate := range candidates {
		for i := range candidate.manifest.Schedules {
			savedTrigger := &candidate.manifest.Schedules[i]
			if !savedTrigger.IsFunctionTrigger() {
				continue
			}
			runID := webhookDeliveryRunID(manifest.ID, savedTrigger.ID, deliveryID)
			run, err := api.scheduler.existingWebhookRun(r.Context(), runID)
			if err != nil || run.ScopeID != candidate.workspace {
				continue
			}
			content, exists, readErr := readFileFromWorkspace(r.Context(), webhookInputPath(candidate.workspace, runID))
			if readErr != nil || !exists {
				http.Error(w, "existing Relay request is temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			if !relayCallPayloadMatches(content, request.Function, request.Input, claims.UserID) {
				http.Error(w, "idempotency_key was already used with different input", http.StatusConflict)
				return
			}
			externalJSON(w, map[string]interface{}{"run_id": runID, "status": string(run.State), "version": candidate.version, "duplicate": true, "poll_url": "/api/relays/" + manifest.ID + "/runs/" + runID})
			return
		}
	}
	release, releaseWorkspace, err := resolveRelayRelease(r.Context(), workspace, request.Version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	published, found, err := ReadWorkflowManifest(r.Context(), releaseWorkspace)
	if err != nil || !found {
		http.Error(w, "published Relay is unavailable", http.StatusServiceUnavailable)
		return
	}
	sched, err := findWorkflowFunctionTrigger(published, request.Function)
	if err != nil || !workflowFunctionCallerAllowed(sched.Function, caller) {
		http.Error(w, "Relay function not found", http.StatusNotFound)
		return
	}
	if _, _, err := workflowFunctionArgs(*sched, args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, delivery, err := api.scheduler.dispatchWorkflowFunction(r.Context(), workflowFunctionCall{WorkflowID: manifest.ID, Function: request.Function, RelayVersion: release.Version, Caller: caller, DeliveryID: deliveryID, Args: args, Payload: map[string]interface{}{"relay_caller": claims.UserID}})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrWebhookConcurrencyLimit) || errors.Is(err, ErrWebhookRunStoreMissing) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), status)
		return
	}
	if delivery.Duplicate {
		content, exists, readErr := readFileFromWorkspace(r.Context(), webhookInputPath(releaseWorkspace, delivery.RunID))
		if readErr != nil || !exists || !relayCallPayloadMatches(content, request.Function, request.Input, claims.UserID) {
			http.Error(w, "idempotency_key was already used with different input", http.StatusConflict)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"run_id": delivery.RunID, "status": delivery.Status, "version": release.Version, "duplicate": delivery.Duplicate, "poll_url": "/api/relays/" + manifest.ID + "/runs/" + delivery.RunID})
}

func (api *StreamingAPI) handleGetRelayRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspace, manifest, claims, ok := api.relayForRunRequest(w, r)
	if !ok {
		return
	}
	runID := mux.Vars(r)["run"]
	run, err := api.scheduler.existingWebhookRun(r.Context(), runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if run.ScopeType != "workflow" || run.TriggerSource != "webhook" {
		http.NotFound(w, r)
		return
	}
	content, exists, err := readFileFromWorkspace(r.Context(), webhookInputPath(run.ScopeID, runID))
	if err != nil || !exists {
		http.NotFound(w, r)
		return
	}
	var delivery WorkflowWebhookDelivery
	var payload struct {
		Caller  string `json:"relay_caller"`
		Version string `json:"relay_version"`
	}
	if json.Unmarshal([]byte(content), &delivery) != nil || json.Unmarshal(delivery.Payload, &payload) != nil || payload.Caller != claims.UserID {
		http.NotFound(w, r)
		return
	}
	if payload.Version == "" {
		if run.ScopeID != workspace {
			http.NotFound(w, r)
			return
		}
	} else {
		_, releaseWorkspace, releaseErr := readRelayRelease(r.Context(), workspace, payload.Version)
		if releaseErr != nil || run.ScopeID != releaseWorkspace {
			http.NotFound(w, r)
			return
		}
		manifest, _, err = ReadWorkflowManifest(r.Context(), releaseWorkspace)
		if err != nil || manifest == nil {
			http.Error(w, "published Relay is unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	result, err := readWebhookRunResult(run.ScopeID, run)
	if err != nil {
		http.Error(w, "run result temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	applyRelayResult(manifest, &result, run.ScopeID, run)
	result.Version = payload.Version
	if err := signWebhookRunArtifacts(&result, run); err != nil {
		http.Error(w, "run artifacts temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	externalJSON(w, result)
}
