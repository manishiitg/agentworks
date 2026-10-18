package guidance

import (
	"testing"
)

func TestTriggerReferencesUseOneWorkflowRetentionCount(t *testing.T) {
	for kind, wants := range map[string][]string{
		"schedules":        {"run_retention_count", "completed `-sched` runs", "completed `-hook` runs", "artifacts_expired=true"},
		"webhook-triggers": {"run_retention_count", "default 10", "same-sized Builder and saved-schedule families"},
		"workflow-tools":   {"same count independently", "`-sched` runs", "`-hook` runs", "paired evaluation cleanup"},
	} {
		rendered, err := renderFromRegistry(kind, tmplData{}, referenceKinds)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range wants {
			if !containsNormalizedText(rendered, want) {
				t.Errorf("%s missing unified retention contract %q", kind, want)
			}
		}
	}
}
