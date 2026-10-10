package step_based_workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type fakeDirectLearningAgent struct {
	cfg *agents.OrchestratorAgentConfig
}

func (f *fakeDirectLearningAgent) Execute(context.Context, map[string]string, []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	return "", nil, nil
}

func (f *fakeDirectLearningAgent) GetType() string { return "fake" }

func (f *fakeDirectLearningAgent) GetConfig() *agents.OrchestratorAgentConfig { return f.cfg }

func (f *fakeDirectLearningAgent) Initialize(context.Context) error { return nil }

func (f *fakeDirectLearningAgent) Close() error { return nil }

func (f *fakeDirectLearningAgent) GetBaseAgent() *agents.BaseAgent { return nil }

func TestBuildLearningsContributionTurnRequiresPatchToolForAllWrites(t *testing.T) {
	targetPath := "/tmp/workspace-docs/Workflow/social-media/learnings/_global"
	msg := BuildLearningsContributionTurnWithTarget("unfollow-cleanup", "Unfollow stale X accounts.", "Capture the unfollow flow.", false, targetPath)
	for _, want := range []string{
		"Use `diff_patch_workspace_file` for every write",
		"including creating a new `/tmp/workspace-docs/Workflow/social-media/learnings/_global/references/<topic>.md` file",
		"Do not use shell redirection, heredocs, tee, Python",
		"`execute_shell_command` for read-only inspection",
		"cat '/tmp/workspace-docs/Workflow/social-media/learnings/_global/SKILL.md'",
		"do not rely on your shell working directory",
		// Negative store boundary: learnings hold HOW, not discovered facts/results.
		"HOW only — not facts or results",
		"never in learnings",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("learning prompt missing %q:\n%s", want, msg)
		}
	}
	for _, forbidden := range []string{
		"heredoc creation",
	} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("learning prompt still contains stale write guidance %q:\n%s", forbidden, msg)
		}
	}
}

func TestBuildLearningsContributionTurnIncludesDurableBrowserSelectorContract(t *testing.T) {
	msg := BuildLearningsContributionTurnWithTargetAndBrowser(
		"post-update",
		"Publish an update through the authenticated browser.",
		"Capture the durable compose and publish flow.",
		false,
		"/tmp/workspace-docs/Workflow/demo/learnings/_global",
		true,
	)
	for _, want := range []string{
		"Never persist snapshot refs",
		"stable-hook inventory",
		"semantic action recipes",
		"hand-written semantic `id`/`name`",
		"role + accessible name",
		"Store classes only when verified hand-written and stable",
		"Mark unverified fallbacks as candidates",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("browser learning prompt missing %q:\n%s", want, msg)
		}
	}
}

func TestBuildLearningsContributionTurnOmitsBrowserContractWithoutCapability(t *testing.T) {
	msg := BuildLearningsContributionTurn("fetch-api", "Fetch API data.", "Capture retry behavior.", false)
	if strings.Contains(msg, "stable-hook inventory") {
		t.Fatalf("non-browser learning prompt contains browser-only contract:\n%s", msg)
	}
}

func TestPrepareDirectLearningTurnKeepsWorkingDirAndRestoresSession(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		[]string{"test-server"}, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/social-media")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}

	sessionID := "direct-learning-test-session"
	defer common.ClearSessionShellConfig(sessionID)
	originalRead := []string{"Workflow/social-media/runs/iteration-0/execution"}
	originalWrite := []string{"Workflow/social-media/runs/iteration-0/execution/step-unfollow"}
	originalWorkingDir := "Workflow/social-media/runs/iteration-0/execution"
	common.SetSessionFolderGuard(sessionID, originalRead, originalWrite)
	common.SetSessionWorkingDir(sessionID, originalWorkingDir)

	cfg := &agents.OrchestratorAgentConfig{
		MCPSessionID:          sessionID,
		FolderGuardReadPaths:  append([]string{}, originalRead...),
		FolderGuardWritePaths: append([]string{}, originalWrite...),
	}
	agent := &fakeDirectLearningAgent{cfg: cfg}
	globalLearningsPath := "Workflow/social-media/learnings/_global"

	restore := hcpo.prepareDirectLearningTurn(agent, []string{globalLearningsPath})

	sessionCfg := common.GetSessionShellConfig(sessionID)
	if sessionCfg == nil {
		t.Fatal("expected session config to exist")
	}
	if sessionCfg.WorkingDir != originalWorkingDir {
		t.Fatalf("direct-learning changed cwd = %q, want unchanged %q", sessionCfg.WorkingDir, originalWorkingDir)
	}
	if !testContainsString(sessionCfg.ReadPaths, globalLearningsPath) || !testContainsString(sessionCfg.WritePaths, globalLearningsPath) {
		t.Fatalf("session guard missing global learnings path: read=%v write=%v", sessionCfg.ReadPaths, sessionCfg.WritePaths)
	}
	if !testContainsString(cfg.FolderGuardReadPaths, globalLearningsPath) || !testContainsString(cfg.FolderGuardWritePaths, globalLearningsPath) {
		t.Fatalf("agent guard missing global learnings path: read=%v write=%v", cfg.FolderGuardReadPaths, cfg.FolderGuardWritePaths)
	}

	restore()

	sessionCfg = common.GetSessionShellConfig(sessionID)
	if sessionCfg.WorkingDir != originalWorkingDir {
		t.Fatalf("restored cwd = %q, want %q", sessionCfg.WorkingDir, originalWorkingDir)
	}
	if strings.Join(sessionCfg.ReadPaths, "\x00") != strings.Join(originalRead, "\x00") {
		t.Fatalf("restored session read paths = %v, want %v", sessionCfg.ReadPaths, originalRead)
	}
	if strings.Join(sessionCfg.WritePaths, "\x00") != strings.Join(originalWrite, "\x00") {
		t.Fatalf("restored session write paths = %v, want %v", sessionCfg.WritePaths, originalWrite)
	}
	if strings.Join(cfg.FolderGuardReadPaths, "\x00") != strings.Join(originalRead, "\x00") {
		t.Fatalf("restored agent read paths = %v, want %v", cfg.FolderGuardReadPaths, originalRead)
	}
	if strings.Join(cfg.FolderGuardWritePaths, "\x00") != strings.Join(originalWrite, "\x00") {
		t.Fatalf("restored agent write paths = %v, want %v", cfg.FolderGuardWritePaths, originalWrite)
	}
}

func TestPrepareDirectLearningTurnCreatesTemporarySessionConfig(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		[]string{"test-server"}, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/social-media")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}

	sessionID := "direct-learning-new-session"
	common.ClearSessionShellConfig(sessionID)
	cfg := &agents.OrchestratorAgentConfig{MCPSessionID: sessionID}
	agent := &fakeDirectLearningAgent{cfg: cfg}
	globalLearningsPath := "Workflow/social-media/learnings/_global"

	restore := hcpo.prepareDirectLearningTurn(agent, []string{globalLearningsPath})
	sessionCfg := common.GetSessionShellConfig(sessionID)
	if sessionCfg == nil {
		t.Fatal("expected temporary session config to be created")
	}
	if sessionCfg.WorkingDir != "" {
		t.Fatalf("temporary cwd = %q, want empty/unchanged", sessionCfg.WorkingDir)
	}
	if !testContainsString(sessionCfg.WritePaths, globalLearningsPath) {
		t.Fatalf("temporary session write guard missing global learnings path: %v", sessionCfg.WritePaths)
	}

	restore()
	if got := common.GetSessionShellConfig(sessionID); got != nil {
		t.Fatalf("temporary session config was not cleared: %+v", got)
	}
}

func testContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Regression for the message_sequence learnings-write sandbox denial: the reused
// execution agent freezes its workspace-write guard at creation, so the snapshot
// is built from the step's FULL granted write scope (messageSequenceStepFullWriteAccess)
// — not the first item's — or the learnings/KB closing turns are denied. This guards
// that a learnings-read-write step yields Learnings=true (→ learnings/_global in the
// frozen snapshot) while a read-only step does not.
func TestMessageSequenceStepFullWriteAccessGrantsLearningsForRWStep(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		[]string{"test-server"}, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/customer-qa-testing")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}

	rwStep := &MessageSequencePlanStep{
		CommonStepFields: CommonStepFields{ID: "survey-app-and-refresh-knowledge"},
		AgentConfigs:     &AgentConfigs{LearningsAccess: LearningsAccessReadWrite, LearningObjective: "capture the refresh flow"},
	}
	if !hcpo.messageSequenceStepFullWriteAccess(rwStep).Learnings {
		t.Fatal("learnings-read-write step must grant Learnings in the frozen snapshot scope")
	}

	roStep := &MessageSequencePlanStep{
		CommonStepFields: CommonStepFields{ID: "survey"},
		AgentConfigs:     &AgentConfigs{LearningsAccess: LearningsAccessRead},
	}
	if hcpo.messageSequenceStepFullWriteAccess(roStep).Learnings {
		t.Fatal("learnings-read step must not grant Learnings write")
	}
}

// The learnings contribution turn fires after every learnings-writing step, so its
// fixed instruction overhead is per-step token cost. Guard against silent re-bloat;
// budgets carry modest headroom over the current trimmed size. If a genuinely needed
// rule pushes past budget, trim redundant rationale elsewhere rather than raising it.
func TestLearningsContributionTurnStaysWithinSizeBudget(t *testing.T) {
	tp := "/tmp/workspace-docs/Workflow/wf/learnings/_global"
	base := BuildLearningsContributionTurnWithTargetAndBrowser("s", "Do the thing.", "Capture the flow.", false, tp, false)
	brow := BuildLearningsContributionTurnWithTargetAndBrowser("s", "Do the thing.", "Capture the flow.", false, tp, true)
	if len(base) > 4800 {
		t.Fatalf("learnings turn (no browser) grew to %d chars (budget 4800) — trim before adding", len(base))
	}
	if len(brow) > 7100 {
		t.Fatalf("learnings turn (browser) grew to %d chars (budget 7100) — trim before adding", len(brow))
	}
}

// The learnings turn is the only point where one agent has seen the execution
// trail, the step description, the binding constraints and the existing learnings
// together — and learnings is the only store it can write. Without an explicit
// escalation line, a real cross-store contradiction gets written up as a caveat
// inside a reference file (observed in production: a "step description says 1%,
// soul.md says 1.5%, prefer 1.5%" note buried in references/), which leaves the
// wrong copy in place and is invisible to whoever owns it.
func TestLearningsContributionTurnOffersConcernsChannel(t *testing.T) {
	msg := BuildLearningsContributionTurnWithTarget("s", "Do the thing.", "Capture the flow.", false,
		"/tmp/workspace-docs/Workflow/wf/learnings/_global")
	for _, want := range []string{
		"CONCERNS: <what contradicts what, naming both sources and the evidence path>",
		"contradicts the constraints",
		// Burying the finding next to the stale content is the exact failure mode.
		"Never leave a \"this is stale, use X\" caveat beside the wrong content",
		// Constraint values must be referenced, never copied into a durable store.
		"Never copy an owner constraint VALUE",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("learnings turn missing %q:\n%s", want, msg)
		}
	}
}
