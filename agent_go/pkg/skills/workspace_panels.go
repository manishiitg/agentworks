package skills

import (
	"embed"
	"fmt"
	"gopkg.in/yaml.v3"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Each panel procedure has one source and one identity across product Builders.
// Product manifests select the panels they expose. Attaching a procedure never
// registers a tool or changes the caller's permissions.
//
//go:embed panels/*/SKILL.md
var workspacePanelFiles embed.FS

var workspacePanelTools = map[string]map[string][]string{}

func init() {
	files, err := fs.Glob(workspacePanelFiles, "panels/*/SKILL.md")
	if err != nil {
		panic(err)
	}
	for _, file := range files {
		body, err := workspacePanelFiles.ReadFile(file)
		if err != nil {
			panic(err)
		}
		name := path.Base(path.Dir(file))
		parsed, err := ParseSkillFromContent(string(body), name, file)
		if err != nil {
			panic(fmt.Errorf("panel skill %s: %w", name, err))
		}
		var header struct {
			Metadata struct {
				Tools map[string][]string `yaml:"panel-tools"`
			} `yaml:"metadata"`
		}
		parts := strings.SplitN(string(body), "---", 3)
		if len(parts) != 3 {
			panic("invalid panel skill header: " + name)
		}
		if err := yaml.Unmarshal([]byte(parts[1]), &header); err != nil {
			panic(err)
		}
		workspacePanelTools[name] = header.Metadata.Tools
		if parsed.Frontmatter.Name != name {
			panic(fmt.Errorf("panel skill %s has a different name", name))
		}
		if err := RegisterBuiltin(&llmtypes.Skill{Name: name, Description: parsed.Frontmatter.Description,
			Content: parsed.Content, Source: llmtypes.SkillSource{Origin: "builtin"}}); err != nil {
			panic(err)
		}
	}
}

func IsWorkspacePanelSkill(name string) bool {
	return strings.HasPrefix(name, "panel-") && IsBuiltinSkill(name)
}

// WorkspacePanelToolMap is the shared, product-specific mapping used by
// discovery and the contract inventory. Returning copies keeps it immutable.
func WorkspacePanelToolMap(name string) map[string][]string {
	out := map[string][]string{}
	for product, names := range workspacePanelTools[name] {
		out[product] = append([]string(nil), names...)
	}
	return out
}

// WorkspacePanelToolGuidance names only attached panel procedures for an
// actually registered tool. It adds a reading hint, never a permission gate.
func WorkspacePanelToolGuidance(tool, description string, attached []*llmtypes.Skill) string {
	var names []string
	for _, skill := range attached {
		if skill == nil {
			continue
		}
		for _, tools := range workspacePanelTools[skill.Name] {
			if slices.Contains(tools, tool) && !slices.Contains(names, skill.Name) {
				names = append(names, skill.Name)
			}
		}
	}
	if len(names) == 0 || strings.Contains(description, "Before changing this panel, read the matching attached skill") {
		return description
	}
	return description + "\nBefore changing this panel, read the matching attached skill with read_skill (choose the relevant one): " + strings.Join(names, ", ") + "."
}
