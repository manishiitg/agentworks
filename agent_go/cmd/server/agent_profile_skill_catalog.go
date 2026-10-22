package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Read the same registered/materialized definitions as the product runtime.
// Workspace copies remain a separate inventory, never a provenance heuristic.
func agentProfileSkillDefinitions(profile agentprofiles.Profile, defaults map[string][]string, selection productpolicy.Selection) (map[string][]skills.Skill, error) {
	definitions := map[string][]skills.Skill{}
	for mode, names := range defaults {
		resolved := map[string]*llmtypes.Skill{}
		for _, name := range names {
			if definition := skills.BuiltinDefinition(name); definition != nil {
				resolved[name] = definition
			}
		}
		if profile.BuiltIn && profile.ID == "agentworks" && mode != "profile" {
			renderMode := "workshop"
			if mode == "run" {
				renderMode = "run"
			}
			if err := guidance.AttachConfiguredReferenceSurface(renderMode, mode == "builder", agentworksproduct.ChatSkills(mode), func(definition *llmtypes.Skill) error {
				resolved[definition.Name] = definition
				return nil
			}); err != nil {
				return nil, err
			}
		}
		if profile.BuiltIn && profile.ID == "relays" && mode != "profile" {
			chatSkills, err := relayproduct.ChatSkills(mode)
			if err != nil {
				return nil, err
			}
			for _, definition := range chatSkills {
				resolved[definition.Name] = definition
			}
		}
		definitions[mode] = []skills.Skill{}
		for _, name := range names {
			definition := selection.Skill(resolved[name])
			if definition == nil {
				continue
			}
			item := skills.Skill{FolderName: name, FilePath: "Platform", Frontmatter: skills.SkillFrontmatter{Name: definition.Name, Description: definition.Description}, Content: definition.Content, Managed: true, Builtin: true}
			if parsed, err := skills.ParseSkillFromContent(definition.Content, name, "Platform"); err == nil {
				item.Content = parsed.Content
			}
			definitions[mode] = append(definitions[mode], item)
		}
	}
	return definitions, nil
}
