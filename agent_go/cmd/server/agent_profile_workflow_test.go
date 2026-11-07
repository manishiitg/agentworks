package server

import "testing"

func TestProfileRequiresWorkflowRuntime(t *testing.T) {
	if profileRequiresWorkflowRuntime(nil) {
		t.Fatal("nil profile requires workflow runtime")
	}
	resolved := &resolvedAgentProfile{}
	if profileRequiresWorkflowRuntime(resolved) {
		t.Fatal("empty capability requires workflow runtime")
	}
	resolved.Definition.Runtime.Capabilities.WorkflowExecution = "required"
	if !profileRequiresWorkflowRuntime(resolved) {
		t.Fatal("required capability did not enable workflow runtime")
	}
}
