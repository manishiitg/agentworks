package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// A Code project in Cowork mode gets its own prompt and skills and loses the coding and terminal features; the shared profile is not touched.
func TestCoworkProfileHasItsOwnPromptSkillsAndNoTerminal(t *testing.T) {
	profile := agentprofiles.Profile{
		ID:                   codeproduct.ProfileID,
		SystemPromptTemplate: "# Assistant\n\nYou are a helpful, capable assistant working in a person's private workspace.\n\n## The workspace\nbody",
		Features:             []agentprofiles.FeatureBinding{{ID: "coding"}, {ID: "terminal"}, {ID: "dashboard"}},
		ResolvedFeatures:     []agentprofiles.ResolvedFeature{{ID: "coding"}, {ID: "terminal"}, {ID: "dashboard"}},
		Skills:               []string{"code-mcp"},
	}
	shared := profile
	applyCoworkProfile(&profile)
	if !strings.HasPrefix(profile.SystemPromptTemplate, "# Cowork assistant") || !strings.Contains(profile.SystemPromptTemplate, "## The workspace\nbody") {
		t.Fatalf("prompt: %q", profile.SystemPromptTemplate)
	}
	for _, list := range [][]string{featureIDs(profile.Features), resolvedFeatureIDs(profile.ResolvedFeatures)} {
		if strings.Join(list, ",") != "dashboard" {
			t.Fatalf("only the dashboard feature should remain, got %v", list)
		}
	}
	for _, skill := range append([]string{"code-mcp"}, codeproduct.CoworkSkills...) {
		found := false
		for _, have := range profile.Skills {
			found = found || have == skill
		}
		if !found {
			t.Fatalf("skill %q missing from %v", skill, profile.Skills)
		}
	}
	if len(shared.Features) != 3 || len(shared.Skills) != 1 || !strings.HasPrefix(shared.SystemPromptTemplate, "# Assistant") {
		t.Fatal("the shared Code profile must not be changed")
	}
}

func featureIDs(bindings []agentprofiles.FeatureBinding) []string {
	out := []string{}
	for _, binding := range bindings {
		out = append(out, binding.ID)
	}
	return out
}

func resolvedFeatureIDs(features []agentprofiles.ResolvedFeature) []string {
	out := []string{}
	for _, feature := range features {
		out = append(out, feature.ID)
	}
	return out
}
