package agentprofiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// RegisterEmbeddedSkillsRendered parses every skill's YAML header and the server stops at startup
// on an invalid one (excellence was down on 2026-10-01 over one unquoted "key: value" inside a
// description). Check every SKILL.md in the repo here, so the build fails first.
func TestEverySkillFrontmatterIsValidYAML(t *testing.T) {
	checked := 0
	_ = filepath.Walk("../../", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(p) != "SKILL.md" || strings.Contains(p, "node_modules") {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Errorf("%s: %v", p, readErr)
			return nil
		}
		content := string(data)
		if !strings.HasPrefix(content, "---\n") {
			return nil
		}
		end := strings.Index(content[4:], "\n---\n")
		if end < 0 {
			return nil
		}
		var metadata struct {
			Description string `yaml:"description"`
		}
		if yamlErr := yaml.Unmarshal([]byte(content[4:end+4]), &metadata); yamlErr != nil {
			t.Errorf("%s: invalid YAML header: %v (quote the value)", p, yamlErr)
		}
		checked++
		return nil
	})
	if checked == 0 {
		t.Fatal("found no SKILL.md files: the path in this test is wrong")
	}
}
