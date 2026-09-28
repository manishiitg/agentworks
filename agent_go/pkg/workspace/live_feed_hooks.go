package workspace

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
)

// Successful workspace tool writes notify the corresponding right-pane views.
// The feed carries only a change notice; each view reads the saved file again.

func isReportFilePath(p string) bool {
	return strings.Contains("/"+strings.Trim(strings.TrimSpace(p), "/"), "/db/reports/")
}

// noteWorkspaceFileWrite publishes notices for graph and dashboard inputs.
func noteWorkspaceFileWrite(paths ...string) {
	for _, p := range paths {
		if isReportFilePath(p) {
			livefeed.PublishWorkflow(livefeed.Report, p)
		}
		livefeed.PublishPlanPath(p)
	}
}

// noteWorkflowDBWrite publishes a report notice for the workflow owning dbPath.
func noteWorkflowDBWrite(dbPath string) {
	livefeed.PublishWorkflow(livefeed.Report, dbPath)
}
