package step_based_workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestBackgroundAgentSkillsContextPreservesFullSkillBundles(t *testing.T) {
	content := []byte("reference body")
	inherited := []*llmtypes.Skill{{
		Name:        "workflow-specialist",
		Description: "workflow-specific operating knowledge",
		Content:     "skill body",
		SupportingFiles: []llmtypes.SkillFile{{
			RelPath: "references/operations.md",
			Content: content,
		}},
	}}

	ctx := withBackgroundAgentSkills(context.Background(), inherited)
	got := backgroundAgentSkillsFromContext(ctx)
	if len(got) != 1 {
		t.Fatalf("inherited skill count = %d, want 1", len(got))
	}
	if got[0] != inherited[0] {
		t.Fatal("context should carry the immutable definition snapshot without reloading it by name")
	}
	if len(got[0].SupportingFiles) != 1 || string(got[0].SupportingFiles[0].Content) != string(content) {
		t.Fatalf("supporting files were not preserved: %#v", got[0].SupportingFiles)
	}

	// Callers receive their own slice header, so modifying the returned list
	// cannot remove skills from the stored snapshot.
	got[0] = nil
	if again := backgroundAgentSkillsFromContext(ctx); len(again) != 1 || again[0] == nil {
		t.Fatal("reading inherited skills should return an independent slice")
	}
}

func TestMissingBackgroundSkillsDeduplicatesChildAndParentIdentity(t *testing.T) {
	existing := []*llmtypes.Skill{{Name: "builder-reference"}}
	inheritedReference := &llmtypes.Skill{Name: "builder-reference"}
	inheritedCustom := &llmtypes.Skill{Name: "custom-analysis"}

	got := missingBackgroundSkills(existing, []*llmtypes.Skill{
		nil,
		{Name: " "},
		inheritedReference,
		inheritedCustom,
		{Name: "custom-analysis"},
	})
	if len(got) != 1 || got[0] != inheritedCustom {
		t.Fatalf("missing skills = %#v, want only the first custom-analysis definition", got)
	}
	if names := backgroundSkillNames([]*llmtypes.Skill{inheritedReference, inheritedCustom, inheritedCustom}); strings.Join(names, ",") != "builder-reference,custom-analysis" {
		t.Fatalf("background skill names = %v", names)
	}
}
