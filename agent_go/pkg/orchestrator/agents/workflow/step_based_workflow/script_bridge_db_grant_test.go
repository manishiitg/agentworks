package step_based_workflow

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// A scripted step's script writes through the bridge as its group session. The
// group session copies folder capabilities from its parent but never the DB
// grant, so before the grant its writes were refused (salesoutreach
// company-search, 2026-09-28..30).
func TestScriptBridgeSessionGetsTheStepsDBGrant(t *testing.T) {
	const parent, group = "schedule-cron--parent-x", "session-group-usa-engineering-ops-1"
	common.SetSessionFolderGuard(parent, []string{"Workflow/w"}, []string{"Workflow/w"})
	ConfigureManagedWorkflowDBSession(parent, "Workflow/w", true)
	common.CopySessionFolderGuard(parent, group)
	t.Cleanup(func() { common.ClearSessionShellConfig(parent); common.ClearSessionShellConfig(group) })

	if got := common.GetSessionShellConfig(group).Env[workflowDBAccessEnv]; got != "" {
		t.Fatalf("precondition: the group copy carries no DB grant, got %q", got)
	}
	grantScriptBridgeSessionDB(group, "Workflow/w", DBAccessReadWrite)
	if got := common.GetSessionShellConfig(group).Env[workflowDBAccessEnv]; got != DBAccessReadWrite {
		t.Fatalf("the script's session must carry the step's grant, got %q", got)
	}
	// Only the grant is added: raw database access stays blocked for the session.
	cfg := common.GetSessionShellConfig(group)
	blocked := false
	for _, p := range cfg.BlockedPaths {
		if p == "Workflow/w/db/db.sqlite" {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("db.sqlite must stay blocked for the group session, blocked=%v", cfg.BlockedPaths)
	}
}

// A read-only step's grant is read-only; no session and no access grant nothing.
func TestScriptBridgeSessionGrantNeverWidens(t *testing.T) {
	const group = "session-group-ro-1"
	t.Cleanup(func() { common.ClearSessionShellConfig(group) })
	grantScriptBridgeSessionDB(group, "Workflow/w", DBAccessRead)
	if got := common.GetSessionShellConfig(group).Env[workflowDBAccessEnv]; got != DBAccessRead {
		t.Fatalf("a read step grants read, got %q", got)
	}
	grantScriptBridgeSessionDB("", "Workflow/w", DBAccessReadWrite)
	grantScriptBridgeSessionDB("session-group-none-1", "Workflow/w", "")
	if common.GetSessionShellConfig("session-group-none-1") != nil {
		t.Fatal("an empty access must not create a session config")
	}
}
