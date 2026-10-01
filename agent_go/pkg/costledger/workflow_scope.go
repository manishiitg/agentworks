package costledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// A draft's cost view includes its published versions. An individual version's
// view remains exact, preserving version/run attribution and historical rows.
func workflowReleasePrefix(workflow string) string {
	parts := strings.Split(workflow, "/")
	if len(parts) != 2 || parts[0] != "Workflow" || parts[1] == "" || strings.HasPrefix(parts[1], ".") {
		return ""
	}
	return workflowtypes.RelayReleaseRoot(workflow) + "/"
}

func workflowCostMatches(wanted, actual string) bool {
	if wanted == actual {
		return true
	}
	prefix := workflowReleasePrefix(wanted)
	return prefix != "" && strings.HasPrefix(actual, prefix) && workflowtypes.RelayReleaseWorkspace(actual) == actual
}

func workflowCostSQL(workflow string) (string, []any) {
	if prefix := workflowReleasePrefix(workflow); prefix != "" {
		return "(workflow_id = ? OR workflow_id GLOB ?)", []any{workflow, prefix + "v[1-9]*"}
	}
	return "workflow_id = ?", []any{workflow}
}

// WorkspaceCostCopies keeps the per-version ledger and also mirrors its rows
// into the owning draft's ledger, so existing Builder cost tools see all spend.
// Only server-written metadata whose namespace hashes back to that draft can
// add a destination; malformed metadata never redirects a ledger write.
func WorkspaceCostCopies(workspace string) []string {
	paths := []string{workspace}
	if workflowtypes.RelayReleaseWorkspace(workspace) != workspace {
		return paths
	}
	raw, err := os.ReadFile(filepath.Join(fsutil.WorkspaceDocsRoot(), workspace, "release.json"))
	if err != nil {
		return paths
	}
	var release struct {
		Source  string `json:"source_workspace"`
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &release) != nil || workflowReleasePrefix(release.Source) == "" {
		return paths
	}
	if workflowtypes.RelayReleaseRoot(release.Source)+"/"+release.Version == workspace {
		paths = append(paths, release.Source)
	}
	return paths
}
