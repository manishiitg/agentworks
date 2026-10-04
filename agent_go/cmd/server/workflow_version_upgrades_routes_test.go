package server

import (
	"strings"
	"testing"
)

func TestRouteSummaryUpgradeFromPreviousContract(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.39"})
	if len(plan) != 4 || plan[0].to != "1.0.40" || plan[0].label != "upgrade-route-summaries" || plan[2].to != workflowContractNestedAgentArtifactsVersion || plan[3].to != workflowContractManagedDBScriptsVersion {
		t.Fatalf("route upgrade path: %+v", plan)
	}
	for _, want := range []string{"summary_routes", "routing_step_id", "route_summaries_json", "validate_report_html", "Do not execute the workflow or send a notification", "Do not stamp on a failed validation"} {
		if !strings.Contains(plan[0].query, want) {
			t.Errorf("missing migration boundary: %s", want)
		}
	}
}
