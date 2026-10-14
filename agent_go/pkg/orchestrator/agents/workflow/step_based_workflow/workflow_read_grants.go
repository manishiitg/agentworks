package step_based_workflow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
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

// folderGuardPromptText is what the step prompt says about the folder guard. The sandbox gets the full per-entry list;
// the model gets a short summary in relative names (about 90 absolute paths on every step was noise). Paths outside the
// workflow folder, such as Downloads or skill folders, stay listed because they are informative.
func folderGuardPromptText(baseWorkspacePath string, paths []string, write bool) string {
	docsRoot := GetPromptDocsRoot()
	base := strings.TrimRight(baseWorkspacePath, "/")
	inside := func(p string) (string, bool) {
		p = strings.TrimRight(p, "/")
		if rel, ok := strings.CutPrefix(p, base+"/"); ok && base != "" {
			return rel, true
		}
		return "", p == base && base != ""
	}
	covered := func(list []string, p string) bool {
		for _, other := range list {
			if other != p && strings.HasPrefix(p, strings.TrimRight(other, "/")+"/") {
				return true
			}
		}
		return false
	}
	var relative, outside []string
	wide := false
	for _, p := range common.DeduplicateStrings(paths) {
		if p == "" || covered(paths, p) {
			continue
		}
		if rel, ok := inside(p); ok {
			if rel == "" || rel == "runs" {
				wide = true
			}
			relative = append(relative, rel+"/")
		} else if !strings.HasPrefix(p, base+"/") {
			outside = append(outside, toAbsPaths(docsRoot, []string{p})[0])
		}
	}
	var parts []string
	switch {
	case write:
		parts = append(parts, strings.Join(relative, ", ")+" (relative to the workflow folder)")
	case wide:
		parts = append(parts, "everything in this workflow folder except the raw database files (use the database tools)")
	default:
		parts = append(parts, strings.Join(relative, ", ")+" (relative to the workflow folder)")
	}
	if len(outside) > 0 {
		parts = append(parts, strings.Join(outside, ", "))
	}
	text := strings.Join(parts, "; plus ")
	if write {
		text += ". Not writable: the plan, step settings, past runs"
		if !slices.Contains(relative, "code/") {
			text += ", code/ (Builder only)"
		}
	}
	return text
}
