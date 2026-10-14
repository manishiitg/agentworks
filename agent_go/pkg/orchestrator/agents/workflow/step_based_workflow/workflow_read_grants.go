package step_based_workflow

import (
	"os"
	"path/filepath"
)

// rawDatabaseFiles are never readable by a step: the managed helper is the only way into the workflow database.
var rawDatabaseFiles = map[string]bool{"db.sqlite": true, "db.sqlite-wal": true, "db.sqlite-shm": true, "db.sqlite-journal": true}

// workflowReadGrants is what a step may READ: every top-level entry of its workflow folder, and every entry of db/
// except the raw database files. Granting entries one by one, instead of the folder with the raw database blocked
// inside it, lets Landlock and Seatbelt alone enforce the rule: no mount namespace is needed to hide a path, so a host
// where that launcher step fails (an AppArmor restriction, say) does not refuse shell commands. Symlinks are skipped
// (a link could lead outside the workflow). The list is built when a step starts, so a folder created mid-run becomes
// readable at the next step. When the folder cannot be listed (a preview or a test without files) the folder itself is
// returned.
func workflowReadGrants(baseWorkspacePath string) []string {
	root := baseWorkspacePath
	if !filepath.IsAbs(root) {
		root = filepath.Join(GetPromptDocsRoot(), baseWorkspacePath)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		return []string{baseWorkspacePath}
	}
	var grants []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(baseWorkspacePath, entry.Name())
		if entry.Name() != DBFolderName || !entry.IsDir() {
			grants = append(grants, path)
			continue
		}
		inner, err := os.ReadDir(filepath.Join(root, DBFolderName))
		if err != nil {
			continue
		}
		for _, file := range inner {
			if rawDatabaseFiles[file.Name()] || file.Type()&os.ModeSymlink != 0 {
				continue
			}
			grants = append(grants, filepath.Join(path, file.Name()))
		}
	}
	return grants
}
