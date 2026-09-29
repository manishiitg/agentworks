package server

import (
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestWorkflowPhaseCLISetupPreservesAuthenticatedShellGuard(t *testing.T) {
	const workflow = "Workflow/conqa2test"
	for _, tc := range []struct {
		name            string
		readOnly        bool
		externalBuilder bool
		wantWrite       bool
	}{
		{name: "Slack Run turn", readOnly: true},
		{name: "Run turn with external operation", readOnly: true, externalBuilder: true},
		{name: "Builder turn", wantWrite: true},
		{name: "external Builder operation", externalBuilder: true, wantWrite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := "phase-cli-guard-" + tc.name
			t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
			writes := []string{"Downloads"}
			if !tc.readOnly {
				writes = append(writes, workflow)
			}
			common.SetSessionFolderGuard(sessionID, []string{workflow, "Downloads"}, writes)
			configureWorkflowPhaseCLIShellGuard(sessionID, workflow, "owner", tc.externalBuilder, tc.readOnly)
			guard := common.GetSessionShellConfig(sessionID)
			if got := slices.Contains(guard.WritePaths, workflow) || slices.Contains(guard.WritePaths, workflow+"/"); got != tc.wantWrite {
				t.Fatalf("workflow write grant = %v, want %v: %+v", got, tc.wantWrite, guard)
			}
			if !slices.Contains(guard.ReadPaths, workflow) && !slices.Contains(guard.ReadPaths, workflow+"/") {
				t.Fatalf("workflow read access lost: %+v", guard)
			}
		})
	}
}
