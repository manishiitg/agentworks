package sparkquillproduct

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

const (
	// ParentProfileID is the product's primary profile: the parent's own
	// conversation with Quill.
	ParentProfileID = "sparkquill"
	// ChildProfileID is the tutoring profile, one conversation per activity.
	ChildProfileID = "sparkquill-child"
)

// BuiltinAgentProfiles returns the parent and child profiles with their
// prompts rendered. The prompt files use Go template actions
// ({{.LocalDateTime}}, {{.Product.NAME}}) that the platform renders per
// turn with the variables PromptVariables computes.
func BuiltinAgentProfiles() []agentprofiles.Profile {
	profiles, err := mustSparkQuillManifest().BuiltinProfiles(productConfigFiles, nil)
	if err != nil {
		panic(fmt.Errorf("render SparkQuill prompts: %w", err))
	}
	for i := range profiles {
		profiles[i] = agentprofiles.WithPlatformCodingProviders(profiles[i])
	}
	return profiles
}

var (
	registerSkillsOnce sync.Once
	registerSkillsErr  error
)

// SkillNames lists the family's skill bundles (top-level skills/<name>/
// folders that carry a SKILL.md), sorted.
func SkillNames() []string {
	entries, err := fs.ReadDir(SkillFiles, "skills")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := fs.Stat(SkillFiles, "skills/"+e.Name()+"/SKILL.md"); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// RegisterProductSkills registers every family skill bundle with the
// platform's skill registry, once.
func RegisterProductSkills() error {
	registerSkillsOnce.Do(func() {
		var bindings []agentprofiles.SkillFileBinding
		for _, name := range SkillNames() {
			bindings = append(bindings, agentprofiles.SkillFileBinding{
				Name:        name,
				Description: skillDescription(name),
				Path:        "skills/" + name + "/SKILL.md",
			})
		}
		registerSkillsErr = agentprofiles.RegisterEmbeddedSkills(SkillFiles, bindings)
	})
	return registerSkillsErr
}

// skillDescription prefers the frontmatter description, with a prose fallback
// for older family skills that have no frontmatter.
func skillDescription(name string) string {
	raw, err := SkillFiles.ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		return name
	}
	if frontmatter, _, err := skills.ParseSkillFile(string(raw)); err == nil && strings.TrimSpace(frontmatter.Description) != "" {
		return strings.TrimSpace(frontmatter.Description)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") {
			continue
		}
		return line
	}
	return name
}
