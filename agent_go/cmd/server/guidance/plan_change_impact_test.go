package guidance

import (
	"strings"
	"testing"
)

func TestInteractivePlanEditsDoNotRequireFullDriftAudit(t *testing.T) {
	for _, kind := range []string{"plan-change-impact", "optimize-playbook", "workflow-tools"} {
		t.Run(kind, func(t *testing.T) {
			rendered, err := renderFromRegistry(kind, tmplData{}, referenceKinds)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"one combined compatibility check", "current agent", "scheduled Autopilot or an explicit user request"} {
				if !strings.Contains(rendered, want) {
					t.Errorf("%s lacks interactive edit policy %q", kind, want)
				}
			}
		})
	}

	impact, err := renderFromRegistry("plan-change-impact", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Stop when the changed step and its affected consumers are compatible",
		"Do not scan unrelated routes",
		"Missing audit receipts alone are not a test blocker",
		"if no artifacts changed, a retry needs no new dependency review",
		"do not run six audits just to populate these fields",
	} {
		if !strings.Contains(impact, want) {
			t.Errorf("impact guide missing bounded review rule %q", want)
		}
	}

	audit, err := renderFromRegistry("run-plan-drift", tmplData{}, allKinds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"A stale `drift_review.needs_review` flag alone does not require this audit before testing",
		"Merely reading this guide during an edit does not authorize launching a background audit",
		"record_plan_drift_review",
	} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit guide missing %q", want)
		}
	}
	if strings.Contains(audit, "record_pulse_result") || strings.Contains(audit, "manual=true") {
		t.Fatal("audit still requires the retired reviewer handshake")
	}
}
