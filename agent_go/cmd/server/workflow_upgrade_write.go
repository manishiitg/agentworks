package server

import (
	"context"
	"encoding/json"
	"fmt"
)

// Upgrade callers hold the existing mutation lock and have rechecked Builder
// authority. Patch only the named upgrade field in freshly read raw settings;
// the general settings writer also saves timers/entries and normalizes fields.
func writeWorkflowUpgradeField(ctx context.Context, root, field string, current *WorkflowManifest) error {
	raw, err := readWorkflowUpgradeFile(ctx, root+"/workflow.json")
	if err != nil {
		return err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return err
	}
	var prior WorkflowManifest
	if err := json.Unmarshal([]byte(raw), &prior); err != nil {
		return err
	}
	encode := func(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
	switch field {
	case "version":
		document["version"] = encode(current.Version)
	case "capabilities.browser_mode":
		var capabilities map[string]json.RawMessage
		if raw := document["capabilities"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &capabilities); err != nil {
				return err
			}
		}
		if capabilities == nil {
			capabilities = map[string]json.RawMessage{}
		}
		capabilities["browser_mode"] = encode(current.Capabilities.BrowserMode)
		document["capabilities"] = encode(capabilities)
	case "crew_attachments":
		var attachments []map[string]json.RawMessage
		if err := json.Unmarshal(document["crew_attachments"], &attachments); err != nil {
			return err
		}
		if len(attachments) != len(current.CrewAttachments) {
			return fmt.Errorf("Crew connections changed; read status again")
		}
		for i, updated := range current.CrewAttachments {
			var id, target string
			if err := json.Unmarshal(attachments[i]["id"], &id); err != nil {
				return err
			}
			if err := json.Unmarshal(attachments[i]["crew_project_id"], &target); err != nil {
				return err
			}
			if id != updated.ID || target != updated.CrewProjectID {
				return fmt.Errorf("Crew connection identity changed; read status again")
			}
			attachments[i]["crew_workspace_path"] = encode(updated.CrewWorkspacePath)
		}
		document["crew_attachments"] = encode(attachments)
	default:
		return fmt.Errorf("choose a named upgrade repair")
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	var candidate WorkflowManifest
	if err := json.Unmarshal(data, &candidate); err != nil {
		return err
	}
	if err := ValidateManifest(&candidate); err != nil {
		return err
	}
	if err := validateTeamManifestUpdate(ctx, &prior, &candidate); err != nil {
		return err
	}
	// The field whitelist cannot change membership, but a Team Contributor still
	// cannot change shared settings. Set the typed permit only after both checks.
	ctx = context.WithValue(ctx, teamManifestPermitKey{}, true)
	return writeFileToWorkspace(ctx, root+"/workflow.json", string(data)+"\n")
}
