package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Each panel procedure has one source and one identity across product Builders.
// Product manifests select the panels they expose. Attaching a procedure never
// registers a tool or changes the caller's permissions.
//
//go:embed panels/*/SKILL.md
var workspacePanelFiles embed.FS

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
