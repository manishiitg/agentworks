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

// Main's name and purpose have one home: the Crew identity in product.json ("identity" and "description"),
// which set_work_identity writes and which Main's purpose already came from. Main's name is its own, kept on its tab like
// any agent's (#4030); renaming the project is set_work_identity's. Side tabs keep their own tab identity
// (PLAT-856-509). Agents have no icon (owner, 5 Nov): an agent is its name and purpose.

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

// mainChatName is a person's Main as people see it: the name its person gave it, else "Main" (owner, 25 Nov, #4030:
// Main is an agent like any other, and renaming it renames the chat, not the project). An automatic import label is
// never shown.
func mainChatName(identity ProductAgentIdentity) string {
	if name := strings.TrimSpace(identity.Name); name != "" && !identity.GeneratedName {
		return name
	}
	return "Main"
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
