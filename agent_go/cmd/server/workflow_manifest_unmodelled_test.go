package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A Crew's workflow.json has top-level fields WorkflowManifest does not model. Rewriting it through the struct dropped its
// internal `triggers`, so a workflow step that called the Crew died with "internal trigger not found" (server C, 2026-10-07).
func TestRewritingAManifestKeepsFieldsTheStructDoesNotModel(t *testing.T) {
	stub, _ := newScheduleRunWorkspaceStub(t)
	ws := "Crew/blueprint-1"
	stub.files[manifestPath(ws)] = `{"schema_version":1,"id":"crew-1","label":"Blueprint","capabilities":{},"workflow_context_paths":["Workflow/login"],"identity":{"name":"Nova"},"triggers":[{"id":"a41c0b2e","kind":"internal","enabled":true,"caller":{"id":"wf_1","type":"workflow"}}]}`
	manifest, exists, err := ReadWorkflowManifest(context.Background(), ws)
	if err != nil || !exists {
		t.Fatalf("read: %v exists=%v", err, exists)
	}
	manifest.WorkflowContextPaths = nil // a modelled field cleared on purpose must stay cleared
	if err := WriteWorkflowManifest(context.Background(), ws, manifest); err != nil {
		t.Fatal(err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stub.files[manifestPath(ws)]), &written); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written["triggers"]), "a41c0b2e") || !strings.Contains(string(written["identity"]), "Nova") {
		t.Fatalf("the Crew's triggers or identity were dropped: triggers=%s identity=%s", written["triggers"], written["identity"])
	}
	if _, still := written["workflow_context_paths"]; still {
		t.Fatalf("a cleared modelled field came back: %s", written["workflow_context_paths"])
	}
}
