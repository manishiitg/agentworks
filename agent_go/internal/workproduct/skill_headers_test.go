package workproduct

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every bundled SKILL.md header must parse: a bad one once kept the server from starting (PLAT-861-262).
func TestEveryBundledSkillHeaderParses(t *testing.T) {
	root := os.DirFS("../..")
	checked := 0
	err := fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "node_modules" || entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) && path != "." {
			return fs.SkipDir
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		content := string(data)
		if !strings.HasPrefix(content, "---\n") {
			return nil
		}
		end := strings.Index(content[4:], "\n---\n")
		if end < 0 {
			t.Errorf("%s: header has no closing ---", path)
			return nil
		}
		var header map[string]any
		if err := yaml.Unmarshal([]byte(content[4:end+4]), &header); err != nil {
			t.Errorf("%s: header does not parse: %v", path, err)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no bundled skills found")
	}
}
