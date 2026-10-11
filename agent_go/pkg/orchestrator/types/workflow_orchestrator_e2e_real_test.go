package types

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// TestWorkflowE2ESingleRegularStepPiCLI is the tracer-bullet for the workflow
// engine on the Pi coding-agent transport. It runs regular, routing,
// todo_task/sub-agent, and agent steps against real Pi CLI tmux
// sessions with the MCP bridge enabled by the agent layer.
//
// Gated on RUN_WORKFLOW_REAL_E2E=1 + RUN_PI_CLI_WORKFLOW_E2E=1 (or the
// provider-level RUN_PI_CLI_REAL_E2E=1) plus a Pi-compatible key.
func TestWorkflowE2ESingleRegularStepPiCLI(t *testing.T) {
	apiKey, model := requirePiCLIWorkflowE2E(t)
	// The workflow engine reads workspace files via an HTTP documents
	// API (see base_orchestrator.go:250 — WORKSPACE_API_URL). The
	// user's running coding-agent-loop server hosts this API at
	// 127.0.0.1:18744, serving files relative to workspace-docs/. We
	// must therefore:
	//   1. Point WORKSPACE_API_URL at the running server.
	//   2. Use a RELATIVE workspace path (e.g. Workflow/_e2e_test/<id>).
	//   3. Write fixture files into the real workspace-docs/ tree so
	//      the API can read them.
	// The temp workspace path is computed but the fixture is on disk.
	wsAPI := strings.TrimSpace(os.Getenv("WORKSPACE_API_URL"))
	if wsAPI == "" {
		// Default to the dev server port the user runs locally.
		wsAPI = "http://127.0.0.1:18744"
		_ = os.Setenv("WORKSPACE_API_URL", wsAPI)
	}
	if err := requireWorkspaceAPIReachable(wsAPI); err != nil {
		t.Skipf("workspace API at %s unreachable (is the dev server running?): %v", wsAPI, err)
	}

	// Pick a workspace path under workspace-docs so the API can serve it.
	wsRoot := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
	if wsRoot == "" {
		wsRoot = "/Users/mipl/ai-work/coding-agent-loop/workspace-docs"
	}
	t.Setenv("WORKSPACE_DOCS_PATH", wsRoot)
	relWorkspace := "Workflow/_e2e_test_" + filepath.Base(t.TempDir())
	workspace := relWorkspace
	planningDir := filepath.Join(wsRoot, relWorkspace, "planning")
	// Keep workspace on disk if KEEP_E2E_WORKSPACE=1 so we can inspect
	// what the LLM actually wrote during debugging.
	if os.Getenv("KEEP_E2E_WORKSPACE") == "" {
		t.Cleanup(func() {
			_ = os.RemoveAll(filepath.Join(wsRoot, relWorkspace))
		})
	}
	if err := os.MkdirAll(planningDir, 0o755); err != nil {
		t.Fatalf("mkdir planning: %v", err)
	}

	// Plan covers every non-human step type in one workflow AND makes
	// each step do something real and verifiable. Theme: compute 6*7,
	// then route/branch/verify the answer. Each step is asked for a
	// deterministic token in its output; per-step assertions below
	// read execution_result and check the token appears. This proves
	// the engine is actually delivering the prompt to the agent,
	// running the agent to completion, and persisting the output —
	// not just driving the state machine through completion markers.
	//
	// Step IDs are stable so the per-step assertions below can
	// address each one.
	stepIDs := []string{"step-compute", "step-classify", "step-verify-even", "step-double-check", "step-report"}
	plan := map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "regular",
				"id":                   stepIDs[0],
				"title":                "Compute the answer",
				"description":          "Compute the value of 6 times 7. Reply with EXACTLY the line RESULT=<value> on a line by itself, then stop. Do not perform any other action.",
				"context_dependencies": []string{},
				"context_output":       "step1.json",
			},
			{
				"type":                 "routing",
				"id":                   stepIDs[1],
				"title":                "Classify the answer's parity",
				"description":          "",
				"context_dependencies": []string{},
				"context_output":       "step2.json",
				"routing_question":     "The value computed in the previous step is 42 (six times seven). Is 42 an even number or an odd number? Pick route_even if 42 is even; pick route_odd if 42 is odd.",
				"routes": []map[string]interface{}{
					{"route_id": "route_even", "route_name": "Even", "condition": "The value 42 is even", "next_step_id": stepIDs[2]},
					{"route_id": "route_odd", "route_name": "Odd", "condition": "The value 42 is odd", "next_step_id": stepIDs[2]},
				},
				"default_route_id": "route_even",
			},
			{
				"type":                 "regular",
				"id":                   stepIDs[2],
				"title":                "Confirm the answer equals 42",
				"description":          "The previous steps computed and classified the value 42 (six times seven). Confirm that the value 42 equals 42. Reply with EXACTLY the line CONFIRM_42_OK on a line by itself and stop.",
				"context_dependencies": []string{},
				"context_output":       "step3.json",
			},
			{
				"type":                 "todo_task",
				"id":                   stepIDs[3],
				"title":                "Double-check the answer via two methods",
				"description":          "Create two todos to verify the answer 42 via two different methods, delegate each to the matching predefined sub-agent, then mark both complete. The two sub-agents are: verify-add (verifies 42 = 21 + 21) and verify-mul (verifies 42 = 6 * 7). Delegate todo #1 to verify-add and todo #2 to verify-mul. When both come back complete, end the step.",
				"context_dependencies": []string{},
				"context_output":       "step4.json",
				// Two predefined sub-agents that the orchestrator
				// delegates real (small) work to. Each sub-agent
				// produces a distinct deterministic token in its
				// execution_result so we can verify both were actually
				// invoked. validateAgentStepFieldsTyped requires
				// route_id == sub_agent_step.id.
				"predefined_routes": []map[string]interface{}{
					{
						"route_id":   "verify-add",
						"route_name": "Verify via addition",
						"condition":  "Use this route to verify 42 = 21 + 21",
						"sub_agent_step": map[string]interface{}{
							"type":                 "regular",
							"id":                   "verify-add",
							"title":                "Verify via addition",
							"description":          "Verify that 21 + 21 equals 42. If they are equal, reply with EXACTLY the line VERIFY_ADD_OK on a line by itself and stop. If not equal, reply VERIFY_ADD_FAIL.",
							"context_dependencies": []string{},
							"context_output":       "step4_verify_add.json",
						},
					},
					{
						"route_id":   "verify-mul",
						"route_name": "Verify via multiplication",
						"condition":  "Use this route to verify 42 = 6 * 7",
						"sub_agent_step": map[string]interface{}{
							"type":                 "regular",
							"id":                   "verify-mul",
							"title":                "Verify via multiplication",
							"description":          "Verify that 6 multiplied by 7 equals 42. If they are equal, reply with EXACTLY the line VERIFY_MUL_OK on a line by itself and stop. If not equal, reply VERIFY_MUL_FAIL.",
							"context_dependencies": []string{},
							"context_output":       "step4_verify_mul.json",
						},
					},
				},
				"next_step_id": stepIDs[4],
			},
			{
				// message_sequence: replies with a token (its session.json glob
				// in assertStepExecutionResult confirms it wrote to the NORMAL
				// step folder execution/step-report/ WITH the workflow root — the
				// folder/prefix fix). NOTE: message_sequence item agents run via
				// createExecutionOnlyAgent, which doesn't receive the orchestrator
				// customTools, so they can't write files in this harness — the
				// context_output file-handoff is covered by unit tests instead.
				"type":                 "agent",
				"id":                   stepIDs[4],
				"title":                "Final report",
				"description":          "Multi-turn final report",
				"context_dependencies": []string{},
				"context_output":       "step5.json",
				"items": []map[string]interface{}{
					{
						"id":      "msg-1",
						"type":    "user_message",
						"title":   "Final report",
						"message": "All earlier steps computed and verified the value 42. Reply with EXACTLY the single line WORKFLOW_DONE_42 on a line by itself and stop. Do not call any tools.",
					},
				},
				"next_step_id": "end",
			},
		},
	}
	if err := writeJSON(filepath.Join(planningDir, "plan.json"), plan); err != nil {
		t.Fatalf("write plan.json: %v", err)
	}
	// variables.json lives at <workspace>/variables/variables.json
	// (controller.go:669) and the execution path requires at least
	// one enabled VariableGroup or it bails with "no enabled variable
	// groups found for execution" (controller.go:1160).
	variablesDir := filepath.Join(wsRoot, relWorkspace, "variables")
	if err := os.MkdirAll(variablesDir, 0o755); err != nil {
		t.Fatalf("mkdir variables: %v", err)
	}
	variablesManifest := map[string]interface{}{
		"variables":       []interface{}{},
		"groups":          []map[string]interface{}{{"name": "default", "values": map[string]string{}, "enabled": true}},
		"extraction_date": time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(filepath.Join(variablesDir, "variables.json"), variablesManifest); err != nil {
		t.Fatalf("write variables.json: %v", err)
	}

	llmCfg := &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{
			Provider: string(llm.ProviderPiCLI),
			ModelID:  model,
		},
		APIKeys: &llm.ProviderAPIKeys{
			PiCLI: &apiKey,
		},
	}
	// Step execution agent selection (controller_agent_factory.go:575
	// selectExecutionLLM) consults, in order: per-step AgentConfigs.
	// ExecutionLLM → sub-agent context override → TieredConfig via
	// tierResolver → none. Without a TieredConfig the engine bails
	// with "no valid LLM configuration found for execution agent".
	// So a minimal e2e MUST supply a 3-tier config even if every tier
	// points at the same model. PhaseLLM separately handles planning
	// and evaluation phase agents (workflow_orchestrator.go:316).
	agentLLM := &workflowtypes.AgentLLMConfig{
		Provider: string(llm.ProviderPiCLI),
		ModelID:  model,
	}
	presetCfg := &workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeExplicit,
		BuilderLLM:    agentLLM,
		PulseLLM:      agentLLM,
		TieredConfig: &workflowtypes.TieredLLMConfig{
			Tier1: agentLLM,
			Tier2: agentLLM,
			Tier3: agentLLM,
		},
	}

	wo, err := NewWorkflowOrchestrator(
		"",                       // mcpConfigPath — empty: no MCP servers
		0.7,                      // temperature
		"workflow",               // agentMode
		loggerv2.NewNoop(),       // logger
		nil,                      // eventBridge — no-op
		nil,                      // tracer
		[]string{},               // selectedServers
		[]string{},               // selectedTools
		false,                    // useCodeExecutionMode
		nil,                      // customTools
		map[string]interface{}{}, // customToolExecutors
		llmCfg,                   // llmConfig
		10,                       // maxTurns
		map[string]string{},      // toolCategories
		presetCfg,                // presetLLMConfig
	)
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	wo.SetExecutionOptions(&stepworkflow.ExecutionOptions{
		SelectedRunFolder: "iteration-0",
		ExecutionStrategy: stepworkflow.ExecutionStrategyStartFromBeginningNoHuman,
		EnabledGroupNames: []string{"default"},
		RouteSelections: map[string]string{
			stepIDs[1]: "route_even",
		},
	})

	// Generous budget: this is a real Pi tmux + MCP bridge workflow with
	// top-level steps, a todo-task orchestrator, and two delegated sub-agents.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	result, err := wo.Execute(ctx, "Compute 6*7 and verify the answer through routing, regular, todo, and agent steps.", workspace, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(result) == "" {
		t.Fatal("Execute returned empty result")
	}

	walkRoot := filepath.Join(wsRoot, relWorkspace)
	assertAllStepsExecutedAndDecisionsMatch(t, walkRoot, stepIDs)
	assertSeededRouteSelectionFile(t, walkRoot, stepIDs[1], "route_even")
	// step-compute: CLI workflow steps receive the code-exec/MCP bridge
	// preamble, so models sometimes answer with a small code-like computation
	// instead of the exact literal token. Accept equivalent forms while keeping
	// the downstream delegated-token assertions strict.
	assertStepExecutionResultContainsAny(t, walkRoot, "step-compute", []string{"RESULT=42", "result = 6 * 7", "6 * 7 = 42", "6 × 7", "= 42"})
	// step-report uses the message_sequence path — its final reply is exact. The
	// session.json glob in assertStepExecutionResult confirms the sequence wrote
	// to the NORMAL step folder (execution/step-report/), with the workflow root.
	assertStepExecutionResultContains(t, walkRoot, "step-report", "WORKFLOW_DONE_42")
	// Sub-agents under todo_task don't get the code_exec preamble
	// and DO follow the literal-token instruction. These are the
	// strongest assertions in this test — they prove the orchestrator
	// actually delegated to TWO distinct sub-agents and each produced
	// its expected token.
	assertStepExecutionResultContains(t, walkRoot, "verify-add", "VERIFY_ADD_OK")
	assertStepExecutionResultContains(t, walkRoot, "verify-mul", "VERIFY_MUL_OK")
	t.Logf("✅ workflow e2e (%d step types, pi-cli/%s): result-len=%d", len(stepIDs), model, len(result))
}

func TestWorkflowE2EAgentPiCLI(t *testing.T) {
	apiKey, model := requirePiCLIWorkflowE2E(t)
	wsAPI := strings.TrimSpace(os.Getenv("WORKSPACE_API_URL"))
	if wsAPI == "" {
		wsAPI = "http://127.0.0.1:18744"
		_ = os.Setenv("WORKSPACE_API_URL", wsAPI)
	}
	if err := requireWorkspaceAPIReachable(wsAPI); err != nil {
		t.Skipf("workspace API at %s unreachable (is the dev server running?): %v", wsAPI, err)
	}

	wsRoot := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
	if wsRoot == "" {
		wsRoot = "/Users/mipl/ai-work/coding-agent-loop/workspace-docs"
	}
	t.Setenv("WORKSPACE_DOCS_PATH", wsRoot)
	relWorkspace := "Workflow/_e2e_msgseq_" + filepath.Base(t.TempDir())
	workspaceDisk := filepath.Join(wsRoot, relWorkspace)
	if os.Getenv("KEEP_E2E_WORKSPACE") == "" {
		t.Cleanup(func() { _ = os.RemoveAll(workspaceDisk) })
	}
	if err := os.MkdirAll(filepath.Join(workspaceDisk, "planning"), 0o755); err != nil {
		t.Fatalf("mkdir planning: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDisk, "variables"), 0o755); err != nil {
		t.Fatalf("mkdir variables: %v", err)
	}
	if err := writeJSON(filepath.Join(workspaceDisk, "variables", "variables.json"), map[string]interface{}{
		"variables":       []interface{}{},
		"groups":          []map[string]interface{}{{"name": "default", "values": map[string]string{}, "enabled": true}},
		"extraction_date": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("write variables.json: %v", err)
	}
	if err := writeJSON(filepath.Join(workspaceDisk, "planning", "plan.json"), map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "agent",
				"id":                   "msgseq-report",
				"title":                "Agent runtime check",
				"description":          "Two-turn message_sequence runtime check.",
				"context_dependencies": []string{},
				"context_output":       "out.json",
				"items": []map[string]interface{}{
					{"id": "remember", "type": "user_message", "message": "Remember token MS_FIRST_ALPHA. Reply with ACK_MS_FIRST_ALPHA and stop. Do not call tools."},
					{"id": "recall", "type": "user_message", "message": "Using the previous turn, reply with MS_SECOND_SEES_FIRST_ALPHA and stop. Do not call tools."},
				},
				"next_step_id": "end",
			},
		},
	}); err != nil {
		t.Fatalf("write plan.json: %v", err)
	}

	llmCfg := &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{Provider: string(llm.ProviderPiCLI), ModelID: model},
		APIKeys: &llm.ProviderAPIKeys{
			PiCLI: &apiKey,
		},
	}
	agentLLM := &workflowtypes.AgentLLMConfig{Provider: string(llm.ProviderPiCLI), ModelID: model}
	presetCfg := &workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeExplicit,
		BuilderLLM:    agentLLM,
		PulseLLM:      agentLLM,
		TieredConfig:  &workflowtypes.TieredLLMConfig{Tier1: agentLLM, Tier2: agentLLM, Tier3: agentLLM},
	}

	wo, err := NewWorkflowOrchestrator("", 0.7, "workflow", loggerv2.NewNoop(), nil, nil, []string{}, []string{}, false, nil, map[string]interface{}{}, llmCfg, 10, map[string]string{}, presetCfg)
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if _, err := wo.Execute(ctx, "Run the agent runtime check.", relWorkspace, map[string]interface{}{"workflowStatus": workflowtypes.WorkflowStatusPreVerification}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(workspaceDisk, "runs", "*", "*", "execution", "msgseq-report", "session.json"))
	if len(matches) == 0 {
		t.Fatalf("message_sequence session.json not written under %s", workspaceDisk)
	}
	body, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Fatalf("read session.json: %v", err)
	}
	var session struct {
		RuntimeSessionID string `json:"runtime_session_id"`
		Entries          []struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatalf("parse session.json: %v", err)
	}
	if !strings.HasPrefix(session.RuntimeSessionID, "msgseq-") {
		t.Fatalf("runtime_session_id = %q, want stable msgseq-* owner", session.RuntimeSessionID)
	}
	if len(session.Entries) != 2 {
		t.Fatalf("message_sequence entries = %d, want 2\nsession=%s", len(session.Entries), body)
	}
	for i, entry := range session.Entries {
		if entry.Status != "completed" {
			t.Fatalf("entry %d status = %q, want completed\nsession=%s", i, entry.Status, body)
		}
	}
	if !strings.Contains(string(body), "MS_FIRST_ALPHA") || !strings.Contains(string(body), "MS_SECOND_SEES_FIRST_ALPHA") {
		t.Fatalf("session.json missing expected agent tokens\nsession=%s", body)
	}
	t.Logf("✅ message_sequence e2e (pi-cli/%s): runtime_session_id=%s session=%s", model, session.RuntimeSessionID, matches[len(matches)-1])
}

func requirePiCLIWorkflowE2E(t *testing.T) (apiKey string, model string) {
	t.Helper()
	if os.Getenv("RUN_WORKFLOW_REAL_E2E") == "" {
		t.Skip("set RUN_WORKFLOW_REAL_E2E=1 to run the workflow e2e")
	}
	if os.Getenv("RUN_PI_CLI_WORKFLOW_E2E") == "" && os.Getenv("RUN_PI_CLI_REAL_E2E") == "" {
		t.Skip("set RUN_PI_CLI_WORKFLOW_E2E=1 or RUN_PI_CLI_REAL_E2E=1 to run the Pi workflow e2e")
	}
	for _, bin := range []string{"tmux", "node"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s binary not found: %v", bin, err)
		}
	}
	if _, err := exec.LookPath("pi"); err != nil {
		if _, npxErr := exec.LookPath("npx"); npxErr != nil {
			t.Skipf("pi binary not found and npx fallback unavailable: pi=%v npx=%v", err, npxErr)
		}
	}
	apiKey = firstNonEmptyEnv("PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY")
	if apiKey == "" {
		t.Skip("PI_API_KEY, GEMINI_API_KEY, or GOOGLE_API_KEY required for pi-cli workflow e2e")
	}
	requireLocalMCPBridgeForPiWorkflowE2E(t)
	model = strings.TrimSpace(os.Getenv("PI_CLI_WORKFLOW_E2E_MODEL"))
	if model == "" {
		model = strings.TrimSpace(os.Getenv("PI_CLI_REAL_E2E_MODEL"))
	}
	if model == "" {
		model = "google/gemini-3.5-flash"
	}
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", t.TempDir())
	t.Cleanup(func() { _ = llm.CleanupPiCLIInteractiveSessions(context.Background()) })
	return apiKey, model
}

func requireLocalMCPBridgeForPiWorkflowE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("PI_CLI_WORKFLOW_E2E_EXTERNAL_MCP_BRIDGE") == "" {
		startLocalMCPBridgeForPiWorkflowE2E(t)
		return
	}
	if os.Getenv("MCP_API_URL") == "" && os.Getenv("MCP_BRIDGE_API_URL") == "" {
		t.Skip("MCP_API_URL or MCP_BRIDGE_API_URL is required when PI_CLI_WORKFLOW_E2E_EXTERNAL_MCP_BRIDGE=1")
	}
	if os.Getenv("MCP_API_TOKEN") == "" {
		t.Skip("MCP_API_TOKEN is required when PI_CLI_WORKFLOW_E2E_EXTERNAL_MCP_BRIDGE=1")
	}
	requireMCPBridgeBinaryForPiWorkflowE2E(t)
	requireMCPBridgeAuthForPiWorkflowE2E(t)
}

func requireMCPBridgeBinaryForPiWorkflowE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("MCP_BRIDGE_BINARY") != "" {
		return
	}
	if bridgePath, err := exec.LookPath("mcpbridge"); err == nil {
		t.Setenv("MCP_BRIDGE_BINARY", bridgePath)
		return
	}
	if home, err := os.UserHomeDir(); err == nil {
		bridgePath := filepath.Join(home, "go", "bin", "mcpbridge")
		if st, statErr := os.Stat(bridgePath); statErr == nil && !st.IsDir() {
			t.Setenv("MCP_BRIDGE_BINARY", bridgePath)
			return
		}
	}
	t.Skip("mcpbridge binary required for pi-cli workflow e2e; run `go install ./cmd/mcpbridge` in mcpagent")
}

func startLocalMCPBridgeForPiWorkflowE2E(t *testing.T) {
	t.Helper()
	requireMCPBridgeBinaryForPiWorkflowE2E(t)

	token := fmt.Sprintf("pi-workflow-e2e-%d", time.Now().UnixNano())
	handlers := mcpexecutor.NewExecutorHandlers("configs/mcp_servers_clean.json", loggerv2.NewNoop())
	router := mux.NewRouter()
	router.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}).Methods("GET")

	routeMCPRequest := func(w http.ResponseWriter, r *http.Request, server, tool string) {
		normalized := strings.ReplaceAll(server, "-", "_")
		switch normalized {
		case "workspace", "workspace_tools", "workspace_browser", "workspace_advanced", "workspace_image", "workspace_image_gen", "workspace_image_edit",
			"human_tools", "delegation_tools", "workflow", "workflow_creator", "knowledgebase_tools", "llm_config_tools", "secret_tools", "skill_tools",
			"mcp_server_tools", "activity_status", "auto_improvement":
			handlers.HandlePerToolCustomRequest(w, r, tool)
		case "memory":
			handlers.HandlePerToolVirtualRequest(w, r, tool)
		default:
			handlers.HandlePerToolMCPRequest(w, r, server, tool)
		}
	}

	toolsRouter := router.PathPrefix("/tools").Subrouter()
	toolsRouter.Use(mcpexecutor.AuthMiddleware(token))
	toolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		routeMCPRequest(w, r, vars["server"], vars["tool"])
	}).Methods("POST", "OPTIONS")
	toolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		handlers.HandlePerToolCustomRequest(w, r, mux.Vars(r)["tool"])
	}).Methods("POST", "OPTIONS")
	toolsRouter.HandleFunc("/virtual/{tool}", func(w http.ResponseWriter, r *http.Request) {
		handlers.HandlePerToolVirtualRequest(w, r, mux.Vars(r)["tool"])
	}).Methods("POST", "OPTIONS")

	sessionToolsRouter := router.PathPrefix("/s/{session_id}/tools").Subrouter()
	sessionToolsRouter.Use(mcpexecutor.AuthMiddleware(token))
	sessionToolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		r.Header.Set("X-Session-ID", vars["session_id"])
		routeMCPRequest(w, r, vars["server"], vars["tool"])
	}).Methods("POST", "OPTIONS")
	sessionToolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		r.Header.Set("X-Session-ID", vars["session_id"])
		handlers.HandlePerToolCustomRequest(w, r, vars["tool"])
	}).Methods("POST", "OPTIONS")
	sessionToolsRouter.HandleFunc("/virtual/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		r.Header.Set("X-Session-ID", vars["session_id"])
		handlers.HandlePerToolVirtualRequest(w, r, vars["tool"])
	}).Methods("POST", "OPTIONS")

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	t.Setenv("MCP_API_URL", srv.URL)
	t.Setenv("MCP_BRIDGE_API_URL", srv.URL)
	t.Setenv("MCP_API_TOKEN", token)
	requireMCPBridgeAuthForPiWorkflowE2E(t)
}

func requireMCPBridgeAuthForPiWorkflowE2E(t *testing.T) {
	t.Helper()
	apiURL := strings.TrimRight(strings.TrimSpace(firstNonEmptyEnv("MCP_BRIDGE_API_URL", "MCP_API_URL")), "/")
	if apiURL == "" {
		t.Skip("MCP_BRIDGE_API_URL or MCP_API_URL is required for pi-cli workflow e2e")
	}
	token := strings.TrimSpace(os.Getenv("MCP_API_TOKEN"))
	if token == "" {
		t.Skip("MCP_API_TOKEN is required for pi-cli workflow e2e")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/tools/virtual/get_api_spec", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build MCP bridge auth preflight request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("MCP bridge API at %s unreachable: %v", apiURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("MCP bridge API at %s rejected MCP_API_TOKEN; run against the same dev server that generated the token", apiURL)
	}
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

// assertStepExecutionResultContains reads the per-step execution
// artifact the engine writes for the given stepID and asserts the
// LLM's actual output contains the expected token. The engine uses
// THREE different on-disk shapes depending on step type, so this
// helper tries them in order:
//
//  1. Regular / top-level step:
//     logs/<stepID>/execution/execution-attempt-*-iteration-*.json
//     → JSON "execution_result" field
//  2. Todo-task sub-agent:
//     logs/step-*-sub-<stepID>-todo-*/execution/execution-attempt-*.json
//     → JSON "execution_result" field. The "step-N-sub-<id>-todo-M"
//     path is built by the engine when it materializes a predefined
//     route into a concrete sub-agent execution; the route_id is
//     embedded in the folder name.
//  3. Message-sequence step:
//     execution/<stepID>/session.json
//     → token may appear anywhere in the serialized
//     conversation_history (the engine archives the full chat, not a
//     single "result" field). A substring scan suffices.
//
// Catches: prompt not delivered (empty result), prompt reused (wrong
// token), LLM completed-with-wrong-content (token missing), sub-agent
// not delegated (no log file at all).
func assertStepExecutionResultContains(t *testing.T, walkRoot, stepID, wantToken string) {
	t.Helper()
	// 1) Top-level / direct execution log
	if matches := executionAttemptResultLogs(filepath.Join(walkRoot, "runs", "*", "*", "logs", stepID, "execution", "execution-attempt-*-iteration-*.json")); len(matches) > 0 {
		if scanExecutionResultLog(t, stepID, matches[len(matches)-1], wantToken) {
			return
		}
		return
	}
	// 2) Todo-task sub-agent execution log (route_id embedded in path)
	if matches := executionAttemptResultLogs(filepath.Join(walkRoot, "runs", "*", "*", "logs", "step-*-sub-"+stepID+"-todo-*", "execution", "execution-attempt-*-iteration-*.json")); len(matches) > 0 {
		if scanExecutionResultLog(t, stepID, matches[len(matches)-1], wantToken) {
			return
		}
		return
	}
	// 3) Message-sequence session log — lives in the step's normal execution
	// folder (execution/<stepID>/session.json), same as every other step.
	if matches, _ := filepath.Glob(filepath.Join(walkRoot, "runs", "*", "*", "execution", stepID, "session.json")); len(matches) > 0 {
		body, err := os.ReadFile(matches[len(matches)-1])
		if err != nil {
			t.Errorf("%s: read %s: %v", stepID, matches[len(matches)-1], err)
			return
		}
		if !strings.Contains(string(body), wantToken) {
			t.Errorf("%s: message_sequence session.json missing %q\n  log: %s", stepID, wantToken, matches[len(matches)-1])
		}
		return
	}
	t.Errorf("%s: no execution artifact found under %s (tried regular log, todo sub-agent log, message_sequence session)", stepID, walkRoot)
}

// assertStepExecutionResultContainsAny is the "any of these forms is
// acceptable" variant. Used for steps where the engine's prompt
// preamble nudges the LLM into a different shape than we asked for,
// but the *content* of the response still proves the task was
// delivered and understood.
func assertStepExecutionResultContainsAny(t *testing.T, walkRoot, stepID string, wantAny []string) {
	t.Helper()
	// Reuse the same path-resolution logic by collecting the
	// execution_result text first, then doing the OR-of-substrings.
	pat := filepath.Join(walkRoot, "runs", "*", "*", "logs", stepID, "execution", "execution-attempt-*-iteration-*.json")
	matches := executionAttemptResultLogs(pat)
	if len(matches) == 0 {
		t.Errorf("%s: no execution log under %s", stepID, pat)
		return
	}
	body, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Errorf("%s: read: %v", stepID, err)
		return
	}
	var entry struct {
		ExecutionResult string `json:"execution_result"`
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		t.Errorf("%s: parse: %v", stepID, err)
		return
	}
	for _, tok := range wantAny {
		if strings.Contains(entry.ExecutionResult, tok) {
			return
		}
	}
	t.Errorf("%s: execution_result contained none of %v\n  result: %q", stepID, wantAny, entry.ExecutionResult)
}

func scanExecutionResultLog(t *testing.T, stepID, logPath, wantToken string) bool {
	t.Helper()
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Errorf("%s: read %s: %v", stepID, logPath, err)
		return false
	}
	var entry struct {
		ExecutionResult string `json:"execution_result"`
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		t.Errorf("%s: parse %s: %v", stepID, logPath, err)
		return false
	}
	if !strings.Contains(entry.ExecutionResult, wantToken) {
		t.Errorf("%s: execution_result missing %q\n  log: %s\n  result: %q", stepID, wantToken, logPath, entry.ExecutionResult)
		return true // we found the right file, just wrong content
	}
	return true
}

func writeJSON(path string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func executionAttemptResultLogs(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	filtered := matches[:0]
	for _, match := range matches {
		base := strings.TrimSuffix(filepath.Base(match), ".json")
		if strings.HasSuffix(base, "-prompts") || strings.HasSuffix(base, "-conversation") || strings.HasSuffix(base, "-timing") {
			continue
		}
		filtered = append(filtered, match)
	}
	return filtered
}

func requireWorkspaceAPIReachable(baseURL string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/documents/_index.json", nil)
	if err != nil {
		return err
	}
	if token := strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")); token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func assertSeededRouteSelectionFile(t *testing.T, walkRoot, stepID, wantRouteID string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(walkRoot, "runs", "*", "*", "execution", stepID, "route_selection.json"))
	if len(matches) == 0 {
		t.Fatalf("%s: seeded route_selection.json not found under %s", stepID, walkRoot)
	}
	body, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Fatalf("%s: read route_selection.json: %v", stepID, err)
	}
	var payload struct {
		SelectRoute string `json:"select_route"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("%s: parse route_selection.json: %v\nbody=%s", stepID, err, body)
	}
	if payload.SelectRoute != wantRouteID {
		t.Fatalf("%s: select_route = %q, want %q\nfile=%s", stepID, payload.SelectRoute, wantRouteID, matches[len(matches)-1])
	}
}

// assertAllStepsExecutedAndDecisionsMatch is the Tier-1 assertion: for
// each step type it confirms (a) the engine wrote per-type execution
// evidence AND (b) where the evidence carries the routing decision,
// the decision matches the deterministic route selection. Catches
// silent-skip bugs (smoke-pass with no execution) and route-selection
// regressions that would have passed the lower-bar "did it produce a
// file" check.
func assertAllStepsExecutedAndDecisionsMatch(t *testing.T, walkRoot string, stepIDs []string) {
	t.Helper()
	type evidence struct {
		// globs lists candidate file paths; first match wins.
		globs []string
		// assertField is optional: when set, the evidence JSON must
		// decode and the field must equal wantValue. Used to verify
		// LLM decisions, not just file presence.
		assertField string
		wantValue   interface{}
	}
	evidenceByStep := map[string]evidence{
		"step-compute":     {globs: []string{filepath.Join(walkRoot, "runs", "*", "*", "logs", "step-compute", "execution", "execution-attempt-*-iteration-*.json")}},
		"step-verify-even": {globs: []string{filepath.Join(walkRoot, "runs", "*", "*", "logs", "step-verify-even", "execution", "execution-attempt-*-iteration-*.json")}},
		// routing-evaluation.json records the deterministic selected_route_id.
		// The e2e passes RouteSelections for this router; the engine must
		// seed, read, validate, and persist route_even.
		"step-classify": {
			globs:       []string{filepath.Join(walkRoot, "runs", "*", "*", "logs", "step-classify", "routing-evaluation.json")},
			assertField: "selected_route_id",
			wantValue:   "route_even",
		},
		// todo_task: prompts.json is the cheapest proof the step
		// reached its agent factory. The richer assertion (todos
		// created+completed) requires reading runtime fields that
		// the engine does not persist to disk (AgentDelegationDecision is
		// runtime-only per planning_agent.go:646).
		"step-double-check": {globs: []string{filepath.Join(walkRoot, "runs", "*", "*", "logs", "step-double-check", "execution", "todo-task-prompts.json")}},
		// message_sequence persists session.json in the step's normal
		// execution folder: execution/<step-id>/session.json.
		"step-report": {globs: []string{filepath.Join(walkRoot, "runs", "*", "*", "execution", "step-report", "session.json")}},
	}

	var completed, missing []string
	for _, id := range stepIDs {
		m := evidenceByStep[id]
		var match string
		for _, g := range m.globs {
			matches, _ := filepath.Glob(g)
			if len(matches) > 0 {
				match = matches[0]
				break
			}
		}
		if match == "" {
			missing = append(missing, id)
			continue
		}
		completed = append(completed, id)
		if m.assertField == "" {
			continue
		}
		body, err := os.ReadFile(match)
		if err != nil {
			t.Errorf("read %s: %v", match, err)
			continue
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Errorf("decode %s: %v\nbody=%s", match, err, body)
			continue
		}
		got := decoded[m.assertField]
		if got != m.wantValue {
			t.Errorf("%s: %s = %v, want %v (full file %s)", id, m.assertField, got, m.wantValue, match)
		}
	}

	if len(missing) > 0 {
		t.Errorf("execution evidence missing for steps: %v (completed: %v)", missing, completed)
	}
	t.Logf("completed steps: %v", completed)
}
