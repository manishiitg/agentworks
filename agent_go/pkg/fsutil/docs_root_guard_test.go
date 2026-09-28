package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docsRootAllowlist freezes the files that still read workspace-docs straight
// from local disk. Workflow data must go through the workspace API so a
// workflow placed on a remote workspace server works (see
// docs/core/remote_workspace_server_plan.html §10). Migrating a file removes
// it from this list; adding one needs a reason: laptop-local state only.
var docsRootAllowlist = map[string]bool{
	// Existing direct-disk callers inherited from main at rebase time. These
	// still need migration for remote workflows; freezing the baseline here
	// prevents unrelated main additions from hiding new branch regressions.
	"cmd/server/cli_landlock.go":                                                           true,
	"cmd/server/crew_cli_runtime.go":                                                       true,
	"cmd/server/crew_move_command.go":                                                      true,
	"cmd/server/crew_shared_access.go":                                                     true,
	"cmd/server/external_builder_workspace.go":                                             true,
	"cmd/server/knowledgebase_routes.go":                                                   true,
	"cmd/server/product_owner.go":                                                          true,
	"cmd/server/project_cli_runtime.go":                                                    true,
	"cmd/server/secret_selection_migration.go":                                             true,
	"cmd/server/virtual-tools/script_db_tools.go":                                          true,
	"cmd/server/workspace_proxy.go":                                                        true,
	"pkg/costledger/workflow_scope.go":                                                     true,
	"cmd/server/access_tokens.go":                                                          true,
	"cmd/server/chat_history_dedupe_migration.go":                                          true,
	"cmd/server/chat_history_persistence.go":                                               true,
	"cmd/server/coding_agent_modes.go":                                                     true,
	"cmd/server/durable_chat_migration_command.go":                                         true,
	"cmd/server/external_file_reads.go":                                                    true,
	"cmd/server/mcp_oauth_store.go":                                                        true,
	"cmd/server/pulse_crew_calls_tool.go":                                                  true,
	"cmd/server/pulse_step_concerns.go":                                                    true,
	"cmd/server/report_human_inputs.go":                                                    true,
	"cmd/server/scheduler.go":                                                              true,
	"cmd/server/server.go":                                                                 true,
	"cmd/server/services/gmail_connections.go":                                             true,
	"cmd/server/services/org_dashboard_connector.go":                                       true,
	"cmd/server/virtual-tools/tool_costs.go":                                               true,
	"cmd/server/virtual-tools/workspace_advanced_tools.go":                                 true,
	"cmd/server/virtual-tools/workspace_document_paths.go":                                 true,
	"cmd/server/workflow_builder_chat_guard.go":                                            true,
	"cmd/server/workflow_cli_isolation.go":                                                 true,
	"cmd/server/workflow_review_data.go":                                                   true,
	"cmd/server/workflow.go":                                                               true,
	"cmd/testing/read_image_paths.go":                                                      true,
	"pkg/costledger/workspace_ledger.go":                                                   true,
	"pkg/loopclosure/loopclosure.go":                                                       true,
	"pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go": true,
	"pkg/orchestrator/agents/workflow/step_based_workflow/plan_change_backlog.go":          true,
	"pkg/orchestrator/agents/workflow/step_based_workflow/plan_drift_candidates.go":        true,
	"pkg/orchestrator/agents/workflow/step_based_workflow/plan_drift_checks.go":            true,
	"pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_details.go":        true,
	"pkg/orchestrator/agents/workflow/step_based_workflow/run_concerns.go":                 true,
	"pkg/orchestrator/base_orchestrator_agent_factory.go":                                  true,
	"pkg/pulseintake/runtime.go":                                                           true,
}

var docsRootCall = regexp.MustCompile(`\b(WorkspaceDocsRoot|getWorkspaceDocsAbsPath)\(\)`)

func TestNoNewDirectDocsRootAccess(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == "pkg/fsutil/atomic.go" || docsRootAllowlist[rel] {
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 -- walking this module's own sources
		if err != nil {
			return err
		}
		if docsRootCall.Match(data) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("new direct workspace-docs access via WorkspaceDocsRoot()/getWorkspaceDocsAbsPath() in %v; read workflow data through the workspace API instead (or add laptop-local state to docsRootAllowlist with a reason)", offenders)
	}
}
