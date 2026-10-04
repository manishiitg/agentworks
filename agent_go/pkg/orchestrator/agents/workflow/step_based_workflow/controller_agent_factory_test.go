package step_based_workflow

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	mcpllm "github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestRegisterStepSessionShellEnvProvidesBridgeParity(t *testing.T) {
	sessionID := "eval-shell-env-test"
	defer common.ClearSessionShellConfig(sessionID)

	registerStepSessionShellEnv(sessionID, "/workspace/eval/step", "/workspace/eval", "/workspace/db/db.sqlite", "iteration-0/eval", map[string]string{
		"VAR_LOGIN_EMAIL":       "configured@example.com",
		"SECRET_LOGIN_PASSWORD": "password",
		"MCP_SESSION_ID":        "stale-parent-session",
	})
	env := common.GetSessionShellEnv(sessionID)
	for key, want := range map[string]string{
		"STEP_OUTPUT_DIR":    "/workspace/eval/step",
		"STEP_EXECUTION_DIR": "/workspace/eval",
		"DB_PATH":            "/workspace/db/db.sqlite",
		// PLAT-185: a scripted step reading a sibling step's runs/<iteration>/
		// logs/... folder had no reliable way to know the current run's own
		// folder name short of hardcoding a guess. RUN_FOLDER removes the guess.
		"RUN_FOLDER":            "iteration-0/eval",
		"VAR_LOGIN_EMAIL":       "configured@example.com",
		"SECRET_LOGIN_PASSWORD": "password",
	} {
		if got := env[key]; got != want {
			t.Fatalf("%s: expected %q, got %q", key, want, got)
		}
	}
	if _, exists := env["MCP_SESSION_ID"]; exists {
		t.Fatal("step session env must not inherit stale MCP_SESSION_ID")
	}
}

func TestResolveDBAccessIsReadWriteForEveryWorkflowStep(t *testing.T) {
	// PLAT-061 removed the db_access field; the invariant it never actually
	// enforced is what matters and must hold for every shape of config.
	for name, cfg := range map[string]*AgentConfigs{"configured": {}, "nil": nil} {
		if got := resolveDBAccess(cfg); got != DBAccessReadWrite {
			t.Fatalf("%s: execution must be read-write, got %q", name, got)
		}
	}
}

func TestConfigureWorkflowDBSessionBlocksRawSQLiteForManagedAgents(t *testing.T) {
	sessionID := "managed-workflow-db"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo/db"}, []string{"Workflow/demo/db"})
	configureWorkflowDBSession(sessionID, "Workflow/demo", DBAccessReadWrite, false)

	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil || cfg.Env[workflowDBAccessEnv] != DBAccessReadWrite {
		t.Fatalf("trusted DB access not recorded: %+v", cfg)
	}
	for _, want := range []string{
		"Workflow/demo/db/db.sqlite",
		"Workflow/demo/db/db.sqlite-wal",
		"Workflow/demo/db/db.sqlite-shm",
	} {
		if !slices.Contains(cfg.BlockedPaths, want) {
			t.Fatalf("raw SQLite path %q not blocked: %+v", want, cfg.BlockedPaths)
		}
	}
}

func TestConfigureWorkflowDBSessionRetainsScriptedCompatibility(t *testing.T) {
	sessionID := "scripted-workflow-db"
	defer common.ClearSessionShellConfig(sessionID)
	configureWorkflowDBSession(sessionID, "Workflow/demo", DBAccessReadWrite, true)
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil || len(cfg.BlockedPaths) != 0 {
		t.Fatalf("scripted compatibility unexpectedly blocked: %+v", cfg)
	}
}

func TestConfigureManagedWorkflowDBSessionProtectsBuilderSQLiteAndSidecars(t *testing.T) {
	const (
		sessionID     = "managed-workflow-builder-db"
		workspacePath = "Workflow/demo"
	)
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })

	// Match the main Workflow Builder's broad workflow-folder grant. The
	// managed DB boundary must still win for the database and both WAL files.
	common.SetSessionFolderGuard(sessionID, []string{workspacePath}, []string{workspacePath})
	common.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{workspacePath + "/planning"})
	ConfigureManagedWorkflowDBSession(sessionID, workspacePath, true)

	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil || cfg.Env[workflowDBAccessEnv] != DBAccessReadWrite {
		t.Fatalf("Builder managed DB access not recorded: %+v", cfg)
	}
	if !slices.Contains(cfg.BlockedWritePaths, workspacePath+"/planning") {
		t.Fatalf("managed DB setup discarded the planning write deny: %+v", cfg.BlockedWritePaths)
	}

	client := workspacepkg.NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	for _, path := range []string{
		workspacePath + "/db/db.sqlite",
		workspacePath + "/db/db.sqlite-wal",
		workspacePath + "/db/db.sqlite-shm",
	} {
		if err := client.ValidatePathWithContext(ctx, path, false); err == nil {
			t.Fatalf("Builder retained raw read access to %q", path)
		}
		if err := client.ValidatePathWithContext(ctx, path, true); err == nil {
			t.Fatalf("Builder retained raw write access to %q", path)
		}
	}

	// Adjacent managed artifacts remain available: agents still author the
	// migration file and use the dedicated tool to apply it.
	for _, path := range []string{
		workspacePath + "/db/migrations/add-table.sql",
		workspacePath + "/db/README.md",
		workspacePath + "/db/assets/export.json",
	} {
		if err := client.ValidatePathWithContext(ctx, path, true); err != nil {
			t.Fatalf("managed DB boundary over-blocked %q: %v", path, err)
		}
	}
}

func TestConfigureManagedWorkflowDBSessionKeepsReadOnlyBuilderFailClosed(t *testing.T) {
	const sessionID = "read-only-workflow-builder-db"
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo"}, nil)

	ConfigureManagedWorkflowDBSession(sessionID, "Workflow/demo", false)

	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil || cfg.Env[workflowDBAccessEnv] != DBAccessRead {
		t.Fatalf("read-only Builder DB access = %+v, want %q", cfg, DBAccessRead)
	}
}

func TestWorkshopToolAgentBridgeSessionOverridesParentRouting(t *testing.T) {
	t.Setenv("MCP_API_URL", "http://127.0.0.1:7777/s/parent-workshop")
	sessionID := "workshop-pulse-fixer-child"
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })

	configureWorkshopToolAgentBridgeSession(sessionID)
	env := common.GetSessionShellEnv(sessionID)
	for key, want := range map[string]string{
		"MCP_SESSION_ID": sessionID,
		"MCP_API_URL":    "http://127.0.0.1:7777/s/" + sessionID,
		"MCP_CUSTOM":     "http://127.0.0.1:7777/s/" + sessionID + "/tools/custom",
	} {
		if got := env[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestSelectPulseLLMUsesPulseConfigInsteadOfPhaseConfig(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator: base,
		presetPhaseLLM: &AgentLLMConfig{
			Provider: "phase-provider",
			ModelID:  "phase-model",
		},
		presetPulseLLM: &AgentLLMConfig{
			Provider: "pulse-provider",
			ModelID:  "pulse-model",
			Options:  map[string]interface{}{"reasoning_effort": "high"},
		},
	}

	got := hcpo.selectPulseLLM("review agent")
	if got == nil {
		t.Fatal("selectPulseLLM returned nil")
	}
	if got.Primary.Provider != "pulse-provider" || got.Primary.ModelID != "pulse-model" {
		t.Fatalf("review selected %s/%s, want pulse-provider/pulse-model", got.Primary.Provider, got.Primary.ModelID)
	}
	if got.Primary.Options["reasoning_effort"] != "high" {
		t.Fatalf("review lost Pulse options: %+v", got.Primary.Options)
	}
}

func TestKBMaintenanceAgentsGetQueryButNotMutation(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	tool := func(name string) llmtypes.Tool {
		return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}}
	}
	noop := func(context.Context, map[string]interface{}) (string, error) { return "", nil }
	base.WorkspaceTools = []llmtypes.Tool{
		tool("execute_shell_command"), tool("diff_patch_workspace_file"),
		tool("query_workflow_db"), tool("mutate_workflow_db"),
	}
	base.WorkspaceToolExecutors = map[string]interface{}{
		"execute_shell_command":     noop,
		"diff_patch_workspace_file": noop,
		"query_workflow_db":         noop,
		"mutate_workflow_db":        noop,
	}
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	tools, executors := hcpo.prepareWorkspaceToolsOnly()
	names := make([]string, 0, len(tools))
	for _, definition := range tools {
		if definition.Function != nil {
			names = append(names, definition.Function.Name)
		}
	}
	if !slices.Contains(names, "query_workflow_db") || executors["query_workflow_db"] == nil {
		t.Fatalf("KB maintenance missing query_workflow_db: tools=%v executors=%v", names, executors)
	}
	if slices.Contains(names, "mutate_workflow_db") || executors["mutate_workflow_db"] != nil {
		t.Fatalf("KB maintenance received DB mutation authority: tools=%v executors=%v", names, executors)
	}
}

func TestPrepareCustomToolsMaterializesDBCapabilityFromDBAccess(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	tool := func(name string) llmtypes.Tool {
		return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}}
	}
	noop := func(context.Context, map[string]interface{}) (string, error) { return "", nil }
	base.WorkspaceTools = []llmtypes.Tool{
		tool("execute_shell_command"), tool("query_workflow_db"), tool("mutate_workflow_db"), tool("apply_workflow_db_migration"),
	}
	base.WorkspaceToolExecutors = map[string]interface{}{
		"execute_shell_command":       noop,
		"query_workflow_db":           noop,
		"mutate_workflow_db":          noop,
		"apply_workflow_db_migration": noop,
	}
	base.ToolCategories = map[string]string{
		"execute_shell_command":       "workspace_advanced",
		"query_workflow_db":           "workflow_db",
		"mutate_workflow_db":          "workflow_db",
		"apply_workflow_db_migration": "workflow_db",
	}
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}

	// PLAT-061 removed db_access; what this pins is that a deliberately narrow
	// explicit tool list still cannot strip the capability-derived DB tools.
	tests := []struct {
		name   string
		access string
		config *AgentConfigs
	}{
		{name: "default tool list", access: DBAccessReadWrite},
		{name: "empty explicit tool list", access: DBAccessReadWrite, config: &AgentConfigs{}},
		{name: "narrow explicit tool list", access: DBAccessReadWrite, config: &AgentConfigs{EnabledCustomTools: []string{"workspace_advanced:execute_shell_command"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, executors := hcpo.prepareCustomTools(tt.config)
			names := make([]string, 0, len(tools))
			for _, definition := range tools {
				if definition.Function != nil {
					names = append(names, definition.Function.Name)
				}
			}
			if !slices.Contains(names, "query_workflow_db") || executors["query_workflow_db"] == nil {
				t.Fatalf("db_access=%q missing query tool: tools=%v executors=%v", tt.access, names, executors)
			}
			gotMutation := slices.Contains(names, "mutate_workflow_db") && executors["mutate_workflow_db"] != nil
			if !gotMutation {
				t.Fatalf("db_access=%q missing uniform mutation capability (tools=%v)", tt.access, names)
			}
			// Migration follows the same DB capability in default and explicit
			// selections; changing the optional tool list must not change it.
			gotMigration := slices.Contains(names, "apply_workflow_db_migration") && executors["apply_workflow_db_migration"] != nil
			if !gotMigration {
				t.Fatalf("db_access=%q missing uniform migration capability (tools=%v)", tt.access, names)
			}
		})
	}
	// Capability additions must not fabricate tools absent from the parent's
	// authorized pool, even when an explicit selection asks for them.
	base.WorkspaceTools = []llmtypes.Tool{tool("execute_shell_command"), tool("query_workflow_db")}
	for _, config := range []*AgentConfigs{nil, {EnabledCustomTools: []string{"workflow_db:*"}}} {
		tools, executors := hcpo.prepareCustomTools(config)
		if len(tools) != 2 || executors["mutate_workflow_db"] != nil || executors["apply_workflow_db_migration"] != nil {
			t.Fatal("tool selection exposed DB mutation outside the authorized pool")
		}
	}
}

func TestPrepareCustomToolsUsesNarrowRequiredHumanToolBaseline(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	tool := func(name string) llmtypes.Tool {
		return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}}
	}
	noop := func(context.Context, map[string]interface{}) (string, error) { return "", nil }
	allHumanTools := []string{
		"human_feedback",
		"notify_user",
		"create_human_input_request",
		"get_notification_history",
		"send_slack_message",
		"slack",
		"google_workspace_cli",
		"get_human_input_request",
		"answer_human_input_request",
		"mark_human_input_consumed",
		"dismiss_duplicate_human_input_request",
	}
	base.WorkspaceTools = []llmtypes.Tool{tool("execute_shell_command")}
	base.WorkspaceToolExecutors = map[string]interface{}{"execute_shell_command": noop}
	base.ToolCategories = map[string]string{"execute_shell_command": "workspace_advanced"}
	for _, name := range allHumanTools {
		base.WorkspaceTools = append(base.WorkspaceTools, tool(name))
		base.WorkspaceToolExecutors[name] = noop
		base.ToolCategories[name] = "human_tools"
	}
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}

	for _, config := range []*AgentConfigs{
		nil,
		{EnabledCustomTools: []string{"workspace_advanced:execute_shell_command"}},
		{EnabledCustomTools: []string{"human_tools:*"}},
	} {
		tools, executors := hcpo.prepareCustomTools(config)
		names := make([]string, 0, len(tools))
		for _, definition := range tools {
			if definition.Function != nil {
				names = append(names, definition.Function.Name)
			}
		}
		for _, required := range []string{"human_feedback", "notify_user", "create_human_input_request"} {
			if !slices.Contains(names, required) || executors[required] == nil {
				t.Fatalf("required human tool %q missing: tools=%v", required, names)
			}
		}
		for _, excluded := range allHumanTools[3:] {
			if slices.Contains(names, excluded) || executors[excluded] != nil {
				t.Fatalf("default step tools unexpectedly include human tool %q: tools=%v", excluded, names)
			}
		}
	}

	tools, _ := hcpo.prepareCustomTools(&AgentConfigs{EnabledCustomTools: []string{"human_tools:get_notification_history"}})
	names := make([]string, 0, len(tools))
	for _, definition := range tools {
		if definition.Function != nil {
			names = append(names, definition.Function.Name)
		}
	}
	if !slices.Contains(names, "get_notification_history") {
		t.Fatalf("explicitly named extra human tool was not enabled: tools=%v", names)
	}
}

func TestExecutionFolderGuardAddsOnlySafeWorkflowRelativeReadPaths(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.SetWorkspacePath("Workflow/testing")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0/test"}

	config := &AgentConfigs{AdditionalReadPaths: []string{"variables", "reports/reference.json", "variables/"}}
	readPaths, writePaths := hcpo.setupExecutionFolderGuard(
		"step-1", "reader", KBAccessNone, LearningsAccessNone, DBAccessRead, config,
	)
	for _, expected := range []string{"Workflow/testing/variables", "Workflow/testing/reports/reference.json"} {
		if !slices.Contains(readPaths, expected) {
			t.Fatalf("additional read grant %q missing from %v", expected, readPaths)
		}
		if slices.Contains(writePaths, expected) {
			t.Fatalf("additional read grant %q unexpectedly widened writes: %v", expected, writePaths)
		}
	}

	unsafe := &AgentConfigs{AdditionalReadPaths: []string{"../other-workflow"}}
	unsafeReads, _ := hcpo.setupExecutionFolderGuard(
		"step-1", "reader", KBAccessNone, LearningsAccessNone, DBAccessRead, unsafe,
	)
	for _, granted := range unsafeReads {
		if strings.Contains(granted, "other-workflow") {
			t.Fatalf("unsafe traversal was granted: %v", unsafeReads)
		}
	}
}

func TestNormalizeAdditionalReadPathsRejectsEscapeAndCanonicalizes(t *testing.T) {
	got, err := normalizeAdditionalReadPaths([]string{" variables/ ", "reports/../variables", "reports/reference.json"})
	if err != nil {
		t.Fatalf("normalizeAdditionalReadPaths returned error: %v", err)
	}
	want := []string{"variables", "reports/reference.json"}
	if !slices.Equal(got, want) {
		t.Fatalf("normalized paths = %v, want %v", got, want)
	}
	for _, invalid := range [][]string{{"."}, {".."}, {"../outside"}, {"/tmp/outside"}, {`..\outside`}} {
		if _, err := normalizeAdditionalReadPaths(invalid); err == nil {
			t.Fatalf("normalizeAdditionalReadPaths(%v) accepted an unsafe path", invalid)
		}
	}
}

func TestCreateStandardAgentConfigUsesWorkflowFolderForCodingAgentWorkingDir(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"",
		0,
		"",
		[]string{"test-server"},
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
	base.SetWorkspacePath("Workflow/testing")
	base.SetMCPSessionID("workflow-session-123")

	config := base.CreateStandardAgentConfigWithLLM(
		"step-agent",
		1,
		agents.OutputFormatStructured,
		&orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider: string(mcpllm.ProviderCodexCLI),
				ModelID:  "gpt-5.3-codex-spark",
			},
		},
	)

	want := filepath.Join(docsRoot, "Workflow", "testing")
	if config.CodingAgentWorkingDir != want {
		t.Fatalf("CodingAgentWorkingDir = %q, want %q", config.CodingAgentWorkingDir, want)
	}
}

func TestApplyStepConfigToAgentConfigDefaultsCodingAgentTmuxCloseOnCompletion(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	config := agents.NewOrchestratorAgentConfig("step-agent")
	config.LLMConfig.Primary.Provider = string(mcpllm.ProviderCodexCLI)

	hcpo.applyStepConfigToAgentConfig(config, &AgentConfigs{}, true, false)

	if config.CodingAgentKeepAlive {
		t.Fatal("expected workflow step coding-agent tmux lifecycle to close on completion by default")
	}
}

func TestApplyStepConfigToAgentConfigSupportsCodingAgentTmuxKeepAlive(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	config := agents.NewOrchestratorAgentConfig("step-agent")
	config.LLMConfig.Primary.Provider = string(mcpllm.ProviderCodexCLI)

	hcpo.applyStepConfigToAgentConfig(config, &AgentConfigs{
		CodingAgentTmuxLifecycle: CodingAgentTmuxLifecycleKeepAlive,
	}, true, false)

	if !config.CodingAgentKeepAlive {
		t.Fatal("expected explicit keep_alive lifecycle to keep coding-agent tmux session alive")
	}
}

// TestApplyStepConfigToAgentConfigEnablesWorkspaceIsolation locks in
// the Phase C contract: applying step config to a workflow-step agent
// flips IsolateCodingAgentWorkspace=true. This is what makes the
// coding-CLI session run in a fresh os.MkdirTemp dir instead of
// CodingAgentWorkingDir, protecting the user's workflow files from
// concurrent-step collisions and accidental model writes.
//
// Chat code paths (pkg/agentwrapper/llm_agent.go) do NOT call
// applyStepConfigToAgentConfig, so this flag stays false there —
// chat sessions continue to operate directly on the user's chosen
// workspace dir for the "agent edits my files" UX.
func TestApplyStepConfigToAgentConfigEnablesWorkspaceIsolation(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	config := agents.NewOrchestratorAgentConfig("step-agent")
	config.LLMConfig.Primary.Provider = string(mcpllm.ProviderCodexCLI)

	if config.IsolateCodingAgentWorkspace {
		t.Fatal("OrchestratorAgentConfig must default IsolateCodingAgentWorkspace=false; chat code paths depend on the zero value being safe")
	}

	hcpo.applyStepConfigToAgentConfig(config, &AgentConfigs{}, true, false)

	if !config.IsolateCodingAgentWorkspace {
		t.Fatal("expected workflow step to enable IsolateCodingAgentWorkspace; without it, concurrent steps collide on CodingAgentWorkingDir and the model's built-in tools can mutate operator files")
	}
}

// TestAllWorkflowAgentFactoriesEnableWorkspaceIsolation verifies that every
// agent factory that produces a long-lived coding-CLI session flips
// IsolateCodingAgentWorkspace=true so the session runs in os.MkdirTemp instead
// of CodingAgentWorkingDir. These factories live in two files:
//   - controller_agent_factory.go (2): regular-step path (applyStepConfigToAgentConfig)
//     and the todo-task orchestrator (createOrchestratorAgent).
//   - interactive_workshop_manager.go (5): the workshop background agents — the
//     `run_in_background` task agent plus the review-plan / review-timing /
//     review-costs / review-step-code agents —
//     each spawns a coding-CLI
//     session for a workflow task and must isolate its workspace.
//
// Without isolation on any of these, an orchestrator / workshop background
// agent collides with the workshop chat's own coding-CLI session in the
// same workflow folder, and the step fails with a "does not support
// concurrent sessions in working directory ... with different MCP
// configs"-style error some coding CLIs raise.
//
// The test asserts a specific count per file. A new factory added later
// without isolation trips this test rather than shipping a silent regression.
// Conversely, removing one of these call sites by accident is also caught.
func TestAllWorkflowAgentFactoriesEnableWorkspaceIsolation(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{path: "controller_agent_factory.go", want: 2},     // regular + todo-task orchestrator
		{path: "interactive_workshop_manager.go", want: 3}, // review workshop agents
	}
	const needle = "config.IsolateCodingAgentWorkspace = true"
	for _, tc := range cases {
		source, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if got := strings.Count(string(source), needle); got != tc.want {
			t.Fatalf("%s: %q occurrences = %d, want %d (a workflow agent factory in this file must enable isolation; chat code paths in other files must not)", tc.path, needle, got, tc.want)
		}
	}
}

// TestWorkflowCLIStepsAlwaysUseStructuredTransport pins the workflow transport
// contract: a coding-agent CLI running a workflow step ALWAYS uses structured
// JSON, and there is no per-step override (the step_config "transport" field
// was removed). Non-CLI providers have no process transport at all.
//
// This replaces the previous always-tmux tests. The old rule was justified by a
// belief that Claude Code had dropped print/stream-json support; that is stale
// (mcpagent P0-certifies Claude structured multi-turn over native --resume).
func TestWorkflowCLIStepsAlwaysUseStructuredTransport(t *testing.T) {
	tests := []struct {
		name                string
		provider            string
		wantTransport       string
		wantForceStructured bool
	}{
		{name: "codex", provider: string(mcpllm.ProviderCodexCLI), wantTransport: "structured", wantForceStructured: true},
		{name: "claude", provider: string(mcpllm.ProviderClaudeCode), wantTransport: "structured", wantForceStructured: true},
		{name: "cursor", provider: string(mcpllm.ProviderCursorCLI), wantTransport: "structured", wantForceStructured: true},
		{name: "pi", provider: string(mcpllm.ProviderPiCLI), wantTransport: "structured", wantForceStructured: true},
		{name: "api provider has no transport", provider: "anthropic", wantTransport: "", wantForceStructured: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcpo := newAgentFactoryTestOrchestrator(t)
			config := agents.NewOrchestratorAgentConfig("step-agent")
			config.LLMConfig.Primary.Provider = tt.provider

			got := hcpo.applyWorkflowTransportToAgentConfig(config, nil, "test agent")

			if got != tt.wantTransport {
				t.Fatalf("transport = %q, want %q", got, tt.wantTransport)
			}
			if config.ForceStructuredCodingAgent != tt.wantForceStructured {
				t.Fatalf("ForceStructuredCodingAgent = %v, want %v", config.ForceStructuredCodingAgent, tt.wantForceStructured)
			}
		})
	}
}

func newAgentFactoryTestOrchestrator(t *testing.T) *StepBasedWorkflowOrchestrator {
	t.Helper()
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"",
		0,
		"",
		[]string{"api-bridge"},
		[]string{"api-bridge:execute_shell_command"},
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
	return &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
}

func TestConfigureSubAgentSessionGuardInheritsNotificationDestination(t *testing.T) {
	const parentSessionID = "workflow-notification-parent"
	const childSessionID = "workflow-notification-child"
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath("Workflow/demo")
	hcpo.SetMCPSessionID(parentSessionID)
	virtualtools.RegisterSessionNotificationDestination(parentSessionID, &services.NotificationDestination{
		WorkflowName:  "demo",
		WorkspacePath: "Workflow/demo",
	})
	t.Cleanup(func() {
		virtualtools.DeleteSessionNotificationDestination(parentSessionID)
		virtualtools.DeleteSessionNotificationDestination(childSessionID)
		common.ClearSessionShellConfig(childSessionID)
	})

	hcpo.configureSubAgentSessionGuard(childSessionID, "message-sequence", "daily-digest", nil, nil)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, childSessionID)
	dest := virtualtools.NotificationDestinationFromContext(ctx)
	if dest == nil || dest.WorkspacePath != "Workflow/demo" || dest.WorkflowName != "demo" {
		t.Fatalf("child notification destination = %#v", dest)
	}
}

func TestInjectStepEnvIntoShellExecutor_OverridesStaleMCPSessionEnv(t *testing.T) {
	t.Setenv("MCP_API_URL", "http://example.test/s/parent-session")
	t.Setenv("MCP_API_TOKEN", "step-token")

	var capturedArgs map[string]interface{}
	var capturedSessionID string
	executors := map[string]interface{}{
		"execute_shell_command": func(ctx context.Context, args map[string]interface{}) (string, error) {
			capturedArgs = args
			capturedSessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
			return "ok", nil
		},
	}

	injectStepEnvIntoShellExecutor(
		executors,
		"/tmp/workflow/execution/math-solver",
		"/tmp/workflow/execution",
		"/tmp/workflow/db/db.sqlite",
		"iteration-0/math",
		"step-session-123",
		map[string]string{
			"VAR_LOGIN_EMAIL":       "configured@example.com",
			"SECRET_LOGIN_PASSWORD": "password",
			"MCP_SESSION_ID":        "stale-runtime-session",
		},
	)

	execFn, ok := executors["execute_shell_command"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		t.Fatal("expected wrapped execute_shell_command executor")
	}

	_, err := execFn(context.Background(), map[string]interface{}{
		"command": "env",
		"extra_env": map[string]interface{}{
			"MCP_SESSION_ID":     "parent-session",
			"MCP_API_URL":        "http://example.test/s/parent-session",
			"MCP_API_TOKEN":      "step-token",
			"STEP_OUTPUT_DIR":    "/stale/output",
			"STEP_EXECUTION_DIR": "/stale/execution",
		},
	})
	if err != nil {
		t.Fatalf("wrapped execute_shell_command returned error: %v", err)
	}

	rawExtraEnv, ok := capturedArgs["extra_env"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected extra_env map, got %#v", capturedArgs["extra_env"])
	}

	if got := rawExtraEnv["STEP_OUTPUT_DIR"]; got != "/tmp/workflow/execution/math-solver" {
		t.Fatalf("expected STEP_OUTPUT_DIR override, got %#v", got)
	}
	if got := rawExtraEnv["STEP_EXECUTION_DIR"]; got != "/tmp/workflow/execution" {
		t.Fatalf("expected STEP_EXECUTION_DIR override, got %#v", got)
	}
	if got := rawExtraEnv["RUN_FOLDER"]; got != "iteration-0/math" {
		t.Fatalf("expected RUN_FOLDER override, got %#v", got)
	}
	if got := rawExtraEnv["MCP_SESSION_ID"]; got != "step-session-123" {
		t.Fatalf("expected MCP_SESSION_ID override, got %#v", got)
	}
	if got := rawExtraEnv["MCP_API_URL"]; got != "http://example.test/s/step-session-123" {
		t.Fatalf("expected step-scoped MCP_API_URL, got %#v", got)
	}
	if got := rawExtraEnv["MCP_CUSTOM"]; got != "http://example.test/s/step-session-123/tools/custom" {
		t.Fatalf("expected step-scoped MCP_CUSTOM, got %#v", got)
	}
	if got := rawExtraEnv["MCP_AUTH"]; got != "Authorization: Bearer step-token" {
		t.Fatalf("expected MCP_AUTH, got %#v", got)
	}
	if got := rawExtraEnv["VAR_LOGIN_EMAIL"]; got != "configured@example.com" {
		t.Fatalf("expected resolved workflow variable, got %#v", got)
	}
	if got := rawExtraEnv["SECRET_LOGIN_PASSWORD"]; got != "password" {
		t.Fatalf("expected workflow secret, got %#v", got)
	}
	if capturedSessionID != "step-session-123" {
		t.Fatalf("expected trusted step session in executor context, got %q", capturedSessionID)
	}
}

func TestSetupExecutionFolderGuardHonorsLearningsAndKBNone(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
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
	base.SetWorkspacePath("Workflow/testing")

	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "iteration-0/test-group",
	}

	readPaths, writePaths := hcpo.setupExecutionFolderGuard(
		"step-1",
		"forbidden-probe",
		KBAccessNone,
		LearningsAccessNone,
		DBAccessReadWrite,
		nil,
	)

	forbiddenReads := []string{
		"Workflow/testing/learnings/_global",
		"Workflow/testing/knowledgebase",
		"Workflow/testing",
	}
	for _, forbidden := range forbiddenReads {
		if slices.Contains(readPaths, forbidden) {
			t.Fatalf("expected read paths not to include %q, got %v", forbidden, readPaths)
		}
	}
	forbiddenWrites := []string{
		"Workflow/testing/learnings/_global",
		"Workflow/testing/learnings/forbidden-probe",
		"Workflow/testing/knowledgebase",
		"Workflow/testing/knowledgebase/notes",
	}
	for _, forbidden := range forbiddenWrites {
		if slices.Contains(writePaths, forbidden) {
			t.Fatalf("expected write paths not to include %q, got %v", forbidden, writePaths)
		}
	}
}

func TestSetupExecutionFolderGuardGivesGenericReviewerWorkflowWideReadOnlyView(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.SetWorkspacePath("Workflow/testing")
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "iteration-0/test-group",
	}

	readPaths, writePaths := hcpo.setupExecutionFolderGuard(
		"parent-generic-report-health",
		"generic-parent-report-health",
		KBAccessRead,
		LearningsAccessRead,
		DBAccessRead,
		nil,
	)

	if !slices.Contains(readPaths, "Workflow/testing") {
		t.Fatalf("generic reviewer must read the workflow root, got %v", readPaths)
	}
	for _, forbidden := range []string{
		"Workflow/testing",
		"Workflow/testing/planning",
		"Workflow/testing/evaluation",
		"Workflow/testing/reports",
	} {
		if slices.Contains(writePaths, forbidden) {
			t.Fatalf("generic reviewer unexpectedly writes %q: %v", forbidden, writePaths)
		}
	}
}

// Every folder-guard builder must grant tool_output_folder. It is where any
// bridge tool result past its inline cap is spilled (MCP_TOOL_OUTPUT_DIR), so an
// agent handed "full output saved to <path>" needs a legal way to read it back.
//
// setupExecutionFolderGuard has granted it since PLAT-073 cluster F, but the two
// parallel builders never did, and nothing pinned the parity. Confirmed live
// 2026-08-17 (confida-login step-5-execute-browser-and-capture-apis, a
// message_sequence step): its read paths carried no tool_output_folder, and a
// spilled agent_browser result came back "outside every workspace root" with no
// recoverable path — a dead end that cost the step a full round trip.
func TestEveryFolderGuardBuilderGrantsToolOutputFolder(t *testing.T) {
	newOrch := func() *StepBasedWorkflowOrchestrator {
		base, err := orchestrator.NewBaseOrchestrator(
			loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
			"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
		)
		if err != nil {
			t.Fatalf("NewBaseOrchestrator returned error: %v", err)
		}
		base.SetWorkspacePath("Workflow/testing")
		return &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0/test-group"}
	}
	const want = "Workflow/testing/tool_output_folder"

	t.Run("execution", func(t *testing.T) {
		readPaths, _ := newOrch().setupExecutionFolderGuard("step-1", "s", KBAccessNone, LearningsAccessNone, DBAccessRead, nil)
		if !slices.Contains(readPaths, want) {
			t.Fatalf("read paths missing %q: %v", want, readPaths)
		}
	})
	// PLAT-284: every execution step gets the workflow's persistent
	// .sandbox-cache writable, even a read-only one with no root-wide grant --
	// that is what lets "pip install --user" on run 1 be "already satisfied"
	// on run 2 instead of a fresh PyPI download in the rotating run folder.
	t.Run("execution grants the persistent sandbox cache", func(t *testing.T) {
		// Literal on purpose: the on-disk name is the cross-module contract the
		// sandbox matches on (security.SandboxPersistentDirName); renaming the
		// constant without renaming folders must fail here.
		const cache = "Workflow/testing/.sandbox-cache"
		readPaths, writePaths := newOrch().setupExecutionFolderGuard("step-1", "s", KBAccessNone, LearningsAccessNone, DBAccessRead, nil)
		if !slices.Contains(writePaths, cache) {
			t.Fatalf("write paths missing %q: %v", cache, writePaths)
		}
		if !slices.Contains(readPaths, cache) {
			t.Fatalf("read paths missing %q: %v", cache, readPaths)
		}
	})
	t.Run("message_sequence", func(t *testing.T) {
		readPaths, _ := newOrch().setupMessageSequenceFolderGuard("step-1", "s", nil, MessageSequenceWriteAccess{})
		if !slices.Contains(readPaths, want) {
			t.Fatalf("read paths missing %q: %v", want, readPaths)
		}
	})
	t.Run("kb_update", func(t *testing.T) {
		readPaths, _ := newOrch().setupKBUpdateFolderGuard("s", "step-1")
		if !slices.Contains(readPaths, want) {
			t.Fatalf("read paths missing %q: %v", want, readPaths)
		}
	})
}

func TestSetupExecutionFolderGuardAddsOnlyConfiguredStores(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
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
	base.SetWorkspacePath("Workflow/testing")

	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "iteration-0/test-group",
	}

	readPaths, writePaths := hcpo.setupExecutionFolderGuard(
		"step-1",
		"kb-direct",
		KBAccessReadWrite,
		LearningsAccessRead,
		DBAccessReadWrite,
		nil,
	)

	for _, expected := range []string{
		"Workflow/testing/learnings/_global",
		"Workflow/testing/learnings/kb-direct",
		"Workflow/testing/knowledgebase",
	} {
		if !slices.Contains(readPaths, expected) {
			t.Fatalf("expected read paths to include %q, got %v", expected, readPaths)
		}
	}
	if !slices.Contains(writePaths, "Workflow/testing/knowledgebase/notes") {
		t.Fatalf("expected write paths to include KB notes for direct writes, got %v", writePaths)
	}
	if slices.Contains(writePaths, "Workflow/testing/learnings/_global") {
		t.Fatalf("main execution should not write global learnings, got %v", writePaths)
	}
	if slices.Contains(writePaths, "Workflow/testing/learnings/kb-direct") {
		t.Fatalf("main execution should not write step learnings, got %v", writePaths)
	}
}

func TestAppendLearningReadPathsScopesAccessToCurrentStep(t *testing.T) {
	got := appendLearningReadPaths(nil, "Workflow/testing", "score-and-plan")
	for _, expected := range []string{
		"Workflow/testing/learnings/_global",
		"Workflow/testing/learnings/score-and-plan",
	} {
		if !slices.Contains(got, expected) {
			t.Fatalf("expected learning read paths to include %q, got %v", expected, got)
		}
	}
	if slices.Contains(got, "Workflow/testing/learnings/other-step") {
		t.Fatalf("learning read paths unexpectedly include another step: %v", got)
	}
}

func TestAppendLearningReadPathsRejectsNonSegmentStepID(t *testing.T) {
	got := appendLearningReadPaths(nil, "Workflow/testing", "../other-workflow")
	if len(got) != 1 || got[0] != "Workflow/testing/learnings/_global" {
		t.Fatalf("unsafe step ID widened learning read scope: %v", got)
	}
}

func TestApplyStepConfigToAgentConfigForcesCodeExecForCLIProviders(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"",
		0,
		"",
		[]string{"test-server"},
		nil,
		false,
		nil,
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}

	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	config := agents.NewOrchestratorAgentConfig("test-agent")
	config.LLMConfig.Primary.Provider = "claude-code"

	hcpo.applyStepConfigToAgentConfig(config, nil, false, false)

	if !config.UseCodeExecutionMode {
		t.Fatalf("expected CLI providers to have code execution mode enabled")
	}
}

func TestClaudeCodeTransportHelpers(t *testing.T) {
	stepConfig := agents.NewOrchestratorAgentConfig("step-agent")
	stepConfig.LLMConfig.Primary.Provider = string(mcpllm.ProviderClaudeCode)
	forceWorkflowClaudeCodeInteractiveTransport(stepConfig)
	if stepConfig.ClaudeCodeTransport != mcpllm.ClaudeCodeTransportTmux {
		t.Fatalf("step ClaudeCodeTransport = %q, want %q", stepConfig.ClaudeCodeTransport, mcpllm.ClaudeCodeTransportTmux)
	}

	chatConfig := agents.NewOrchestratorAgentConfig("workflow-builder-agent")
	chatConfig.LLMConfig.Primary.Provider = string(mcpllm.ProviderClaudeCode)
	forceWorkflowClaudeCodeInteractiveTransport(chatConfig)
	if chatConfig.ClaudeCodeTransport != mcpllm.ClaudeCodeTransportTmux {
		t.Fatalf("chat ClaudeCodeTransport = %q, want %q", chatConfig.ClaudeCodeTransport, mcpllm.ClaudeCodeTransportTmux)
	}
}

// TestWorkflowClaudeCodeUsesStructuredTransport pins that Claude Code in a
// workflow step gets structured JSON like every other CLI provider, and that
// ClaudeCodeTransport (which only distinguishes tmux variants and has no
// structured value) is NOT pinned to tmux in that case — pinning it there would
// leave the config self-contradictory.
func TestWorkflowClaudeCodeUsesStructuredTransport(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	c := agents.NewOrchestratorAgentConfig("workflow-runtime-agent")
	c.LLMConfig.Primary.Provider = string(mcpllm.ProviderClaudeCode)

	got := hcpo.applyWorkflowTransportToAgentConfig(c, nil, "runtime")

	if got != "structured" {
		t.Fatalf("transport = %q, want structured", got)
	}
	if !c.ForceStructuredCodingAgent {
		t.Fatal("ForceStructuredCodingAgent = false, want true")
	}
	if c.ClaudeCodeTransport == mcpllm.ClaudeCodeTransportTmux {
		t.Fatal("ClaudeCodeTransport pinned to tmux while running structured; that contradicts the selected transport")
	}
}

func TestUnattendedWorkshopClaudeCodeUsesStructuredTransport(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetMCPSessionID("pulse-reviewer-transport-test")
	iwm := &InteractiveWorkshopManager{controller: hcpo}
	config := iwm.createUnattendedWorkshopAgentConfig("pulse-reviewer", 100, &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{
			Provider: string(mcpllm.ProviderClaudeCode),
			ModelID:  "claude-opus-5",
		},
	}, "Pulse reviewer")

	if !config.ForceStructuredCodingAgent {
		t.Fatal("ForceStructuredCodingAgent = false, want true")
	}
	if config.LLMConfig.Primary.Provider != string(mcpllm.ProviderClaudeCode) {
		t.Fatalf("provider = %q, want %q", config.LLMConfig.Primary.Provider, mcpllm.ProviderClaudeCode)
	}
}

func TestSelectExecutionLLM_PrefersStepExecutionLLMOverSubAgentAndTiered(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
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
		BaseOrchestrator: base,
		tierResolver: NewTierResolver(&TieredLLMConfig{
			Tier1: &AgentLLMConfig{Provider: "openai", ModelID: "tier-1"},
			Tier2: &AgentLLMConfig{Provider: "openai", ModelID: "tier-2"},
			Tier3: &AgentLLMConfig{Provider: "openai", ModelID: "tier-3"},
		}, nil),
	}

	ctx := context.WithValue(context.Background(), workshopTierContextKey{}, int(TierLow))
	ctx = context.WithValue(ctx, virtualtools.SubAgentLLMContextKey, &AgentLLMConfig{
		Provider: "openai",
		ModelID:  "sub-agent",
	})

	cfg := &AgentConfigs{
		ExecutionLLM: &AgentLLMConfig{
			Provider: "openai",
			ModelID:  "step-override",
		},
	}

	llm := hcpo.selectExecutionLLM(ctx, cfg, "step-1")
	if llm == nil {
		t.Fatal("expected execution llm config, got nil")
	}
	if llm.Primary.ModelID != "step-override" {
		t.Fatalf("expected step override model, got %q", llm.Primary.ModelID)
	}

}

func TestSelectExecutionLLM_UsesTierResolverWhenStepExecutionLLMIsUnset(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
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
		BaseOrchestrator: base,
		tierResolver: NewTierResolver(&TieredLLMConfig{
			Tier1: &AgentLLMConfig{Provider: "openai", ModelID: "tier-1"},
			Tier2: &AgentLLMConfig{Provider: "openai", ModelID: "tier-2"},
			Tier3: &AgentLLMConfig{Provider: "openai", ModelID: "tier-3"},
		}, nil),
	}

	// Repeated executions do not progressively change the tier. Only an
	// explicit setting (including a reviewed trial) changes model selection.
	for _, tc := range []struct {
		name   string
		config AgentConfigs
		want   string
	}{
		{"default execution", AgentConfigs{}, "tier-1"},
		{"reviewed medium", AgentConfigs{ExecutionTier: "medium"}, "tier-2"},
		{"reviewed low", AgentConfigs{ExecutionTier: "low"}, "tier-3"},
		{"cleared returns to high", AgentConfigs{}, "tier-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for run := 0; run < 5; run++ {
				llm := hcpo.selectExecutionLLM(context.Background(), &tc.config, "step-1")
				if llm == nil || llm.Primary.ModelID != tc.want {
					t.Fatalf("run %d: expected %s, got %+v", run, tc.want, llm)
				}
			}
		})
	}
}

func TestSelectExecutionLLM_UsesFixedExecutionTier(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
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
		BaseOrchestrator: base,
		tierResolver: NewTierResolver(&TieredLLMConfig{
			Tier1: &AgentLLMConfig{Provider: "openai", ModelID: "tier-1"},
			Tier2: &AgentLLMConfig{Provider: "openai", ModelID: "tier-2"},
			Tier3: &AgentLLMConfig{Provider: "openai", ModelID: "tier-3"},
		}, nil),
	}

	llm := hcpo.selectExecutionLLM(context.Background(), &AgentConfigs{ExecutionTier: "medium"}, "step-1")
	if llm == nil {
		t.Fatal("expected execution llm config, got nil")
	}
	if llm.Primary.ModelID != "tier-2" {
		t.Fatalf("expected tier-2 model for fixed medium execution_tier, got %q", llm.Primary.ModelID)
	}
}

func TestSelectExecutionLLM_WorkshopTierOverrideBeatsFixedExecutionTier(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
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
		BaseOrchestrator: base,
		tierResolver: NewTierResolver(&TieredLLMConfig{
			Tier1: &AgentLLMConfig{Provider: "openai", ModelID: "tier-1"},
			Tier2: &AgentLLMConfig{Provider: "openai", ModelID: "tier-2"},
			Tier3: &AgentLLMConfig{Provider: "openai", ModelID: "tier-3"},
		}, nil),
	}

	ctx := context.WithValue(context.Background(), workshopTierContextKey{}, int(TierLow))
	llm := hcpo.selectExecutionLLM(ctx, &AgentConfigs{ExecutionTier: "medium"}, "step-1")
	if llm == nil {
		t.Fatal("expected execution llm config, got nil")
	}
	if llm.Primary.ModelID != "tier-3" {
		t.Fatalf("expected workshop override to win with tier-3 model, got %q", llm.Primary.ModelID)
	}
}

// Steps can now READ planning/ but must never write it. Before this, a step had
// no way to see the plan at all — only its own description and resolved
// dependencies — so it could not tell whether it was the first of nine or the
// last. Write access stays impossible: plan.json and step_config.json are only
// mutated through the typed plan-mod tools, which validate schemas and emit
// events; a raw shell write would corrupt them silently.
func TestExecutionFolderGuardGrantsPlanningReadNeverWrite(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.SetWorkspacePath("Workflow/testing")
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "iteration-0/default-group",
	}

	readPaths, writePaths := hcpo.setupExecutionFolderGuard(
		"step-1", "some-step", KBAccessNone, LearningsAccessNone, DBAccessReadWrite,
		nil,
	)
	planningPath := "Workflow/testing/planning"
	if !slices.Contains(readPaths, planningPath) {
		t.Fatalf("step must be able to read %q, got %v", planningPath, readPaths)
	}
	for _, w := range writePaths {
		if strings.Contains(w, "/planning") {
			t.Fatalf("planning/ must never be writable by a step, got write path %q in %v", w, writePaths)
		}
	}
}

// A step is told its outputs live at runs/<run>/execution/<step>/, and confirming
// that path means walking down to it. Granting only the execution/ leaf made the
// walk fail: `ls runs/iteration-0/default` returned "Operation not permitted"
// while the deeper path would have worked. A CDP test step on 2026-08-02 spent
// four calls discovering that. The run folder also holds logs/ and
// run_metadata.json, which are ordinary evidence for a step reasoning about its
// own run.
func TestExecutionFolderGuardGrantsTheRunFolderNotOnlyItsExecutionChild(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.SetWorkspacePath("Workflow/testing")
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "iteration-0/default",
	}

	readPaths, _ := hcpo.setupExecutionFolderGuard(
		"step-0-cdp-test", "step-0-cdp-test", KBAccessNone, LearningsAccessNone,
		resolveDBAccess(nil),
		nil,
	)

	runPath := "Workflow/testing/runs/iteration-0/default"
	if !slices.Contains(readPaths, runPath) {
		t.Fatalf("step cannot list its own run folder %q; got %v", runPath, readPaths)
	}
	// The narrower grant must remain — widening to the run folder should not have
	// replaced the execution grant that sibling-step results depend on.
	execPath := runPath + "/execution"
	if !slices.Contains(readPaths, execPath) {
		t.Fatalf("execution read grant was lost, got %v", readPaths)
	}
	// Read must not reach the workflow root: that was denied before and should
	// stay denied — this fix widens by exactly one level, not to everything.
	if slices.Contains(readPaths, "Workflow/testing") {
		t.Fatalf("read scope widened to the workflow root, got %v", readPaths)
	}
}

// TestExecutionFolderGuardGrantsToolOutputFolderRead pins the PLAT-073
// cluster F fix (dd9ede3c): mcpagent spills any bridge tool result over its
// inline size cap (a large agent_browser snapshot, chief among them) to
// MCP_TOOL_OUTPUT_DIR, which resolves to <workspace-root>/tool_output_folder
// — a sibling of runs/ that was previously outside every granted read path.
// A step told to read its own spilled output back had no legal way to
// comply. This must be readable without widening to the bare workflow root.
func TestExecutionFolderGuardGrantsToolOutputFolderRead(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0,
		"", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.SetWorkspacePath("Workflow/testing")
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "iteration-0/default",
	}

	readPaths, _ := hcpo.setupExecutionFolderGuard(
		"step-1", "some-step", KBAccessNone, LearningsAccessNone,
		resolveDBAccess(nil),
		nil,
	)

	toolOutputPath := "Workflow/testing/tool_output_folder"
	if !slices.Contains(readPaths, toolOutputPath) {
		t.Fatalf("step must be able to read its own spilled tool output at %q, got %v", toolOutputPath, readPaths)
	}
	if slices.Contains(readPaths, "Workflow/testing") {
		t.Fatalf("granting tool_output_folder read must not widen to the bare workflow root, got %v", readPaths)
	}
}

// A scheduler Pulse turn's Goal Work child gets the Pulse model; its other
// children (Plan Drift, Technical, Architecture) get the Medium tier; any
// other background task follows the Builder (phase) model. Missing models fall
// back rather than failing.
func TestSelectBackgroundTaskLLMRoutesGoalWorkToPulseModelAndUpkeepToMediumTier(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.presetPhaseLLM = &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-opus-5-5"}
	hcpo.presetPulseLLM = &AgentLLMConfig{Provider: "codex-cli", ModelID: "gpt-5.5"}
	hcpo.tierResolver = NewTierResolver(&TieredLLMConfig{
		Tier1: &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5"},
		Tier2: &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-haiku-4-5-20251001"},
	}, nil)

	if got := hcpo.selectBackgroundTaskLLM(false, "strategic_review", "background task agent"); got == nil || got.Primary.ModelID != "claude-opus-5-5" {
		t.Fatalf("ordinary background task = %+v, want the Builder/phase model", got)
	}
	if got := hcpo.selectBackgroundTaskLLM(true, "strategic_review", "Goal Work"); got == nil || got.Primary.Provider != "codex-cli" || got.Primary.ModelID != "gpt-5.5" {
		t.Fatalf("Goal Work = %+v, want pulse_llm", got)
	}
	for _, module := range []string{"technical_review", "architecture_review", ""} {
		if got := hcpo.selectBackgroundTaskLLM(true, module, "upkeep"); got == nil || got.Primary.ModelID != "claude-haiku-4-5-20251001" {
			t.Fatalf("Pulse upkeep child %q = %+v, want the Medium tier", module, got)
		}
	}

	hcpo.tierResolver = nil
	if got := hcpo.selectBackgroundTaskLLM(true, "technical_review", "upkeep"); got == nil || got.Primary.ModelID != "gpt-5.5" {
		t.Fatalf("upkeep without a Medium tier = %+v, want the Pulse model fallback", got)
	}
	hcpo.presetPulseLLM = nil
	if got := hcpo.selectBackgroundTaskLLM(true, "strategic_review", "Goal Work"); got == nil || got.Primary.ModelID != "claude-opus-5-5" {
		t.Fatalf("Goal Work without pulse_llm = %+v, want the phase model fallback", got)
	}
}

// Historical counters may still be present on disk, but the execution path
// must not turn them into a model override before consulting explicit config.
func TestExecutionDoesNotInjectHistoryBasedTierOverrides(t *testing.T) {
	src := readSourceFile(t, "controller_execution.go")
	for _, retired := range []string{"decideAdaptiveExecutionTier", "recordAdaptiveExecutionTierSuccess", "DescriptionHashRuns", "PreferredExecutionTier", "context.WithValue(executionAgentCtx, WorkshopTierOverrideKey"} {
		if strings.Contains(src, retired) {
			t.Fatalf("execution still derives a tier override from retired history: %s", retired)
		}
	}
}

func TestStepExecutionScopeUsesOwningAgentSubtree(t *testing.T) {
	tests := map[string]string{
		"/workspace/runs/run/execution/parent/agents/research/calls/call-1":                           "/workspace/runs/run/execution/parent",
		"/workspace/runs/run/execution/parent/scripts/items/collect/calls/call-1":                     "/workspace/runs/run/execution/parent",
		"/workspace/runs/run/execution/parent/agents/research/calls/call-1/agents/check/calls/call-2": "/workspace/runs/run/execution/parent/agents/research/calls/call-1",
		"/workspace/runs/run/execution/top-level":                                                     "/workspace/runs/run/execution",
	}
	for output, want := range tests {
		if got := stepExecutionScopeAbsPath(output); got != want {
			t.Errorf("stepExecutionScopeAbsPath(%q) = %q, want %q", output, got, want)
		}
	}
}
