package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"strings"
	"time"
)

// Main's purpose has one home: the Crew identity in product.json ("description"), which set_work_identity writes.
// Main's name is its own chat's (its tab identity), like every tab's; it is not the project's name (owner, 25 Nov,
// #4030), and only set_work_identity renames the project. Side tabs keep their own tab identity. Agents have no icon (owner, 5 Nov): an agent is its name and purpose.

type crewMainIdentity struct {
	Name, Purpose string
}

func crewManifestPath(workspacePath string) string {
	return path.Join(strings.Trim(strings.TrimSpace(workspacePath), "/"), "product.json")
}

// readCrewMainIdentity reads Main's identity from the Crew's product.json; ok is false when there is none to read.
func readCrewMainIdentity(ctx context.Context, workspacePath string) (crewMainIdentity, bool) {
	if strings.TrimSpace(workspacePath) == "" {
		return crewMainIdentity{}, false
	}
	raw, exists, err := readFileFromWorkspace(ctx, crewManifestPath(workspacePath))
	if err != nil || !exists {
		return crewMainIdentity{}, false
	}
	var manifest struct {
		Description string `json:"description"`
		Identity    struct {
			Name string `json:"name"`
		} `json:"identity"`
	}
	if json.Unmarshal([]byte(raw), &manifest) != nil {
		return crewMainIdentity{}, false
	}
	return crewMainIdentity{Name: strings.TrimSpace(manifest.Identity.Name), Purpose: strings.TrimSpace(manifest.Description)}, true
}

// writeCrewMainPurpose saves Main's purpose (nil keeps it) into the Crew identity, as set_work_identity does, keeping
// every other manifest field.
func writeCrewMainPurpose(ctx context.Context, workspacePath string, purpose *string) (crewMainIdentity, error) {
	manifestPath := crewManifestPath(workspacePath)
	raw, exists, err := readFileFromWorkspace(ctx, manifestPath)
	if err != nil {
		return crewMainIdentity{}, err
	}
	if !exists {
		return crewMainIdentity{}, fmt.Errorf("this project has no settings to save Main's identity in")
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return crewMainIdentity{}, fmt.Errorf("read this project's settings: %w", err)
	}
	if purpose != nil {
		clean := strings.Join(strings.Fields(*purpose), " ")
		if len([]rune(clean)) > productAgentPurposeMaxRunes {
			return crewMainIdentity{}, fmt.Errorf("the purpose is longer than %d characters", productAgentPurposeMaxRunes)
		}
		manifest["description"] = clean
	}
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return crewMainIdentity{}, err
	}
	if err := writeRawFileToWorkspace(ctx, manifestPath, string(encoded)+"\n"); err != nil {
		return crewMainIdentity{}, err
	}
	saved, _ := readCrewMainIdentity(ctx, workspacePath)
	log.Printf("[AGENT_IDENTITY] store=crew path=%s purpose_set=%t", manifestPath, purpose != nil)
	return saved, nil
}
