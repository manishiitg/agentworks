package server

import "testing"

// PLAT-159. A workflow's stable folder identity (e.g. "social-media") and its
// configured display label (e.g. "twitter-automation") can differ. Using the
// folder name as the label made the same running session look like a
// different workflow wherever the UI reads the label instead of the folder
// name — the Global Activity Monitor pill, in particular, showed a session's
// own workflow as if it were a separate one running elsewhere.
func TestWorkflowDisplayLabelPrefersManifestLabel(t *testing.T) {
	manifest := &WorkflowManifest{Label: "twitter-automation"}
	got := workflowDisplayLabel("Workflow/social-media", manifest)
	if got != "twitter-automation" {
		t.Errorf("label = %q, want the configured manifest label, not the folder name", got)
	}
}
