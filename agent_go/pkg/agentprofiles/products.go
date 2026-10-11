package agentprofiles

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"

// ForProducts projects a resolved manifest for one deployment/caller without
// changing the registry's canonical definition. Apply after ownership checks
// and before deriving prompts, tools, skills or client-visible feature metadata.
func (p Profile) ForProducts(selection productpolicy.Selection) Profile {
	p = cloneProfile(p)
	p.SystemPromptTemplate = selection.Text(p.SystemPromptTemplate)
	p.Skills = filterProductNames(p.Skills, selection.AllowsSkill)
	p.ToolPolicy.Enabled = filterProductNames(p.ToolPolicy.Enabled, selection.AllowsTool)
	p.Runtime.BridgeTools = filterProductNames(p.Runtime.BridgeTools, selection.AllowsTool)
	tools := p.Tools[:0]
	for _, tool := range p.Tools {
		if selection.AllowsBinding(tool.ID) {
			tools = append(tools, tool)
		}
	}
	p.Tools = tools
	features := p.Features[:0]
	for _, feature := range p.Features {
		if feature.ID != "knowledgebase" || selection.Has("knowledgebase") {
			features = append(features, feature)
		}
	}
	p.Features = features
	resolved := p.ResolvedFeatures[:0]
	for _, feature := range p.ResolvedFeatures {
		if feature.ID == "knowledgebase" && !selection.Has("knowledgebase") {
			continue
		}
		feature.Tools = filterProductNames(feature.Tools, selection.AllowsTool)
		feature.Skills = filterProductNames(feature.Skills, selection.AllowsSkill)
		feature.PromptExtension = selection.Text(feature.PromptExtension)
		resolved = append(resolved, feature)
	}
	p.ResolvedFeatures = resolved
	return p
}

func filterProductNames(names []string, allowed func(string) bool) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if allowed(name) {
			out = append(out, name)
		}
	}
	return out
}
