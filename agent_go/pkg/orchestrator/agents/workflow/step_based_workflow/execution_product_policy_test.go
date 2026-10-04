package step_based_workflow

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestRelayExecutionPolicyLoadedWithPlanForAllEntrypoints(t *testing.T) {
	for _, root := range []string{"Workflow/draft", "Workflow/draft/releases/v1/workspace"} {
		t.Run(root, func(t *testing.T) {
			base := newFakeWorkspaceAPIWithContent(t, map[string]string{root + "/workflow.json": `{"kind":"relay","code_layout_version":1}`})
			base.SetWorkspacePath(root)
			c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
			if err := c.loadCodeLayout(context.Background()); err != nil {
				t.Fatal(err)
			}
			cfg := &AgentConfigs{KnowledgebaseAccess: KBAccessReadWrite, KnowledgebaseContribution: "remember", LearningsAccess: LearningsAccessReadWrite, LearningObjective: "remember"}
			if c.resolveDBAccess(cfg) != DBAccessNone || c.UseKnowledgebase() || c.resolveExecutionLearningsAccess(cfg, nil) != LearningsAccessNone || c.canReadLearnings(cfg, nil) || c.canWriteLearnings(cfg, nil) {
				t.Fatal("legacy step config re-enabled platform stores")
			}
			access := c.constrainMessageSequenceWriteAccess(cfg, MessageSequenceWriteAccess{DB: true, Knowledgebase: true, Learnings: true})
			if access != (MessageSequenceWriteAccess{}) {
				t.Fatalf("sequence grants stores: %+v", access)
			}
			if items := c.messageSequenceClosingItems(context.Background(), &MessageSequencePlanStep{AgentConfigs: cfg}, 0); len(items) > 0 {
				t.Fatalf("Relay has reflection turns: %+v", items)
			}
			env := c.codeRuntimeEnv(map[string]string{"DB_PATH": "stale", workflowDBAccessEnv: DBAccessReadWrite, "VAR_INPUT": "hello", "SECRET_CUSTOM_DB": "user db", "WORKFLOW_KB_FACTS": "stale"})
			if env["DB_PATH"] != "" || env[workflowDBAccessEnv] != DBAccessNone || env["VAR_INPUT"] != "hello" || env["SECRET_CUSTOM_DB"] != "user db" || env["WORKFLOW_KB_FACTS"] != "" || env["WORKFLOW_KB_ACCESS"] != KBAccessNone {
				t.Fatalf("wrong runtime env: %+v", env)
			}
			// The same controller can reload an ordinary workflow; defaults still apply.
			if err := c.configureExecutionProduct(""); err != nil {
				t.Fatal(err)
			}
			if c.resolveDBAccess(cfg) != DBAccessReadWrite || !c.UseKnowledgebase() || !c.canWriteLearnings(cfg, nil) {
				t.Fatal("ordinary workflow lost stores")
			}
		})
	}
}

func TestRelayToolsAndGuardsIgnoreExplicitStoreGrants(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	c := codeLayoutController(t, 1)
	if err := c.configureExecutionProduct("relay"); err != nil {
		t.Fatal(err)
	}
	names := []string{"execute_shell_command", "query_workflow_db", "mutate_workflow_db", "apply_workflow_db_migration", "create_workflow_database_snapshot"}
	c.WorkspaceToolExecutors = map[string]interface{}{}
	c.ToolCategories = map[string]string{}
	for _, name := range names {
		c.WorkspaceTools = append(c.WorkspaceTools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}})
		c.WorkspaceToolExecutors[name] = func(context.Context, map[string]interface{}) (string, error) { return "", nil }
		c.ToolCategories[name] = "workflow_db"
	}
	c.ToolCategories["execute_shell_command"] = "workspace_advanced"
	cfg := &AgentConfigs{EnabledCustomTools: []string{"workflow_db:*"}, KnowledgebaseAccess: KBAccessReadWrite, LearningsAccess: LearningsAccessReadWrite}
	tools, execs := c.prepareCustomTools(cfg)
	if len(tools) != 1 || tools[0].Function.Name != "execute_shell_command" || len(execs) != 1 {
		t.Fatalf("Relay got DB tools: %+v %v", tools, execs)
	}
	read, write := c.setupMessageSequenceFolderGuard("step-1", "answer", cfg, MessageSequenceWriteAccess{DB: true, Knowledgebase: true, Learnings: true})
	for _, store := range platformStoreBlockedPaths(c.GetWorkspacePath()) {
		if slices.Contains(read, store) || slices.Contains(write, store) || slices.Contains(write, store+"/assets") {
			t.Fatalf("store grant %s read=%v write=%v", store, read, write)
		}
	}
	step := &RegularPlanStep{CommonStepFields: CommonStepFields{ID: "script"}, AgentConfigs: cfg}
	guard, err := c.resolveScriptedShellGuard(context.Background(), step, 0, "step-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range platformStoreBlockedPaths(c.GetWorkspacePath()) {
		if !slices.Contains(guard.BlockedPaths, store) {
			t.Fatalf("Python guard does not block %s: %+v", store, guard)
		}
	}
}

func TestRelayBuilderRestoreAndScriptBridgeDenyPlatformStores(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspace := "Workflow/relay"
	if err := os.MkdirAll(filepath.Join(root, workspace), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workspace, "workflow.json"), []byte(`{"kind":"relay"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"relay-builder-restore", "relay-script-tool"} {
		t.Cleanup(func() { common.ClearSessionShellConfig(session) })
		common.SetSessionFolderGuard(session, []string{workspace}, []string{workspace})
		common.SetSessionShellEnv(session, map[string]string{"DB_PATH": "stale", workflowDBAccessEnv: DBAccessReadWrite})
		if session == "relay-builder-restore" {
			ConfigureManagedWorkflowDBSession(session, workspace, true)
		} else {
			grantScriptBridgeSessionDB(session, workspace, DBAccessNone)
		}
		cfg := common.GetSessionShellConfig(session)
		if cfg.Env[workflowDBAccessEnv] != DBAccessNone || cfg.Env["DB_PATH"] != "" {
			t.Fatalf("inherited DB grant retained: %+v", cfg)
		}
		for _, store := range platformStoreBlockedPaths(workspace) {
			if !slices.Contains(cfg.BlockedPaths, store) {
				t.Fatalf("broad parent bypasses %s: %+v", store, cfg)
			}
		}
	}
}

func TestRelayDoesNotRestoreAttachedPlatformKnowledgebase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	for name, manifest := range map[string]string{
		"relay":  `{"id":"consumer","kind":"relay","created_by":"owner","knowledgebase_sources":[{"workflow_id":"source","alias":"facts","access":"read"}]}`,
		"source": `{"id":"source","created_by":"owner"}`,
	} {
		dir := filepath.Join(root, "Workflow", name, "knowledgebase", "notes")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "Workflow", name, "workflow.json"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read, _, _, env := appendWorkflowFolderAccess("Workflow/relay", nil, nil, true)
	if len(read) > 0 || env["WORKFLOW_KB_ACCESS"] != KBAccessNone || env["WORKFLOW_KB_FACTS"] != "" {
		t.Fatalf("Relay attached KB was re-enabled: read=%v env=%v", read, env)
	}
	const session = "relay-stale-kb-source"
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	common.SetSessionFolderGuard(session, []string{filepath.Join(root, "Workflow/source/knowledgebase")}, nil)
	common.SetSessionShellEnv(session, map[string]string{"WORKFLOW_KB_FACTS": "stale"})
	ConfigureManagedWorkflowDBSession(session, "Workflow/relay", true)
	cfg := common.GetSessionShellConfig(session)
	canonicalKB, err := filepath.EvalSymlinks(filepath.Join(root, "Workflow/source/knowledgebase"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env["WORKFLOW_KB_FACTS"] != "" || !slices.Contains(cfg.BlockedPaths, canonicalKB) {
		t.Fatalf("stale KB grant survived restore: %+v", cfg)
	}
}

func TestRelayPythonGuardBlocksAttachedKnowledgebase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	for name, manifest := range map[string]string{
		"testing": `{"id":"consumer","kind":"relay","created_by":"owner","knowledgebase_sources":[{"workflow_id":"source","alias":"facts","access":"read"}]}`,
		"source":  `{"id":"source","created_by":"owner"}`,
	} {
		dir := filepath.Join(root, "Workflow", name, "knowledgebase")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "Workflow", name, "workflow.json"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := codeLayoutController(t, 1)
	if err := c.configureExecutionProduct("relay"); err != nil {
		t.Fatal(err)
	}
	step := &RegularPlanStep{CommonStepFields: CommonStepFields{ID: "script"}}
	guard, err := c.resolveScriptedShellGuard(context.Background(), step, 0, "step-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Join(root, "Workflow/source/knowledgebase"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(guard.BlockedPaths, canonical) {
		t.Fatalf("Python did not block attached KB: %+v", guard)
	}
}
