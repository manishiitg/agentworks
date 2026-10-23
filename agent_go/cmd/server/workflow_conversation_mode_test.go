package server

import "testing"

func TestWorkflowConversationModeFollowsAccess(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		old := &ExecutionOptions{WorkshopMode: "run", SelectedRunFolder: "iteration-85", ResumeFromStep: 3}
		req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/testing", ExecutionOptions: old}
		normalizeWorkflowConversationMode(&req, readOnly)
		want := "workshop"
		if readOnly {
			want = "run"
		}
		if req.ExecutionOptions.WorkshopMode != want || workflowCLIMode(&req, readOnly) != want {
			t.Fatalf("readOnly=%v: metadata/CLI mode=%q", readOnly, req.ExecutionOptions.WorkshopMode)
		}
		if req.ExecutionOptions.SelectedRunFolder != "iteration-85" || req.ExecutionOptions.ResumeFromStep != 3 || old.WorkshopMode != "run" {
			t.Fatal("changed execution options or retained request")
		}
	}
	reader := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", ExecutionOptions: &ExecutionOptions{WorkshopMode: "workshop"}}
	normalizeWorkflowConversationMode(&reader, true)
	if reader.ExecutionOptions.WorkshopMode != "run" {
		t.Fatal("demotion retained Builder metadata")
	}
	owner := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder"}
	normalizeWorkflowConversationMode(&owner, false)
	if owner.ExecutionOptions == nil || owner.ExecutionOptions.WorkshopMode != "workshop" {
		t.Fatal("missing options lost resolved Builder mode")
	}
}

func TestWorkflowConversationModeLeavesOtherRuntimesAlone(t *testing.T) {
	for _, req := range []QueryRequest{
		{AgentMode: "workflow"},
		{AgentMode: "workflow_phase", PhaseID: "execution"},
		{AgentMode: "workflow_phase", PhaseID: "workflow-builder", AgentProfileID: "crew-agent"},
	} {
		normalizeWorkflowConversationMode(&req, false)
		if req.ExecutionOptions != nil {
			t.Fatal("changed a separate runtime")
		}
	}
}
