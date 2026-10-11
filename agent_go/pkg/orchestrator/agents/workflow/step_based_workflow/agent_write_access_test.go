package step_based_workflow

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// TestAgentAbsPath_IncludesWorkflowRoot guards the forward-pipe bug:
// the absolute path the message_sequence agent is handed (StepExecutionPath,
// item/code dirs) MUST include the workflow root (GetWorkspacePath, e.g.
// "Workflow/social-media"). Without it the agent writes to <docsRoot>/runs/...,
// outside its workflow folder, where downstream context_dependencies can't see
// the file.
func TestAgentAbsPath_IncludesWorkflowRoot(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		[]string{"test-server"}, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/social-media")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0"}

	stepExecRel := hcpo.agentSequenceExecutionRelPath("step-5", "step-report") // runs/iteration-0/execution/step-report
	got := hcpo.agentSequenceAbsPath(stepExecRel)
	want := filepath.Join(docsRoot, "Workflow/social-media", "runs", "iteration-0", "execution", "step-report")
	if got != want {
		t.Fatalf("agentSequenceAbsPath = %q, want %q (must include docsRoot + workflow root)", got, want)
	}
	if !strings.Contains(filepath.ToSlash(got), "Workflow/social-media") {
		t.Fatalf("agent-facing path is missing the workflow root: %q", got)
	}
}

func TestAgentExecutionRelPath_UsesNormalStepFolder(t *testing.T) {
	hcpo := &StepBasedWorkflowOrchestrator{selectedRunFolder: "iteration-0"}
	for _, tc := range []struct{ stepPath, stepID string }{
		{"step-5", "step-run-intent-orchestrator"},
		{"parent/agents/login/calls/call-1", "login-specialist"},
	} {
		got := hcpo.agentSequenceExecutionRelPath(tc.stepPath, tc.stepID)
		// Must equal the folder every other step writes to (execution/<stepID>) —
		// the folder downstream context_dependencies resolve against.
		want := filepath.Join("runs", "iteration-0", "execution", getArtifactFolderName(tc.stepID, tc.stepPath))
		if got != want {
			t.Fatalf("agentSequenceExecutionRelPath(%q,%q) = %q, want normal step folder %q", tc.stepPath, tc.stepID, got, want)
		}
		if strings.Contains(got, "message_sequences") {
			t.Fatalf("sequence still writes to isolated message_sequences folder: %q", got)
		}
	}
}

func TestAgentRouteSessionLivesAboveCallFolders(t *testing.T) {
	hcpo := &StepBasedWorkflowOrchestrator{selectedRunFolder: "iteration-0"}
	stepPath := "parent/agents/login/calls/call-1"
	want := filepath.Join("runs", "iteration-0", "execution", "parent", "agents", "login", "session.json")
	if got := hcpo.agentSequenceSessionPath(stepPath, "login-specialist"); got != want {
		t.Fatalf("agentSequenceSessionPath() = %q, want %q", got, want)
	}
}

func TestAgentRuntimeSessionIDStableForSequence(t *testing.T) {
	hcpo := &StepBasedWorkflowOrchestrator{
		selectedRunFolder: "iteration-0",
		currentGroupName:  "Acme Group",
	}

	session := &agentSequenceSession{}
	gotA := hcpo.agentSequenceRuntimeSessionID(session, "step-5", "review-specialist")
	session.runtime = &agentSequenceRuntime{SessionID: gotA}
	gotB := hcpo.agentSequenceRuntimeSessionID(session, "step-5", "review-specialist")
	if gotA != gotB {
		t.Fatalf("runtime session id changed between sequence items: %q vs %q", gotA, gotB)
	}
	if !strings.Contains(gotA, "iteration-0") || !strings.Contains(gotA, "acme-group") || !strings.Contains(gotA, "review-specialist") {
		t.Fatalf("runtime session id missing scope parts: %q", gotA)
	}
	if strings.Contains(gotA, "item") {
		t.Fatalf("runtime session id should not include sanitizer fallback for non-empty scope: %q", gotA)
	}
}

func TestAgentRuntimeSessionIDOmitsEmptyScope(t *testing.T) {
	hcpo := &StepBasedWorkflowOrchestrator{}

	got := hcpo.agentSequenceRuntimeSessionID(nil, "step-2", "writer")
	if !strings.HasPrefix(got, "msgseq-step-2-writer-") {
		t.Fatalf("runtime session id = %q, want msgseq-step-2-writer-<unique owner>", got)
	}
}

func TestAgentWriteAccess_RejectsPerFilePaths(t *testing.T) {
	var w AgentWriteAccess
	err := json.Unmarshal([]byte(`{"db": true, "paths": ["db/session_health.json"]}`), &w)
	if err == nil {
		t.Fatal("expected error for per-file paths in write_access, got nil")
	}
	if !strings.Contains(err.Error(), "per-file scoping") {
		t.Fatalf("error should explain per-file scoping is unsupported, got: %v", err)
	}
}

func TestAgentWriteAccess_FolderBooleansOK(t *testing.T) {
	var w AgentWriteAccess
	if err := json.Unmarshal([]byte(`{"db": true, "knowledgebase": true}`), &w); err != nil {
		t.Fatalf("folder-level booleans should unmarshal cleanly, got: %v", err)
	}
	if !w.DB || !w.Knowledgebase || w.Learnings {
		t.Fatalf("unexpected decoded write_access: %+v", w)
	}
}

func TestAgentWriteAccess_EmptyOK(t *testing.T) {
	var w AgentWriteAccess
	if err := json.Unmarshal([]byte(`{}`), &w); err != nil {
		t.Fatalf("empty write_access should unmarshal cleanly, got: %v", err)
	}
	if w != (AgentWriteAccess{}) {
		t.Fatalf("empty write_access should be zero value, got: %+v", w)
	}
}

func TestAgentItemInheritsStepWriteAccess(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)
	hcpo.useKnowledgebase = true
	config := &AgentConfigs{
		KnowledgebaseAccess: KBAccessReadWrite,
		LearningsAccess:     LearningsAccessReadWrite,
	}

	got := hcpo.resolveAgentItemWriteAccess(config, AgentItem{
		ID:   "plain-turn",
		Type: "user_message",
	})
	if !got.DB || !got.Knowledgebase || !got.Learnings {
		t.Fatalf("plain sequence turn should inherit all step-level writes, got: %+v", got)
	}
}

func TestAgentItemOverrideNarrowsStepWriteAccess(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)
	hcpo.useKnowledgebase = true
	config := &AgentConfigs{
		KnowledgebaseAccess: KBAccessReadWrite,
		LearningsAccess:     LearningsAccessReadWrite,
	}

	got := hcpo.resolveAgentItemWriteAccess(config, AgentItem{
		ID:          "db-only-turn",
		Type:        "user_message",
		WriteAccess: AgentWriteAccess{DB: true},
	})
	if !got.DB || got.Knowledgebase || got.Learnings {
		t.Fatalf("non-empty item override should narrow inherited writes to db only, got: %+v", got)
	}
}

func TestAgentItemAlwaysKeepsUniformDBWriteAccess(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)
	hcpo.useKnowledgebase = true
	config := &AgentConfigs{
		KnowledgebaseAccess: KBAccessRead,
		LearningsAccess:     LearningsAccessRead,
	}

	got := hcpo.resolveAgentItemWriteAccess(config, AgentItem{
		ID:   "attempted-escalation",
		Type: "user_message",
		WriteAccess: AgentWriteAccess{
			DB: true, Knowledgebase: true, Learnings: true,
		},
	})
	if got != (AgentWriteAccess{DB: true}) {
		t.Fatalf("item override must keep uniform DB access without escalating KB/learnings, got: %+v", got)
	}
}

func TestAgentTemplateVarsReflectItemWriteAccess(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		[]string{"test-server"}, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/test-flow")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0"}
	step := msgSeqStep(AgentItem{ID: "capture", Type: "user_message"})
	// The step itself grants KB read-write: the prompt advertises only what the
	// step's folder guard allows (PLAT-438).
	step.AgentConfigs = &AgentConfigs{KnowledgebaseAccess: KBAccessReadWrite}
	item := AgentItem{
		ID:          "capture",
		Type:        "user_message",
		WriteAccess: AgentWriteAccess{DB: true, Knowledgebase: true, Learnings: true},
	}
	readPaths, writePaths := hcpo.setupAgentFolderGuard("step-1", step.GetID(), getAgentConfigs(step), item.WriteAccess)
	vars := hcpo.buildAgentTemplateVars(step, item, 0, "step-1", "write the durable notes", readPaths, writePaths, item.WriteAccess)
	if !strings.Contains(vars["FolderGuardReadPaths"], filepath.Join("Workflow", "test-flow", "learnings", step.GetID())) {
		t.Fatalf("agent cannot read its step-specific learnings: %q", vars["FolderGuardReadPaths"])
	}
	if strings.Contains(vars["FolderGuardWritePaths"], filepath.Join("Workflow", "test-flow", "learnings", step.GetID())) {
		t.Fatalf("agent unexpectedly received step-learning write access: %q", vars["FolderGuardWritePaths"])
	}

	if got := vars["KbAccess"]; got != KBAccessReadWrite {
		t.Fatalf("KbAccess = %q, want %q", got, KBAccessReadWrite)
	}
	if note := vars["AgentAccessNote"]; !strings.Contains(note, "db/") || !strings.Contains(note, "knowledgebase/notes/") || !strings.Contains(note, "learnings/_global/") {
		t.Fatalf("access note does not list item write grants: %q", note)
	}
	if got := vars["KBGuidanceBlock"]; !strings.Contains(got, "Knowledgebase contribution") {
		t.Fatalf("KBGuidanceBlock missing direct-write guidance: %q", got)
	}
	wantNotesPath := filepath.ToSlash(filepath.Join(docsRoot, "Workflow/test-flow/knowledgebase/notes")) + "/"
	if got := vars["KBGuidanceBlock"]; !strings.Contains(got, wantNotesPath) ||
		!strings.Contains(got, "Do not use shell redirection, heredocs, tee, Python") {
		t.Fatalf("KBGuidanceBlock should use absolute notes path and patch-only writes: %q", got)
	}
}

func TestAgentTemplateVarsUseEffectiveWriteAccess(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		[]string{"test-server"}, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/test-flow")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0"}
	step := msgSeqStep(AgentItem{ID: "capture", Type: "user_message"})
	item := AgentItem{
		ID:          "capture",
		Type:        "user_message",
		WriteAccess: AgentWriteAccess{Learnings: true},
	}
	effectiveAccess := AgentWriteAccess{}
	readPaths, writePaths := hcpo.setupAgentFolderGuard("step-1", step.GetID(), getAgentConfigs(step), effectiveAccess)
	vars := hcpo.buildAgentTemplateVars(step, item, 0, "step-1", "write the durable notes", readPaths, writePaths, effectiveAccess)

	if note := vars["AgentAccessNote"]; strings.Contains(strings.TrimPrefix(note, "Reads are available for execution outputs, soul, builder logs, db/, knowledgebase/, learnings/_global/, and this step's learnings folder. "), "learnings/_global/") {
		t.Fatalf("write access note should reflect effective grants, not raw item grants: %q", note)
	}
	if writes := vars["FolderGuardWritePaths"]; strings.Contains(writes, "learnings/_global") {
		t.Fatalf("folder guard write paths should not include stripped learnings grant: %q", writes)
	}
}

func TestAgentFolderGuardIncludesAdditionalReadPathsWithoutWrites(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/test-flow")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0/dev"}
	config := &AgentConfigs{AdditionalReadPaths: []string{"variables", "reports/reference.json"}}

	readPaths, writePaths := hcpo.setupAgentFolderGuard(
		"step-1", "step-seq", config, AgentWriteAccess{},
	)
	for _, expected := range []string{"Workflow/test-flow/variables", "Workflow/test-flow/reports/reference.json"} {
		if !slices.Contains(readPaths, expected) {
			t.Fatalf("agent read paths missing %q: %v", expected, readPaths)
		}
		if slices.Contains(writePaths, expected) {
			t.Fatalf("agent additional read path widened writes to %q: %v", expected, writePaths)
		}
	}
}

func msgSeqStep(items ...AgentItem) *AgentPlanStep {
	return &AgentPlanStep{
		CommonStepFields: CommonStepFields{
			ID:          "step-seq",
			Title:       "Sequence",
			Description: "do work",
		},
		Items: items,
	}
}

func TestAgentItemReportedFailure(t *testing.T) {
	tests := []struct {
		name       string
		summary    string
		wantFailed bool
		wantReason string
	}{
		{name: "failed with reason", summary: "did the work\nSTATUS: FAILED — cannot write db/x.json: no db write access", wantFailed: true, wantReason: "cannot write db/x.json: no db write access"},
		{name: "failed no space", summary: "STATUS:FAILED - blocked by folder guard", wantFailed: true, wantReason: "blocked by folder guard"},
		{name: "completed is not failed", summary: "all done\nSTATUS: COMPLETED", wantFailed: false},
		{name: "prose mention of failed is not the marker", summary: "the previous attempt failed but I recovered", wantFailed: false},
		{name: "no status marker", summary: "wrote the queue and validated it", wantFailed: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, failed := agentSequenceItemReportedFailure(tc.summary)
			if failed != tc.wantFailed {
				t.Fatalf("failed=%v, want %v (summary=%q)", failed, tc.wantFailed, tc.summary)
			}
			if tc.wantFailed && reason != tc.wantReason {
				t.Fatalf("reason=%q, want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestAgentItemUsesManagedDBToolsWithoutRawDBFilesystemAccess(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)
	config := &AgentConfigs{
		KnowledgebaseAccess: KBAccessRead,
		LearningsAccess:     LearningsAccessRead,
	}
	readPaths, writePaths := hcpo.setupAgentFolderGuard("step-1", "readonly", config, AgentWriteAccess{
		DB: true, Knowledgebase: true, Learnings: true,
	})
	allPaths := strings.Join(append(append([]string{}, readPaths...), writePaths...), "\n")
	if strings.Contains(allPaths, "db.sqlite") {
		t.Fatalf("agent item unexpectedly received raw db.sqlite filesystem access: %v", writePaths)
	}
	for _, p := range readPaths {
		if strings.Contains(p, "/db/") && !strings.Contains(p, "/db/assets") && !strings.HasSuffix(p, "/db/README.md") {
			t.Fatalf("unexpected DB read grant: %q", p)
		}
	}
	for _, p := range writePaths {
		if strings.Contains(p, "/db/") && !strings.Contains(p, "/db/assets") {
			t.Fatalf("unexpected DB write grant: %q", p)
		}
	}
	joined := strings.Join(writePaths, "\n")
	for _, forbidden := range []string{"/knowledgebase/notes", "/learnings/_global"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("read-only non-DB store unexpectedly received write access to %s: %v", forbidden, writePaths)
		}
	}
}

// PLAT-175. customer-login's survey-app-and-refresh-knowledge step is a
// agent step instructed, in its own plan description, to sync
// db/assets/business-context/ via shell every cycle -- read the existing
// .source_sha to compare, then add/overwrite/remove files and rewrite
// .source_sha and _manifest.json. mutate_workflow_db is SQL-only and cannot
// do this. Before this fix, setupAgentFolderGuard granted nothing
// under db/ at all (commit a960df20 dropped the whole folder instead of
// narrowing to just db.sqlite), so this step had no legal path to do the one
// thing its own instructions require every run -- silently, since nothing
// upstream had changed on the runs where it was checked.
func TestAgentFolderGuardGrantsDBAssetsReadWrite(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)
	config := &AgentConfigs{
		KnowledgebaseAccess: KBAccessRead,
		LearningsAccess:     LearningsAccessRead,
	}
	readPaths, writePaths := hcpo.setupAgentFolderGuard("step-1", "survey-app-and-refresh-knowledge", config, AgentWriteAccess{
		DB: true,
	})
	wantAssetsPath := filepath.Join("Workflow", "test-flow", "db", "assets")
	if !slices.Contains(readPaths, wantAssetsPath) {
		t.Fatalf("db/assets/ missing from read paths: %v", readPaths)
	}
	if !slices.Contains(writePaths, wantAssetsPath) {
		t.Fatalf("db/assets/ missing from write paths: %v", writePaths)
	}
	for _, p := range append(append([]string{}, readPaths...), writePaths...) {
		if strings.Contains(p, "db.sqlite") {
			t.Fatalf("db/assets/ grant must not also expose db.sqlite: %q", p)
		}
	}
}
