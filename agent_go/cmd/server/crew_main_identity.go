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

// Main's name, purpose and icon have one home: the Crew identity in product.json ("identity" and "description"),
// which set_work_identity writes and which Main's purpose already came from. Side tabs keep their own tab identity.
// Before this, Main saved its icon in the Crew identity while the tab list read the tab identity, so the icon never
// showed (owner, 4 Nov, PLAT-856-509).

type crewMainIdentity struct {
	Name, Purpose, Icon string
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
			Icon string `json:"icon"`
		} `json:"identity"`
	}
	if json.Unmarshal([]byte(raw), &manifest) != nil {
		return crewMainIdentity{}, false
	}
	return crewMainIdentity{Name: strings.TrimSpace(manifest.Identity.Name), Purpose: strings.TrimSpace(manifest.Description), Icon: strings.TrimSpace(manifest.Identity.Icon)}, true
}

// writeCrewMainIdentity saves Main's name, purpose or icon (nil keeps one) into the Crew identity, as
// set_work_identity does, keeping every other manifest field.
func writeCrewMainIdentity(ctx context.Context, workspacePath string, name, purpose, icon *string) (crewMainIdentity, error) {
	manifestPath := crewManifestPath(workspacePath)
	raw, exists, err := readFileFromWorkspace(ctx, manifestPath)
	if err != nil {
		return crewMainIdentity{}, err
	}
	if !exists {
		return crewMainIdentity{}, fmt.Errorf("this Crew has no settings to save Main's identity in")
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return crewMainIdentity{}, fmt.Errorf("read this Crew's settings: %w", err)
	}
	identity, _ := manifest["identity"].(map[string]any)
	if identity == nil {
		identity = map[string]any{}
	}
	if name != nil {
		identity["name"] = strings.Join(strings.Fields(*name), " ")
	}
	if icon != nil {
		clean, err := cleanAgentIcon(*icon)
		if err != nil {
			return crewMainIdentity{}, err
		}
		identity["icon"] = clean
	}
	if purpose != nil {
		clean := strings.Join(strings.Fields(*purpose), " ")
		if len([]rune(clean)) > productAgentPurposeMaxRunes {
			return crewMainIdentity{}, fmt.Errorf("the purpose is longer than %d characters", productAgentPurposeMaxRunes)
		}
		manifest["description"] = clean
	}
	manifest["identity"] = identity
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return crewMainIdentity{}, err
	}
	if err := writeRawFileToWorkspace(ctx, manifestPath, string(encoded)+"\n"); err != nil {
		return crewMainIdentity{}, err
	}
	saved, _ := readCrewMainIdentity(ctx, workspacePath)
	log.Printf("[AGENT_IDENTITY] store=crew path=%s name_set=%t purpose_set=%t icon_set=%t icon=%q", manifestPath, name != nil, purpose != nil, icon != nil, saved.Icon)
	return saved, nil
}
