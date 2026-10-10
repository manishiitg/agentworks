package step_based_workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

func kbSessionFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/consumer"
	for _, name := range []string{"consumer", "source"} {
		if err := os.MkdirAll(filepath.Join(root, "Workflow", name, "knowledgebase", "notes"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "Workflow", name, "workflow.json"), []byte(`{"id":"`+name+`","created_by":"owner"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	kb := filepath.Join(root, "Workflow/source/knowledgebase")
	os.WriteFile(filepath.Join(kb, "notes/fact.md"), []byte("verified architecture"), 0644)
	return root, ws, kb
}
func saveKBSources(t *testing.T, root, ws string, attach bool, folder bool) {
	t.Helper()
	m := map[string]interface{}{"id": "consumer", "created_by": "owner"}
	if attach {
		m["knowledgebase_sources"] = []workflowtypes.KnowledgebaseSource{{WorkflowID: "source", Alias: "project-a", Access: "read"}}
	}
	if folder {
		m["folder_access"] = []workflowtypes.WorkflowFolderGrant{{ID: "independent", Alias: "independent", Path: filepath.Join(root, "Workflow/source/knowledgebase"), Access: workflowtypes.FolderAccessReadWrite}}
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(root, ws, "workflow.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestKBSourcesSessionRefreshDetachAndIndependentGrants(t *testing.T) {
	root, ws, kb := kbSessionFixture(t)
	saveKBSources(t, root, ws, true, false)
	session := "shared-kb-builder"
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	common.SetSessionFolderGuard(session, []string{ws}, []string{ws})
	RefreshWorkflowFolderAccessSession(session, ws)
	cfg := common.GetSessionShellConfig(session)
	if cfg.Env["WORKFLOW_KB_project-a"] != kb || !containsString(cfg.ReadPaths, kb) || !containsString(cfg.BlockedWritePaths, kb) {
		t.Fatal("builder did not receive read-only source", cfg)
	}
	child := "kb-copy"
	defer common.ClearSessionShellConfig(child)
	if !common.CopySessionFolderGuard(session, child) {
		t.Fatal("could not copy guard")
	}
	saveKBSources(t, root, ws, false, false)
	RefreshWorkflowFolderAccessSession(session, ws)
	if copied := common.GetSessionShellConfig(child); containsString(copied.ReadPaths, kb) || copied.Env["WORKFLOW_KB_project-a"] != "" {
		t.Fatal("copied session retained source after detach")
	}
	cfg = common.GetSessionShellConfig(session)
	if cfg.Env["WORKFLOW_KB_project-a"] != "" || containsString(cfg.ReadPaths, kb) || containsString(cfg.BlockedWritePaths, kb) {
		t.Fatal("detach retained grants", cfg)
	}
	saveKBSources(t, root, ws, true, true)
	cfg = common.GetSessionShellConfig(session)
	if !containsString(cfg.BlockedWritePaths, kb) {
		t.Fatal("read-only KB overlay missing")
	}
	saveKBSources(t, root, ws, false, true)
	cfg = common.GetSessionShellConfig(session)
	if !containsString(cfg.WritePaths, kb) || containsString(cfg.BlockedWritePaths, kb) {
		t.Fatal("detach damaged independent folder grant", cfg)
	}
}

func TestKBSourcesStepOptOutAndOwnershipRevocation(t *testing.T) {
	root, ws, kb := kbSessionFixture(t)
	saveKBSources(t, root, ws, true, false)
	for _, enabled := range []bool{false, true} {
		session := "kb-read-mode"
		common.SetSessionFolderGuard(session, []string{ws}, []string{ws})
		reads, writes, blocked, env := appendWorkflowFolderAccess(ws, nil, nil, enabled)
		common.SetSessionFolderGuard(session, reads, writes)
		configureWorkflowFolderAccessSession(session, ws, blocked, env)
		cfg := common.GetSessionShellConfig(session)
		if containsString(cfg.ReadPaths, kb) != enabled {
			t.Fatal("step opt-out ignored", cfg)
		}
		common.ClearSessionShellConfig(session)
	}
	session := "kb-revoked"
	defer common.ClearSessionShellConfig(session)
	common.SetSessionFolderGuard(session, []string{ws}, nil)
	RefreshWorkflowFolderAccessSession(session, ws)
	os.WriteFile(filepath.Join(root, "Workflow/source/workflow.json"), []byte(`{"id":"source","created_by":"someone-else"}`), 0644)
	cfg := common.GetSessionShellConfig(session)
	if containsString(cfg.ReadPaths, kb) || common.GetSessionShellEnv(session)["WORKFLOW_KB_project-a"] != "" {
		t.Fatal("ownership revocation retained access", cfg)
	}
}

func TestKBSourcesActualShellReadOnlyAndDetach(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native macOS sandbox integration")
	}
	root, ws, kb := kbSessionFixture(t)
	saveKBSources(t, root, ws, true, false)
	t.Setenv("NATIVE_WORKSPACE", "true")
	session := "kb-shell"
	defer common.ClearSessionShellConfig(session)
	common.SetSessionFolderGuard(session, []string{filepath.Join(root, ws)}, []string{filepath.Join(root, ws)})
	RefreshWorkflowFolderAccessSession(session, ws)
	run := func(command string) (string, error) {
		cfg := common.GetSessionShellConfig(session)
		iso := security.Isolator{BaseDir: root, WorkDir: filepath.Join(root, ws), ReadPaths: cfg.ReadPaths, WritePaths: cfg.WritePaths, BlockedWritePaths: cfg.BlockedWritePaths}
		cmd, cleanup, err := iso.ExecuteIsolated(context.Background(), command, nil)
		if err != nil {
			return "", err
		}
		defer cleanup()
		for key, value := range cfg.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run(`cat "$WORKFLOW_KB_project-a/notes/fact.md" | tr a-z A-Z`)
	if err != nil || !strings.Contains(out, "VERIFIED ARCHITECTURE") {
		t.Fatal(out, err)
	}
	if out, err := run(`printf changed > "$WORKFLOW_KB_project-a/notes/fact.md"`); err == nil {
		t.Fatal("source write succeeded", out)
	}
	if out, err := run(`cat ../source/workflow.json`); err == nil {
		t.Fatal("source manifest leaked", out)
	}
	saveKBSources(t, root, ws, false, false)
	if out, err := run("cat " + shellQuotePath(filepath.Join(kb, "notes/fact.md"))); err == nil {
		t.Fatal("cached absolute path survived detach", out)
	}
}

func TestKBSourcesWriteGrantAppliesToAllThreeFolderGuards(t *testing.T) {
	root, ws, kb := kbSessionFixture(t)
	notesPath := filepath.Join(kb, "notes")
	m := map[string]interface{}{"id": "consumer", "created_by": "owner", "knowledgebase_sources": []workflowtypes.KnowledgebaseSource{{WorkflowID: "source", Alias: "project-a", Access: "write"}}}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(root, ws, "workflow.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}

	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath(ws)
	hcpo.selectedRunFolder = "iteration-0/default"

	// Source has not granted "consumer" write access yet: no write path.
	_, writes := hcpo.setupExecutionFolderGuard("step-1", "collect", KBAccessReadWrite, LearningsAccessNone, DBAccessRead, nil)
	if containsString(writes, notesPath) {
		t.Fatal("external notes/ writable before grant", writes)
	}

	// Grant it.
	if err := os.WriteFile(filepath.Join(root, "Workflow/source/workflow.json"), []byte(`{"id":"source","created_by":"owner","access":{"allowed_kb_writers":["consumer"]}}`), 0644); err != nil {
		t.Fatal(err)
	}

	_, writes = hcpo.setupExecutionFolderGuard("step-1", "collect", KBAccessReadWrite, LearningsAccessNone, DBAccessRead, nil)
	if !containsString(writes, notesPath) {
		t.Fatal("execution folder guard did not grant external write source", writes)
	}
	// The step's own kbAccess still gates it -- read-only step access must not
	// pick up the external write source either.
	_, writes = hcpo.setupExecutionFolderGuard("step-1", "collect", KBAccessRead, LearningsAccessNone, DBAccessRead, nil)
	if containsString(writes, notesPath) {
		t.Fatal("read-only step kbAccess still received external write source", writes)
	}

	writeStepConfig := &AgentConfigs{KnowledgebaseAccess: KBAccessReadWrite}
	_, writes = hcpo.setupMessageSequenceFolderGuard("step-2", "collect2", writeStepConfig, MessageSequenceWriteAccess{DB: true, Knowledgebase: true})
	if !containsString(writes, notesPath) {
		t.Fatal("message sequence folder guard did not grant external write source", writes)
	}

	step := &OrchestratorPlanStep{CommonStepFields: CommonStepFields{ID: "step-3"}, AgentConfigs: writeStepConfig}
	_, writes = hcpo.setupOrchestratorFolderGuard(step, "step-1")
	if !containsString(writes, notesPath) {
		t.Fatal("orchestrator folder guard did not grant external write source", writes)
	}
}

func TestKBSourcesBuilderPromptAndExecutionScope(t *testing.T) {
	root, ws, kb := kbSessionFixture(t)
	saveKBSources(t, root, ws, true, false)
	prompt := PhaseChatSystemPrompt("workflow-builder", map[string]string{"WorkspacePath": ws})
	if !strings.Contains(prompt, "WORKFLOW_KB_project-a") || !strings.Contains(prompt, "Attached knowledge bases") {
		t.Fatal("builder source discovery not injected")
	}
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath(ws)
	hcpo.selectedRunFolder = "iteration-0/default"
	for _, mode := range []string{KBAccessNone, KBAccessRead} {
		reads, _ := hcpo.setupExecutionFolderGuard("step-1", "collect", mode, LearningsAccessNone, DBAccessRead, nil)
		if containsString(reads, kb) != (mode == KBAccessRead) {
			t.Fatal("execution KB mode not applied", mode, reads)
		}
	}
}
