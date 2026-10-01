package agentprofiles

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// SkillFileBinding names a product-owned SKILL.md in its embedded filesystem.
// The rendered frontmatter supplies its discovery description unless Description
// explicitly overrides it for a product variant.
type SkillFileBinding struct {
	Name        string
	Description string
	Path        string
}

// RegisterEmbeddedSkills reads each binding's SKILL.md and any embedded files
// beside it from fsys, strips the frontmatter, and registers the complete
// bundle as a builtin skill. Supporting files keep product-owned skills on the
// same progressive-disclosure path as imported skills: SKILL.md stays short,
// while references/ and scripts/ are projected only for agents that attach it.
//
// fsys is a product's own //go:embed'd filesystem (embed.FS satisfies fs.FS),
// so this has no dependency on any one product's package layout. Call it from
// inside a sync.Once in the product's own registration entry point -- that
// part stays per-product, since each product's "have I registered yet" is
// independent of every other product's.
func RegisterEmbeddedSkills(fsys fs.FS, bindings []SkillFileBinding) error {
	return RegisterEmbeddedSkillsRendered(fsys, bindings, nil)
}

// RegisterEmbeddedSkillsRendered is RegisterEmbeddedSkills with every
// SKILL.md and text supporting file passed through render first, so one
// embedded template can be registered once per product (see
// RenderFeatureText). A nil render registers the files unchanged.
func RegisterEmbeddedSkillsRendered(fsys fs.FS, bindings []SkillFileBinding, render func(string) string) error {
	if render == nil {
		render = func(text string) string { return text }
	}
	for _, binding := range bindings {
		data, err := fs.ReadFile(fsys, binding.Path)
		if err != nil {
			return fmt.Errorf("read skill %q: %w", binding.Name, err)
		}
		content := render(string(data))
		description := binding.Description
		if strings.HasPrefix(content, "---\n") {
			if end := strings.Index(content[4:], "\n---\n"); end >= 0 {
				var metadata struct {
					Description string `yaml:"description"`
				}
				if err := yaml.Unmarshal([]byte(content[4:end+4]), &metadata); err != nil {
					return fmt.Errorf("parse skill %q frontmatter: %w", binding.Name, err)
				}
				if strings.TrimSpace(description) == "" {
					description = strings.TrimSpace(metadata.Description)
				}
				content = content[end+9:]
			}
		}
		root := path.Dir(binding.Path)
		var supportingFiles []llmtypes.SkillFile
		if err := fs.WalkDir(fsys, root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filePath == binding.Path {
				return nil
			}
			fileData, err := fs.ReadFile(fsys, filePath)
			if err != nil {
				return err
			}
			relPath := strings.TrimPrefix(filePath, root+"/")
			if utf8.Valid(fileData) {
				fileData = []byte(render(string(fileData)))
			}
			supportingFiles = append(supportingFiles, llmtypes.SkillFile{RelPath: relPath, Content: fileData})
			return nil
		}); err != nil {
			return fmt.Errorf("read supporting files for skill %q: %w", binding.Name, err)
		}
		if err := skills.RegisterBuiltin(&llmtypes.Skill{
			Name:            binding.Name,
			Description:     description,
			Content:         content,
			SupportingFiles: supportingFiles,
			Source:          llmtypes.SkillSource{Origin: "builtin"},
		}); err != nil {
			return fmt.Errorf("register skill %q: %w", binding.Name, err)
		}
	}
	return nil
}

// RegisterSkills registers already-materialized skills directly -- for a
// product whose skill content isn't a static embedded file (e.g. rendered
// from a shared template registry, like
// guidance.MaterializeReferenceKindsAsSkills) but should still be an
// individually-named, product-declared skill the same way
// RegisterEmbeddedSkills' file-backed ones are.
func RegisterSkills(items []*llmtypes.Skill) error {
	for _, item := range items {
		if err := skills.RegisterBuiltin(item); err != nil {
			return fmt.Errorf("register skill %q: %w", item.Name, err)
		}
	}
	return nil
}
