package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

type crewAgentCatalog struct {
	SchemaVersion int                 `json:"schema_version"`
	Agents        []crewAgentTemplate `json:"agents"`
}

var errUnknownCrewTemplate = errors.New("unknown Crew template")

type crewAgentTemplate struct {
	ID             string            `json:"id"`
	Version        int               `json:"version"`
	Name           string            `json:"name"`
	Role           string            `json:"role"`
	Purpose        string            `json:"purpose"`
	SelectedSkills []string          `json:"selected_skills"`
	Files          map[string]string `json:"files"`
}

func loadCrewAgentTemplate(id string) (*crewAgentTemplate, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	root, err := playbooksRoot()
	if err != nil {
		return nil, err
	}
	for _, category := range []string{"website-growth", "finance", "marketing", "sales", "customer-success", "customer-support", "product", "operations", "engineering", "qa", "security", "gtm", "shopify"} {
		content, readErr := os.ReadFile(path.Join(root, "crew-agents", category, "catalog.json"))
		if readErr != nil {
			return nil, fmt.Errorf("read %s Crew catalog: %w", category, readErr)
		}
		var catalog crewAgentCatalog
		if err := json.Unmarshal(content, &catalog); err != nil || catalog.SchemaVersion != 1 {
			return nil, fmt.Errorf("invalid %s Crew catalog", category)
		}
		for index := range catalog.Agents {
			item := &catalog.Agents[index]
			if item.ID != id {
				continue
			}
			if item.Version < 1 || len(item.SelectedSkills) == 0 || len(item.Files) < 3 || item.SelectedSkills[0] != id {
				return nil, fmt.Errorf("invalid Crew template %q", id)
			}
			for relative := range item.Files {
				legacyFinanceSetup := id == "finance-analyst" && (relative == "TEMPLATE_SETUP.md" || relative == "TEMPLATE_SETUP.json")
				if path.IsAbs(relative) || path.Clean(relative) != relative || strings.HasPrefix(relative, "../") ||
					(!strings.HasPrefix(relative, "skills/"+id+"/") && !strings.HasPrefix(relative, "templates/"+id+"/") && !legacyFinanceSetup) {
					return nil, fmt.Errorf("invalid path in Crew template %q", id)
				}
			}
			return item, nil
		}
	}
	return nil, fmt.Errorf("%w %q", errUnknownCrewTemplate, id)
}

// applyCrewAgentTemplate is idempotent across Builder retries. The
// product manifest receipt is written last, after every local skill and setup
// file exists and the skill is selected. Existing user-edited files are never
// overwritten by a retried creation call.
func applyCrewAgentTemplate(ctx context.Context, workspacePath string, item *crewAgentTemplate) error {
	return applyCrewAgentTemplateFiles(ctx, workspacePath, item, false)
}

// applyCrewAgentTemplateFiles installs a template. keepEdited installs into an
// existing Crew: a file the owner already has is kept byte for byte instead of
// failing the install; creation stays strict. The version check runs before
// anything is written.
func applyCrewAgentTemplateFiles(ctx context.Context, workspacePath string, item *crewAgentTemplate, keepEdited bool) error {
	if item == nil {
		return nil
	}
	product, _, ok := projectProductForPath(workspacePath)
	if !ok {
		return fmt.Errorf("Crew workspace was not found")
	}
	manifestPath := path.Join(workspacePath, "product.json")
	raw, found, err := readFileFromWorkspace(ctx, manifestPath)
	if err != nil {
		return fmt.Errorf("read Crew manifest for template: %w", err)
	}
	if !found {
		return fmt.Errorf("Crew manifest is missing for template")
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode Crew manifest for template: %w", err)
	}
	installed := crewTemplateReceiptRows(manifest)
	for _, entry := range installed {
		row, _ := entry.(map[string]interface{})
		if row["id"] == item.ID {
			if row["version"] == float64(item.Version) {
				return nil
			}
			return fmt.Errorf("Crew has another version of template %q", item.ID)
		}
	}
	for relative, content := range item.Files {
		fullPath := path.Join(workspacePath, relative)
		current, exists, err := readFileFromWorkspace(ctx, fullPath)
		if err != nil {
			return fmt.Errorf("read Crew template file %s: %w", relative, err)
		}
		if exists {
			if current != content && !keepEdited {
				return fmt.Errorf("Crew template file %s already has different content", relative)
			}
			continue
		}
		if err := createWorkspaceFolder(ctx, path.Dir(fullPath)); err != nil {
			return fmt.Errorf("create Crew template folder: %w", err)
		}
		if err := writeRawFileToWorkspace(ctx, fullPath, content); err != nil {
			return fmt.Errorf("write Crew template file %s: %w", relative, err)
		}
	}
	if err := applyCrewCreationSelections(ctx, product.ProfileID, workspacePath, item.SelectedSkills, nil, nil, nil); err != nil {
		return err
	}
	installed = append(installed, map[string]interface{}{"id": item.ID, "version": item.Version})
	manifest["templates"] = installed
	delete(manifest, "template")
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeRawFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}

// Normalize the earlier single-template receipt without losing it on install.
func crewTemplateReceiptRows(manifest map[string]interface{}) []interface{} {
	rows, _ := manifest["templates"].([]interface{})
	rows = append([]interface{}{}, rows...)
	if legacy, ok := manifest["template"].(map[string]interface{}); ok {
		for _, row := range rows {
			if current, ok := row.(map[string]interface{}); ok && current["id"] == legacy["id"] {
				return rows
			}
		}
		rows = append(rows, legacy)
	}
	return rows
}

// Detaching keeps files and setup progress. Only skills no retained template
// needs are deselected; the receipt is written last for an idempotent retry.
func removeCrewAgentTemplate(ctx context.Context, root string, item *crewAgentTemplate) error {
	product, _, ok := projectProductForPath(root)
	if !ok {
		return fmt.Errorf("Crew workspace was not found")
	}
	raw, found, err := readFileFromWorkspace(ctx, root+"/product.json")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Crew configuration was not found")
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	remaining := []interface{}{}
	removed := false
	retained := map[string]bool{}
	for _, row := range crewTemplateReceiptRows(manifest) {
		entry, _ := row.(map[string]interface{})
		id, _ := entry["id"].(string)
		if id == item.ID {
			removed = true
			continue
		}
		remaining = append(remaining, row)
		other, err := loadCrewAgentTemplate(id)
		if err != nil || other == nil {
			// An older/custom receipt may no longer be in the catalog. Keep
			// selected skills rather than guessing which it still needs.
			current, readErr := productSelectedSkills(ctx, product.ProfileID, root)
			if readErr != nil {
				return readErr
			}
			for _, name := range current {
				retained[name] = true
			}
			continue
		}
		for _, name := range other.SelectedSkills {
			retained[name] = true
		}
	}
	if !removed {
		return nil
	}
	remove := map[string]bool{}
	for _, name := range item.SelectedSkills {
		if !retained[name] {
			remove[name] = true
		}
	}
	if err := updateProductSelectedSkills(ctx, product.ProfileID, root, func(current []string) []string {
		next := []string{}
		for _, name := range current {
			if !remove[name] {
				next = append(next, name)
			}
		}
		return next
	}); err != nil {
		return err
	}
	manifest["templates"] = remaining
	delete(manifest, "template")
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeRawFileToWorkspace(ctx, root+"/product.json", string(encoded)+"\n")
}
