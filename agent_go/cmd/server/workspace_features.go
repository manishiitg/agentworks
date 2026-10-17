package server

import (
	"context"
	"encoding/json"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func workspaceMemoryEnabled(ctx context.Context, profileID, root string) bool {
	if profileID == "code" && codeProjectMode(ctx, root) == "local" {
		return false
	}
	enabled := profileID == crewProfileID
	raw, found, err := readFileFromWorkspace(ctx, projectRuntimeManifestPath(profileID, root))
	if err != nil || !found {
		return enabled
	}
	var doc struct {
		Capabilities struct {
			MemoryEnabled *bool `json:"memory_enabled"`
		} `json:"capabilities"`
	}
	if json.Unmarshal([]byte(raw), &doc) == nil && doc.Capabilities.MemoryEnabled != nil {
		return *doc.Capabilities.MemoryEnabled
	}
	return enabled
}

func applyWorkspaceMemoryFeature(ctx context.Context, profile *agentprofiles.Profile, root string) error {
	if !isProjectProfileID(profile.ID) {
		return nil
	}
	enabled := workspaceMemoryEnabled(ctx, profile.ID, root)
	features := make([]agentprofiles.FeatureBinding, 0, len(profile.Features)+1)
	for _, binding := range profile.Features {
		if binding.ID != "memory" {
			features = append(features, binding)
		}
	}
	if enabled {
		features = append(features, agentprofiles.FeatureBinding{ID: "memory"})
	}
	resolved := profile.ResolvedFeatures[:0:0]
	for _, feature := range profile.ResolvedFeatures {
		if feature.ID != "memory" {
			resolved = append(resolved, feature)
		}
	}
	profile.ResolvedFeatures = resolved
	profile.Features = features
	if err := agentprofiles.ResolveFeatures(profile); err != nil {
		return err
	}
	return nil
}

func setWorkspaceMemory(ctx context.Context, profileID, root string, enabled bool) error {
	lock := productConversationRegistryMutex(projectRuntimeManifestPath(profileID, root))
	lock.Lock()
	defer lock.Unlock()
	raw, manifestPath, err := ensureProjectRuntimeManifest(ctx, profileID, root)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = json.Unmarshal([]byte(raw), &doc); err != nil {
		return err
	}
	caps, _ := doc["capabilities"].(map[string]any)
	if caps == nil {
		caps = map[string]any{}
		doc["capabilities"] = caps
	}
	caps["memory_enabled"] = enabled
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, manifestPath, string(data)+"\n")
}

func (api *StreamingAPI) workspaceEffectivePanels(ctx context.Context, userID, profileID, root string) []string {
	profile, err := api.agentProfiles.Resolve(profileID, 0, userID)
	if err != nil {
		return nil
	}
	if applyWorkspaceMemoryFeature(ctx, &profile, root) != nil {
		return nil
	}
	if profileID == "code" {
		switch codeProjectMode(ctx, root) {
		case "cowork":
			applyCoworkProfile(&profile)
		case "local":
			restrictCodeLocalFeatures(&profile)
		}
	}
	panels := []string{}
	for _, feature := range profile.ResolvedFeatures {
		panels = appendUniqueStrings(panels, feature.UIPanels...)
	}
	return panels
}
