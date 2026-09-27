package server

import (
	"context"
	"strings"
	"testing"
)

// A multi-file "*** Begin Patch" names its files inside the diff. The write
// guard must check every one of them, or a patch whose filepath is allowed
// could still write a blocked file such as a non-owner's workflow.json.
func TestWorkflowPhaseGuardChecksEveryFileOfAMultiFilePatch(t *testing.T) {
	called := false
	executors := map[string]func(context.Context, map[string]interface{}) (string, error){
		"diff_patch_workspace_file": func(context.Context, map[string]interface{}) (string, error) {
			called = true
			return "ok", nil
		},
	}
	wrapped := wrapExecutorsWithWorkflowPhaseFolderGuard(executors, "Workflow/wf", nil, []string{"Workflow/wf/workflow.json"})
	patch := "*** Begin Patch\n" +
		"*** Update File: Workflow/wf/code/main.py\n-a\n+b\n" +
		"*** Update File: Workflow/wf/workflow.json\n-\"owner\": \"alice\"\n+\"owner\": \"bob\"\n" +
		"*** End Patch\n"
	_, err := wrapped["diff_patch_workspace_file"](context.Background(), map[string]interface{}{
		"filepath": "Workflow/wf/code/main.py",
		"diff":     patch,
	})
	if err == nil || !strings.Contains(err.Error(), "workflow.json") || called {
		t.Fatalf("err = %v, executor called = %v; want workflow.json refused before the patch runs", err, called)
	}

	allowed := "*** Begin Patch\n*** Update File: Workflow/wf/code/main.py\n-a\n+b\n*** Update File: Workflow/wf/code/lib.py\n-c\n+d\n*** End Patch\n"
	if _, err := wrapped["diff_patch_workspace_file"](context.Background(), map[string]interface{}{"diff": allowed}); err != nil || !called {
		t.Fatalf("an all-allowed multi-file patch must run: err = %v", err)
	}
}
