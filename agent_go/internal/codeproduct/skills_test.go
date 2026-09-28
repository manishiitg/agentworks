package codeproduct

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// Code's shared-feature skills are Crew's templates rendered with Code's
// product.yaml name, under Code's own skill names.
func TestCodeFeatureSkillsAreRenderedForCode(t *testing.T) {
	if err := RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	profile := BuiltinAgentProfile()
	if err := agentprofiles.ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	for _, name := range profile.Skills {
		if strings.HasPrefix(name, "work-") || name == "background-work" {
			t.Fatalf("Code attaches Crew's skill %s: %v", name, profile.Skills)
		}
	}
	for _, name := range []string{"code-mcp", "code-integrations", "code-workflow-files", "code-skills", "code-dashboard", "code-ui-control", "code-background-work", "code-schedules-and-bots"} {
		if !skills.IsBuiltinSkill(name) {
			t.Fatalf("%s is not registered", name)
		}
		attached := skills.LoadAttachable("", []string{name})
		if len(attached) != 1 {
			t.Fatalf("LoadAttachable(%s) = %v", name, attached)
		}
		content := attached[0].Content
		if strings.Contains(content, "{{product}}") || strings.Contains(content, "{{profile_id}}") {
			t.Fatalf("%s has an unrendered placeholder", name)
		}
		for _, crewSkill := range []string{"`work-mcp`", "`work-integrations`", "`work-skills`", "`background-work`"} {
			if strings.Contains(content, crewSkill) {
				t.Fatalf("%s points at Crew's skill %s", name, crewSkill)
			}
		}
	}
	bots := skills.LoadAttachable("", []string{"code-schedules-and-bots"})[0].Content
	if !strings.Contains(bots, "profile_id=code") || strings.Contains(bots, "profile_id=work") {
		t.Fatal("Code's bots skill does not name profile_id=code")
	}
	if !strings.Contains(skills.LoadAttachable("", []string{"code-skills"})[0].Content, "Code should remember that") {
		t.Fatal("Code's skills skill does not name the product")
	}
	for _, text := range agentprofiles.FeaturePromptExtensions(profile) {
		if strings.Contains(text, "{{product}}") || strings.Contains(text, "`work-") {
			t.Fatalf("prompt extension not rendered for Code: %q", text)
		}
	}
}
