package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
)

type crewAgentCatalog struct {
	SchemaVersion int                 `json:"schema_version"`
	Agents        []crewAgentTemplate `json:"agents"`
}

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
	return nil, fmt.Errorf("unknown Crew template %q", id)
}

// applyCrewAgentTemplate is idempotent across Builder retries. The
// product manifest receipt is written last, after every local skill and setup
// file exists and the skill is selected. Existing user-edited files are never
// overwritten by a retried creation call.
func applyCrewAgentTemplate(ctx context.Context, workspacePath string, item *crewAgentTemplate) error {
	if item == nil {
		return nil
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
	installed, _ := manifest["templates"].([]interface{})
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
			if current != content {
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
	if err := applyCrewCreationSelections(ctx, "work", workspacePath, item.SelectedSkills, nil, nil, nil); err != nil {
		return err
	}
	installed = append(installed, map[string]interface{}{"id": item.ID, "version": item.Version})
	manifest["templates"] = installed
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeRawFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}
