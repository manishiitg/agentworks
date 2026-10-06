// Package remoteplacement reports which workflows live on a remote workspace
// server instead of this machine.
//
// Placement is laptop-local state owned by the local workspace-api, which
// routes every workspace request for a placed workflow to its server. It lives
// next to the docs root, never inside it, because it also holds server tokens
// (see workspace/remote_router.go remotePlacementFile). agent_go only needs to
// know that such a workflow has no local folder, so it reads the file directly.
package remoteplacement

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// FileEnv overrides the placement file location (shared with workspace-api).
const FileEnv = "WORKSPACE_REMOTE_WORKFLOWS_FILE"

// File is the placement file for a docs root.
func File(docsRoot string) string {
	if v := strings.TrimSpace(os.Getenv(FileEnv)); v != "" {
		return filepath.Clean(v)
	}
	root := filepath.Clean(docsRoot)
	return filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+".remote-workflows.json")
}

type file struct {
	Workflows map[string]string `json:"workflows"`
}

// ServerFor returns the server id that owns workspacePath (a docs-relative
// path inside a workflow), or "" when it is local.
func ServerFor(docsRoot, workspacePath string) string {
	rel := workspaceRelativePath(docsRoot, workspacePath)
	if rel == "" || strings.TrimSpace(docsRoot) == "" {
		return ""
	}
	raw, err := os.ReadFile(File(docsRoot)) // #nosec G304 -- operator-owned path next to the docs root
	if err != nil {
		return ""
	}
	var f file
	if json.Unmarshal(raw, &f) != nil {
		return ""
	}
	for wf, id := range f.Workflows {
		wf = clean(wf)
		if wf != "" && (rel == wf || strings.HasPrefix(rel, wf+"/")) {
			return id
		}
	}
	return ""
}

// ScratchRelPath is where this machine keeps per-session scratch for work
// that must run locally (CLI tool output, browser and tool-API commands) but
// belongs to a workflow on a remote server. It mirrors the workspace-api
// router's remote-scratch area, so nothing is recreated under Workflow/.
const ScratchRelPath = "_system/remote-scratch"

// LocalScratchDir maps a path inside a remote workflow to its local scratch
// directory. ok=false when workspacePath is local.
func LocalScratchDir(docsRoot, workspacePath string) (string, bool) {
	id := ServerFor(docsRoot, workspacePath)
	if id == "" {
		return "", false
	}
	rel := strings.TrimPrefix(workspaceRelativePath(docsRoot, workspacePath), "Workflow/")
	return filepath.Join(docsRoot, filepath.FromSlash(ScratchRelPath), id, filepath.FromSlash(rel)), true
}

// IsRemote reports whether workspacePath lives on a remote workspace server.
func IsRemote(docsRoot, workspacePath string) bool {
	return ServerFor(docsRoot, workspacePath) != ""
}

func workspaceRelativePath(docsRoot, workspacePath string) string {
	p := strings.TrimSpace(workspacePath)
	if filepath.IsAbs(p) {
		if rel, err := filepath.Rel(docsRoot, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			p = rel
		}
	}
	return clean(p)
}

func clean(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return strings.Trim(path.Clean("/"+filepath.ToSlash(p)), "/")
}
