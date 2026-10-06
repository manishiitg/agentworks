package server

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// graphStrictMode reads AGENTWORKS_GRAPH_STRICT (PLAT-579): "enforce" (the
// default, everywhere) refuses a run or step whose inputs cannot be read,
// "warn" only logs it and tells the builder, "off" skips the check. Owner,
// 2026-10-06: a broken input graph always fails.
func graphStrictMode() string {
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTWORKS_GRAPH_STRICT"))); mode {
	case "off", "warn":
		return mode
	}
	return "enforce"
}

// workflowGraphPreflight checks the input and output graph before a step
// (stepID set) or a whole workflow run starts. It returns an error only in
// enforce mode; in warn mode it returns the text the caller may show the
// builder. Any failure to read the workflow never blocks a run.
func workflowGraphPreflight(workspacePath, stepID string) (notice string, err error) {
	mode := graphStrictMode()
	if mode == "off" || strings.TrimSpace(workspacePath) == "" {
		return "", nil
	}
	issues, readErr := step_based_workflow.GraphPreflight(workspacePath, stepID)
	if readErr != nil || len(issues) == 0 {
		return "", nil
	}
	detail := step_based_workflow.GraphPreflightMessage(issues)
	if mode == "enforce" {
		scope := "the workflow"
		if stepID != "" {
			scope = "step " + stepID
		}
		return "", fmt.Errorf("workflow_graph_check_failed: %s has inputs that cannot be read, so nothing was started:%s\nFix: list each file in its producer's context_output (comma-separated) and keep the consumer's dependency a bare name, or remove the dependency. Set AGENTWORKS_GRAPH_STRICT=warn to run anyway", scope, detail)
	}
	log.Printf("[GRAPH STRICT] would refuse %s in enforce mode:%s", workspacePath+"/"+stepID, detail)
	return "Graph check (this would be refused in enforce mode):" + detail, nil
}
