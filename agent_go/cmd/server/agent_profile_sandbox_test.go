package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestAgentProfileReadOnlyFolders(t *testing.T) {
	workflowRO := []string{"Workflow/demo/planning/"}
	got := agentProfileReadOnlyFolders(agentprofiles.SandboxPolicy{}, workflowRO)
	if strings.Join(got, ",") != "subagents/,Workflow/demo/planning/" {
		t.Fatalf("default read-only set = %v", got)
	}
	got = agentProfileReadOnlyFolders(agentprofiles.SandboxPolicy{ReadOnly: []string{"Downloads", " reports/ ", ""}}, workflowRO)
	if strings.Join(got, ",") != "Downloads/,reports/,Workflow/demo/planning/" {
		t.Fatalf("declared read-only set = %v", got)
	}
	got = agentProfileReadOnlyFolders(agentprofiles.SandboxPolicy{ReadOnly: []string{}}, workflowRO)
	if strings.Join(got, ",") != "Workflow/demo/planning/" {
		t.Fatalf("an explicit empty ambient list must retain authorized workflow context: %v", got)
	}
}
