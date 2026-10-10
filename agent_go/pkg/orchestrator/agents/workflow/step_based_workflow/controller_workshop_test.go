package step_based_workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestWorkshopStepLogFolderUsesDeclaredStepID(t *testing.T) {
	if got := workshopStepLogFolder("  compile-report  "); got != "compile-report" {
		t.Fatalf("workshopStepLogFolder() = %q, want declared step ID", got)
	}
	if got := workshopStepLogFolder(""); got != "" {
		t.Fatalf("workshopStepLogFolder() invented fallback %q for empty step ID", got)
	}
}

func TestSwitchWorkshopGroupSessionCachesPerGroup(t *testing.T) {
	t.Setenv("MCP_API_URL", "http://example.test")

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"",
		0,
		"",
		nil,
		nil,
		false,
		&orchestrator.LLMConfig{},
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}

	envRef := map[string]string{
		"MCP_API_TOKEN":  "test-token",
		"MCP_API_URL":    "http://example.test/s/original-session",
		"MCP_SESSION_ID": "original-session",
	}
	base.SetWorkspaceEnvRef(envRef)

	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:         base,
		workshopGroupSessionIDs:  make(map[string]string),
		workshopGroupSessionRefs: make(map[string]int),
		workshopGroupLastUsed:    make(map[string]time.Time),
	}

	releaseGroup1, err := hcpo.switchWorkshopGroupSession("group-1")
	if err != nil {
		t.Fatalf("switchWorkshopGroupSession(group-1) returned error: %v", err)
	}
	defer releaseGroup1()
	group1Session := hcpo.GetMCPSessionID()
	if !strings.Contains(group1Session, "session-group-group-1-") {
		t.Fatalf("expected group-1 session id, got %q", group1Session)
	}
	if envRef["MCP_SESSION_ID"] != group1Session {
		t.Fatalf("expected workspace env MCP_SESSION_ID %q, got %q", group1Session, envRef["MCP_SESSION_ID"])
	}
	if got := envRef["MCP_CUSTOM"]; got != "http://example.test/s/"+group1Session+"/tools/custom" {
		t.Fatalf("expected workspace env MCP_CUSTOM for group session, got %q", got)
	}
	if got := envRef["MCP_AUTH"]; got != "Authorization: Bearer test-token" {
		t.Fatalf("expected workspace env MCP_AUTH, got %q", got)
	}

	releaseGroup1Again, err := hcpo.switchWorkshopGroupSession("group-1")
	if err != nil {
		t.Fatalf("switchWorkshopGroupSession(group-1) second call returned error: %v", err)
	}
	defer releaseGroup1Again()
	if hcpo.GetMCPSessionID() != group1Session {
		t.Fatalf("expected group-1 session to be reused, got %q want %q", hcpo.GetMCPSessionID(), group1Session)
	}

	releaseGroup2, err := hcpo.switchWorkshopGroupSession("group-2")
	if err != nil {
		t.Fatalf("switchWorkshopGroupSession(group-2) returned error: %v", err)
	}
	defer releaseGroup2()
	group2Session := hcpo.GetMCPSessionID()
	if !strings.Contains(group2Session, "session-group-group-2-") {
		t.Fatalf("expected group-2 session id, got %q", group2Session)
	}
	if group2Session == group1Session {
		t.Fatalf("expected different session ids for different groups, both were %q", group2Session)
	}

	if len(hcpo.workshopGroupSessionIDs) != 2 {
		t.Fatalf("expected 2 cached group sessions, got %d", len(hcpo.workshopGroupSessionIDs))
	}
}

func TestSwitchWorkshopGroupSessionRejectsThirdActiveGroup(t *testing.T) {
	t.Setenv("MCP_API_URL", "http://example.test")

	origLimit := browser.MaxBrowserSessionsPerWorkflow
	browser.MaxBrowserSessionsPerWorkflow = 2
	defer func() { browser.MaxBrowserSessionsPerWorkflow = origLimit }()

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"",
		0,
		"",
		nil,
		nil,
		false,
		&orchestrator.LLMConfig{},
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}

	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:         base,
		workshopGroupSessionIDs:  make(map[string]string),
		workshopGroupSessionRefs: make(map[string]int),
		workshopGroupLastUsed:    make(map[string]time.Time),
	}

	releaseGroup1, err := hcpo.switchWorkshopGroupSession("group-1")
	if err != nil {
		t.Fatalf("switchWorkshopGroupSession(group-1) returned error: %v", err)
	}
	defer releaseGroup1()

	releaseGroup2, err := hcpo.switchWorkshopGroupSession("group-2")
	if err != nil {
		t.Fatalf("switchWorkshopGroupSession(group-2) returned error: %v", err)
	}
	defer releaseGroup2()

	if _, err := hcpo.switchWorkshopGroupSession("group-3"); err == nil {
		t.Fatal("expected third active workshop group session to be rejected")
	} else if !strings.Contains(err.Error(), "max 2") {
		t.Fatalf("expected max-2 error, got %v", err)
	}
}

func TestApplyWorkshopExecuteOptionsPropagatesGroupName(t *testing.T) {
	t.Setenv("MCP_API_URL", "http://example.test")

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"",
		0,
		"",
		nil,
		nil,
		false,
		&orchestrator.LLMConfig{},
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.SetWorkspaceEnvRef(map[string]string{
		"MCP_API_TOKEN":  "test-token",
		"MCP_API_URL":    "http://example.test/s/original-session",
		"MCP_SESSION_ID": "original-session",
	})

	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:         base,
		workshopGroupSessionIDs:  make(map[string]string),
		workshopGroupSessionRefs: make(map[string]int),
		workshopGroupLastUsed:    make(map[string]time.Time),
		variablesManifest: &VariablesManifest{
			Groups: []VariableGroup{
				{Name: "customer-b", Values: map[string]string{"acct": "customer-b"}},
				{Name: "xspaces", Values: map[string]string{"acct": "xspaces"}},
			},
		},
	}

	// RunFolder is explicit so no workspace listing is needed.
	release, err := hcpo.applyWorkshopExecuteOptions(context.Background(), &WorkshopExecuteOptions{
		GroupName: "xspaces",
		RunFolder: "iteration-0/xspaces",
	})
	if err != nil {
		t.Fatalf("applyWorkshopExecuteOptions returned error: %v", err)
	}
	if hcpo.currentGroupName != "xspaces" {
		t.Fatalf("currentGroupName = %q, want %q (crew delivery keys lose their group segment)", hcpo.currentGroupName, "xspaces")
	}
	release()
	if hcpo.currentGroupName != "" {
		t.Fatalf("currentGroupName = %q after release, want empty (group leaked between execute_step calls)", hcpo.currentGroupName)
	}
}
