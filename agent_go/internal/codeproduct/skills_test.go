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
	mcp := skills.LoadAttachable("", []string{"code-mcp"})[0].Content
	if !strings.Contains(mcp, "manage_my_mcp_servers") || strings.Contains(mcp, "install_mcp_server") || strings.Contains(mcp, "platform administrator") {
		t.Fatalf("Code's MCP skill is not the connections one: %s", mcp)
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

// Cowork's prompt keeps Code's platform mechanics but has its own introduction, and its skills are the ones registered for it.
func TestCoworkPromptHasItsOwnIntroAndTheSamePlatformMechanics(t *testing.T) {
	template := BuiltinAgentProfile().SystemPromptTemplate
	prompt := CoworkPrompt(template)
	if !strings.HasPrefix(prompt, "# Cowork assistant") || strings.Contains(prompt, "You are a helpful, capable assistant working in") {
		t.Fatalf("Cowork must have its own introduction:\n%.300s", prompt)
	}
	for _, mechanics := range []string{"## The workspace", "## Platform actions", "## Other chats in this Code"} {
		if !strings.Contains(prompt, mechanics) {
			t.Fatalf("the platform mechanics %q must stay in the Cowork prompt", mechanics)
		}
	}
	for _, skill := range CoworkSkills {
		if !strings.Contains(prompt, skill) && skill != "cowork-assistant" {
			// the intro names the skills the agent should read
			t.Fatalf("the Cowork introduction should point at %q", skill)
		}
	}
}
