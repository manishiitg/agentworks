package productpolicy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

// CleanupProjected removes only platform-marked skill folders for products
// disabled by the installation. Native CLIs discover disk skills themselves,
// so omitting one from a new definition must also retire its previous projection.
// User-owned folders and symlinks are never followed or removed.
func CleanupProjected(workingDir string) error {
	if workingDir == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(workingDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, relative := range []string{".agents/skills", ".claude/skills", ".cursor/skills", ".codex/skills", ".gemini/skills", ".pi/skills"} {
		parent := root
		safe := true
		for _, component := range []string{filepath.Dir(relative), "skills"} {
			parent = filepath.Join(parent, component)
			info, err := os.Lstat(parent)
			if os.IsNotExist(err) {
				safe = false
				break
			}
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				safe = false
				break
			}
		}
		if !safe {
			continue
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			product := SkillProduct(entry.Name())
			if product == "" || Enabled(product) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			folder := filepath.Join(parent, entry.Name())
			marker, err := os.Lstat(filepath.Join(folder, projectfile.SkillMarkerFile))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !marker.Mode().IsRegular() {
				continue
			}
			if err := os.RemoveAll(folder); err != nil {
				return fmt.Errorf("retire disabled product skill %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}
