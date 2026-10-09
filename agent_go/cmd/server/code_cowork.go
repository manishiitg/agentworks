package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// A Code project made in Cowork mode (a private assistant for non-technical business users; product.json `mode`) has its own prompt
// and skills and no coding or terminal guidance. The toolbar is the page's concern (WorkWorkspacePane); this is what the agent is told.

// codeProjectMode is the mode saved in the project's product.json ("dev", "cowork", "local"), or "" when none is saved. It is
// context, not authority: it only changes the prompt and skills, and the person owns the file.
func codeProjectMode(ctx context.Context, workspacePath string) string {
	raw, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(workspacePath), "/")+"/product.json")
	if err != nil || !exists {
		return ""
	}
	var manifest struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal([]byte(raw), &manifest) != nil {
		return ""
	}
	return strings.TrimSpace(manifest.Mode)
}

// applyCoworkProfile turns this turn's copy of the Code profile into the Cowork one: its own system prompt (same platform mechanics),
// the Cowork skills, and without the coding and terminal features.
func applyCoworkProfile(profile *agentprofiles.Profile) {
	if profile == nil || profile.ID != codeproduct.ProfileID {
		return
	}
	drop := map[string]bool{"coding": true, "terminal": true}
	features := make([]agentprofiles.ResolvedFeature, 0, len(profile.ResolvedFeatures))
	for _, feature := range profile.ResolvedFeatures {
		if !drop[feature.ID] {
			features = append(features, feature)
		}
	}
	profile.ResolvedFeatures = features
	bindings := make([]agentprofiles.FeatureBinding, 0, len(profile.Features))
	for _, binding := range profile.Features {
		if !drop[binding.ID] {
			bindings = append(bindings, binding)
		}
	}
	profile.Features = bindings
	profile.SystemPromptTemplate = codeproduct.CoworkPrompt(profile.SystemPromptTemplate)
	profile.Skills = appendUniqueStrings(append([]string(nil), profile.Skills...), codeproduct.CoworkSkills...)
}
