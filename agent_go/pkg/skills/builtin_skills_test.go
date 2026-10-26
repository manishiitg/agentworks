package skills

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestRegisterBuiltinMakesProductSkillAttachableByName(t *testing.T) {
	input := &llmtypes.Skill{
		Name:        "test-product-research-director",
		Description: "Research a product brief",
		Content:     "# Research Director\n\nStudy the brief.",
		Paths:       []string{"briefs/**"},
		Metadata:    map[string]string{"product": "example-product"},
		SupportingFiles: []llmtypes.SkillFile{
			{RelPath: "references/checklist.md", Content: []byte("original checklist")},
		},
	}
	if err := RegisterBuiltin(input); err != nil {
		t.Fatalf("RegisterBuiltin() error = %v", err)
	}

	// Registration must snapshot the supplied definition. Products commonly
	// build the value from temporary embed/parsing structures and should be
	// free to reuse those structures after startup.
	input.Content = "mutated input"
	input.Paths[0] = "mutated/**"
	input.Metadata["product"] = "mutated"
	input.SupportingFiles[0].Content[0] = 'X'

	got := LoadAttachable("http://workspace-must-not-be-needed.invalid", []string{"test-product-research-director"})
	if len(got) != 1 {
		t.Fatalf("LoadAttachable() returned %d skills, want 1", len(got))
	}
	if got[0].Content != "# Research Director\n\nStudy the brief." {
		t.Fatalf("registered content was mutated: %q", got[0].Content)
	}
	if got[0].Paths[0] != "briefs/**" || got[0].Metadata["product"] != "example-product" {
		t.Fatalf("registered slices/maps were mutated: %+v", got[0])
	}
	if string(got[0].SupportingFiles[0].Content) != "original checklist" {
		t.Fatalf("registered supporting file was mutated: %q", got[0].SupportingFiles[0].Content)
	}
	if got[0].Source.Origin != "builtin" {
		t.Fatalf("default source origin = %q, want builtin", got[0].Source.Origin)
	}
	if !IsBuiltinSkill("test-product-research-director") {
		t.Fatal("registered product skill is not reported as builtin")
	}

	// Resolution must also return a fresh copy for every agent.
	got[0].Content = "mutated result"
	got[0].Metadata["product"] = "mutated result"
	got[0].SupportingFiles[0].Content[0] = 'Y'
	gotAgain := LoadAttachable("http://workspace-must-not-be-needed.invalid", []string{"test-product-research-director"})
	if len(gotAgain) != 1 || gotAgain[0].Content != "# Research Director\n\nStudy the brief." {
		t.Fatalf("one resolved agent mutated the registry: %+v", gotAgain)
	}
	if gotAgain[0].Metadata["product"] != "example-product" || string(gotAgain[0].SupportingFiles[0].Content) != "original checklist" {
		t.Fatalf("one resolved agent mutated nested registry data: %+v", gotAgain[0])
	}
}

func TestRegisterBuiltinRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name  string
		skill *llmtypes.Skill
		want  string
	}{
		{name: "nil", skill: nil, want: "nil"},
		{name: "empty name", skill: &llmtypes.Skill{Description: "description", Content: "content"}, want: "invalid skill name"},
		{name: "slash name", skill: &llmtypes.Skill{Name: "video/research", Description: "description", Content: "content"}, want: "invalid skill name"},
		{name: "uppercase name", skill: &llmtypes.Skill{Name: "Video-Research", Description: "description", Content: "content"}, want: "invalid skill name"},
		{name: "missing description", skill: &llmtypes.Skill{Name: "test-missing-description", Content: "content"}, want: "no description"},
		{name: "missing content", skill: &llmtypes.Skill{Name: "test-missing-content", Description: "description"}, want: "no content"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := RegisterBuiltin(test.skill)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RegisterBuiltin() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRegisterBuiltinRejectsDuplicateName(t *testing.T) {
	skill := &llmtypes.Skill{
		Name:        "test-duplicate-product-skill",
		Description: "A product skill",
		Content:     "# Product skill",
	}
	if err := RegisterBuiltin(skill); err != nil {
		t.Fatalf("first RegisterBuiltin() error = %v", err)
	}
	if err := RegisterBuiltin(skill); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate RegisterBuiltin() error = %v, want already registered", err)
	}
}

func TestBuiltinRegistryKeepsAgentBrowserAvailable(t *testing.T) {
	if !IsBuiltinSkill("agent-browser") {
		t.Fatal("agent-browser was not registered at package startup")
	}
	got := builtinAttachableSkill("agent-browser")
	if got == nil || got.Name != "agent-browser" || !strings.Contains(got.Content, "CDP Shared Chrome Rules") {
		t.Fatalf("unexpected registered agent-browser skill: %+v", got)
	}
}
