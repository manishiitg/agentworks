package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Rename and delete for a Crew's workflow. Both run inside handleCrewExecutables, so they use the
// same Crew owner, account and "owner is online on a Mac" checks as creating a workflow.

var errCrewWorkflowNotFound = errors.New("that workflow was not found in this project")

// crewWorkflowByID returns this Crew's own Python workflow with that ID. A workflow of another
// Crew is never returned, so one Crew's owner cannot touch another Crew's workflow.
func crewWorkflowByID(crewID, id string) (*crewExecutableBinding, error) {
	binding, err := findCrewExecutableByID(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.Kind != "script" || binding.CrewID != crewID {
		return nil, errCrewWorkflowNotFound
	}
	return binding, nil
}

// renameCrewWorkflow changes the name stored in the workflow's script.json. Its ID, files and runs stay as they are.
func (api *StreamingAPI) renameCrewWorkflow(w http.ResponseWriter, r *http.Request, crewID string) {
	var input struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "Enter a workflow name.", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 120 {
		http.Error(w, "Enter a workflow name (up to 120 characters).", http.StatusBadRequest)
		return
	}
	binding, err := crewWorkflowByID(crewID, input.ID)
	if errors.Is(err, errCrewWorkflowNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Could not read this workflow.", http.StatusInternalServerError)
		return
	}
	crewWorkflowNamesMu.Lock()
	defer crewWorkflowNamesMu.Unlock()
	if taken, nameErr := crewWorkflowNameTaken(r.Context(), binding.CrewID, name, binding.ID); nameErr != nil {
		http.Error(w, "Could not check the name. Try again.", http.StatusInternalServerError)
		return
	} else if taken {
		http.Error(w, errCrewWorkflowNameTaken.Error(), http.StatusConflict)
		return
	}
	definition, err := readCrewScript(r.Context(), binding.Root)
	if err != nil {
		http.Error(w, "Could not read this workflow.", http.StatusInternalServerError)
		return
	}
	definition.Name = name
	if err := saveCrewScriptDefinition(r.Context(), binding, definition, nil); err != nil {
		http.Error(w, "Could not save the new name: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if api.scheduler != nil {
		api.scheduler.InvalidateWorkflowManifestCache()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": binding.ID, "name": name})
}

// removeCrewWorkflowFiles removes everything a workflow keeps on disk: its code, its runs and data, its saved
// settings (schedules, web addresses, functions) and its definition folder. Each folder must sit inside the
// Crew's own automation folder and end with this workflow's ID. The Crew's shared database is not touched.
func removeCrewWorkflowFiles(binding crewExecutableBinding) error {
	layout, found, err := crewroot.LookupExecutable(getWorkspaceDocsAbsPath(), binding.Root)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("this workflow's folders are unavailable")
	}
	folders := []string{layout.Code, layout.Runtime, layout.Settings, layout.Definitions}
	for _, folder := range folders {
		if folder == "" || folder == layout.Crew.Automation || !crewroot.Within(layout.Crew.Automation, folder) || filepath.Base(folder) != binding.ID {
			return fmt.Errorf("refusing to remove a folder that is not this workflow's own")
		}
	}
	forgetAutomationRecordsUnder(context.Background(), layout.Settings)
	var failures []error
	for _, folder := range folders {
		if err := os.RemoveAll(folder); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// deleteCrewWorkflow removes the workflow, its files and its run history. The Crew then has no workflow, so a new
// one can be made. The files go before the saved record, so a failed delete can be tried again.
func (api *StreamingAPI) deleteCrewWorkflow(w http.ResponseWriter, r *http.Request, crewID string) {
	binding, err := crewWorkflowByID(crewID, r.URL.Query().Get("id"))
	if errors.Is(err, errCrewWorkflowNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Could not read this workflow.", http.StatusInternalServerError)
		return
	}
	// Work out the run history to remove before the folders are gone.
	historyRoot, err := scheduleHistoryRemovalScope(binding.Root)
	if err != nil {
		http.Error(w, "Could not find this workflow's run history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := removeCrewWorkflowFiles(*binding); err != nil {
		log.Printf("[CREW_WORKFLOW_DELETE] crew=%s workflow=%s files_failed: %v", crewID, binding.ID, err)
		http.Error(w, "Could not remove all of this workflow's files. Try again.", http.StatusInternalServerError)
		return
	}
	if err := purgeRemovedScheduleHistory(r.Context(), historyRoot); err != nil {
		http.Error(w, "The files were removed, but the run history cleanup failed. Try again.", http.StatusInternalServerError)
		return
	}
	noteScheduleSummaryChange()

	// Stop what the deleted workflow still holds in memory.
	deleteWorkflowRuntime(binding.ID)
	api.workflowOrchestratorContextMux.Lock()
	cancel := api.workflowOrchestratorContexts[binding.ID]
	delete(api.workflowOrchestratorContexts, binding.ID)
	api.workflowOrchestratorContextMux.Unlock()
	if cancel != nil {
		cancel()
	}
	api.workflowStepIDMux.Lock()
	delete(api.workflowStepIDs, binding.ID)
	api.workflowStepIDMux.Unlock()
	api.workflowObjectiveMux.Lock()
	delete(api.workflowObjectives, binding.ID)
	api.workflowObjectiveMux.Unlock()

	if err := removeCrewExecutableBinding(crewID, binding.ID); err != nil {
		http.Error(w, "The files were removed, but the workflow record could not be removed. Try again.", http.StatusInternalServerError)
		return
	}
	invalidateWorkflowManifestIndex()
	if api.scheduler != nil {
		api.scheduler.InvalidateWorkflowManifestCache()
	}
	log.Printf("[CREW_WORKFLOW_DELETE] crew=%s workflow=%s removed", crewID, binding.ID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "id": binding.ID})
}
