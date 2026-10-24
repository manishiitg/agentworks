package server

import (
	"context"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func applyWorkspaceMemoryFeature(profile *agentprofiles.Profile) error {
	if !isProjectProfileID(profile.ID) {
		return nil
	}
	features := make([]agentprofiles.FeatureBinding, 0, len(profile.Features)+1)
	for _, binding := range profile.Features {
		if binding.ID != "memory" {
			features = append(features, binding)
		}
	}
	// Every Crew has project memory.
	features = append(features, agentprofiles.FeatureBinding{ID: "memory"})
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

func (api *StreamingAPI) workspaceEffectivePanels(ctx context.Context, userID, profileID, root string) []string {
	profile, err := api.agentProfiles.Resolve(profileID, 0, userID)
	if err != nil {
		return nil
	}
	if applyWorkspaceMemoryFeature(&profile) != nil {
		return nil
	}
	if profileID == crewProfileID {
		switch codeProjectMode(ctx, root) {
		case "cowork":
			applyCoworkProfile(&profile)
		}
	}
	panels := []string{}
	for _, feature := range profile.ResolvedFeatures {
		panels = appendUniqueStrings(panels, feature.UIPanels...)
	}
	return panels
}
