package step_based_workflow

import (
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspacepathpolicy"
)

// ConfigureRelayDashboardBuilderSession is an authoring capability, independent
// of execution.platform_stores. Execution sessions still block every store.
func ConfigureRelayDashboardBuilderSession(sessionID, workspacePath string, readWrite bool) error {
	access := DBAccessRead
	if readWrite {
		access = DBAccessReadWrite
		// Landlock splits a parent grant around private children. Materialize
		// authored roots before compilation so new code/reports files work too.
		var grants []workspacepathpolicy.Grant
		for _, dir := range []string{"code", "docs", "db/reports", "db/assets", "db/migrations"} {
			grants = append(grants, workspacepathpolicy.Grant{Path: filepath.Join(workspacePath, dir), Lifecycle: workspacepathpolicy.PlatformManaged, Kind: workspacepathpolicy.Directory, Mode: 0o770})
		}
		if _, err := workspacepathpolicy.Materialize(GetPromptDocsRoot(), grants); err != nil {
			return err
		}
	}
	configureWorkflowDBSession(sessionID, workspacePath, access, false)
	blocked := common.GetSessionShellConfig(sessionID).BlockedPaths
	for _, p := range platformStoreBlockedPaths(workspacePath) {
		if p != filepath.Join(workspacePath, DBFolderName) {
			blocked = append(blocked, p)
		}
	}
	common.SetSessionFolderGuardBlockedPaths(sessionID, common.DeduplicateStrings(blocked))
	common.ReplaceSessionShellEnvPrefix(sessionID, "WORKFLOW_KB_", nil)
	common.SetSessionShellEnv(sessionID, map[string]string{"DB_PATH": "", "WORKFLOW_KB_ACCESS": KBAccessNone})
	return nil
}
