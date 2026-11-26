package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"path"
)

// A project on a Mac has no functions (#4078): functions are online only and a Mac project can no longer move
// online. A Crew's functions are rows of its functions.json automation record, so on every Mac start, with the
// automation-records import, each Crew's record keeps only its caller bindings (input_mapping), after its whole
// content goes to the private automation-records backup. Once removed there is nothing left to do. Their tabs stay
// and are ordinary agents on a Mac (crewFunctionTabKey). Remove this once every Mac has started on this version.
func removeMacCrewFunctions(ctx context.Context, root string) {
	if !IsLocalMode() {
		return
	}
	file := path.Join(root, crewFunctionsFileName)
	raw, found, err := readFileFromWorkspace(ctx, file)
	if err != nil || !found {
		if err != nil {
			log.Printf("[CREW_FUNCTIONS] event=mac_functions_removal_failed root=%s error=%v", root, err)
		}
		return
	}
	kept, removed, err := withoutFunctions(raw)
	if err != nil || len(removed) == 0 {
		if err != nil {
			log.Printf("[CREW_FUNCTIONS] event=mac_functions_removal_failed root=%s error=%v", root, err)
		}
		return
	}
	if err := backupAutomationFile(file, "", raw); err != nil {
		log.Printf("[CREW_FUNCTIONS] event=mac_functions_removal_failed root=%s reason=backup error=%v", root, err)
		return
	}
	if err := writeFileToWorkspace(ctx, file, kept); err != nil {
		log.Printf("[CREW_FUNCTIONS] event=mac_functions_removal_failed root=%s error=%v", root, err)
		return
	}
	log.Printf("[CREW_FUNCTIONS] event=mac_functions_removed root=%s functions=%q", root, removed)
}

// withoutFunctions keeps only caller bindings (records with input_mapping) and names the functions it dropped.
func withoutFunctions(raw string) (string, []string, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return "", nil, err
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
	document["functions"] = kept
	out, err := json.MarshalIndent(document, "", "  ")
	return string(out), removed, err
}
