package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// One-off on a Mac (#4078): a project on a Mac has no functions, because functions are online only and a Mac project
// can no longer move online. Each Crew's functions.json is copied beside itself first, then rewritten with only the
// records that are caller bindings (input_mapping), not functions. Their tabs stay and are ordinary agents on a Mac
// (crewFunctionTabKey). A done marker is written last. Remove this file once every Mac has started on this version.

const (
	crewFunctionMacRemovalMarker = "_system/mac-functions-removed.done"
	crewFunctionMacRemovalBackup = ".before-mac-functions-removed"
)

func removeMacCrewFunctions() error {
	if !IsLocalMode() {
		return nil
	}
	docs := fsutil.WorkspaceDocsRoot()
	marker := filepath.Join(docs, filepath.FromSlash(crewFunctionMacRemovalMarker))
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	var failures []error
	records, err := defaultProjectOwners().All()
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	for id, rec := range records {
		if rec.Product != crewProfileID {
			continue
		}
		file := filepath.Join(docs, filepath.FromSlash(projectStorageRoot(rec)), crewFunctionsFileName)
		removed, err := removeFunctionsFromFile(file)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", file, err))
			continue
		}
		if len(removed) > 0 {
			log.Printf("[CREW_FUNCTIONS] event=mac_functions_removed project=%s functions=%q backup=%s", id, removed, crewFunctionsFileName+crewFunctionMacRemovalBackup)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err // no marker: the next start tries again
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// removeFunctionsFromFile keeps only caller bindings and returns the names of the functions it removed.
func removeFunctionsFromFile(file string) ([]string, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	items, _ := document["functions"].([]any)
	kept, removed := []any{}, []string{}
	for _, item := range items {
		record, _ := item.(map[string]any)
		if record != nil && record["input_mapping"] != nil {
			kept = append(kept, item)
			continue
		}
		name, _ := record["name"].(string)
		removed = append(removed, name)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if err := os.WriteFile(file+crewFunctionMacRemovalBackup, raw, 0o600); err != nil {
		return nil, err
	}
	document["functions"] = kept
	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return removed, os.WriteFile(file, out, 0o644)
}
