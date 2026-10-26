package skills

import (
	"embed"
	"fmt"
	"gopkg.in/yaml.v3"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Each panel procedure has one source and one identity across product Builders.
// Product manifests select the panels they expose. Attaching a procedure never
// registers a tool or changes the caller's permissions.
//
//go:embed panels
var workspacePanelFiles embed.FS

var workspacePanelTools = map[string]map[string][]string{}
var workspacePanelExternalTools = map[string][]string{}

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
				Version       string              `yaml:"version"`
				Tools         map[string][]string `yaml:"panel-tools"`
				ExternalTools []string            `yaml:"external-tools"`
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
		workspacePanelExternalTools[name] = header.Metadata.ExternalTools
		if parsed.Frontmatter.Name != name {
			panic(fmt.Errorf("panel skill %s has a different name", name))
		}
		var supporting []llmtypes.SkillFile
		root := path.Dir(file)
		if err := fs.WalkDir(workspacePanelFiles, root, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || p == file {
				return nil
			}
			content, err := workspacePanelFiles.ReadFile(p)
			if err != nil {
				return err
			}
			supporting = append(supporting, llmtypes.SkillFile{RelPath: strings.TrimPrefix(p, root+"/"), Content: content})
			return nil
		}); err != nil {
			panic(err)
		}
		if err := RegisterBuiltin(&llmtypes.Skill{Name: name, Description: parsed.Frontmatter.Description,
			Content: parsed.Content, SupportingFiles: supporting, Metadata: map[string]string{"version": header.Metadata.Version}, Source: llmtypes.SkillSource{Origin: "builtin"}}); err != nil {
			panic(err)
		}
	}
}

// WorkspacePanelReference reads the canonical source used by both a panel
// package and product-specific reference projections.
func WorkspacePanelReference(name, relative string) ([]byte, error) {
	return workspacePanelFiles.ReadFile(path.Join("panels", name, relative))
}

func IsWorkspacePanelSkill(name string) bool {
	return strings.HasPrefix(name, "panel-") && IsBuiltinSkill(name)
}

// WorkspacePanelToolMap is the shared, product-specific mapping used by
// discovery and the contract inventory. Returning copies keeps it immutable.
func WorkspacePanelToolMap(name string) map[string][]string {
	out := map[string][]string{}
	for product, names := range workspacePanelTools[name] {
		for _, name := range names {
			public := platformtools.PublicName(name)
			if !slices.Contains(out[product], public) {
				out[product] = append(out[product], public)
			}
		}
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
			if slices.ContainsFunc(tools, func(source string) bool { return source == tool || platformtools.PublicName(source) == tool }) && !slices.Contains(names, skill.Name) {
				names = append(names, skill.Name)
			}
		}
	}
	if len(names) == 0 || strings.Contains(description, "Read the relevant attached guide with read_skill") {
		return description
	}
	return description + "\nRead the relevant attached guide with read_skill (choose the area for this action): " + strings.Join(names, ", ") + "."
}

func WorkspacePanelNames() []string {
	names := make([]string, 0, len(workspacePanelTools))
	for name := range workspacePanelTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func WorkspacePanelExternalTools(name string) []string {
	var out []string
	for _, source := range workspacePanelExternalTools[name] {
		public := platformtools.PublicName(source)
		if !slices.Contains(out, public) {
			out = append(out, public)
		}
	}
	return out
}
