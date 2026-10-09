package server

import (
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func knownWorkshopRegisteredToolNamesOutsideWorkflowPool() map[string]string {
	registered := map[string]string{}
	add := func(source string, names ...string) {
		for _, name := range names {
			registered[todo_creation_human.ConsolidatedPlanToolName(name)] = source
		}
	}

	add("mcpagent virtual tools", "get_api_spec", "search_tools", "get_prompt", "get_resource")
	add("execution sub-agent/session tools", "call_sub_agent", "call_scripted_sub_agent", "call_generic_agent", "query_sub_agent", "stop_sub_agent", "get_sub_agent_conversation", "get_route_description")
	add("conditional workspace browser tools", "agent_browser")
	add("server secret management tools",
		"list_secrets", "set_workflow_secret", "delete_workflow_secret",
		"manage_user_access", "manage_workflow_webhook",
	)
	// Registered by registerLLMCapabilityDiscoveryTools in multiagent_llm_tools.go,
	// which is outside the workflow tool pool. Granted to workshop mode by
	// 385c95158 without a matching entry here, which left this invariant red.
	add("LLM capability discovery tools", "list_llm_capabilities")
	add("workshop plan tools",
		"create_plan", "validate_plan_change", "migrate_message_sequence_code_items", "migrate_orchestrator_step_type", "migrate_declared_execution_mode", "strip_declared_execution_mode",
		"add_orchestrator_step", "add_orchestrator_route", "update_orchestrator_step", "update_orchestrator_route", "delete_orchestrator_route",
		"add_scripted_step", "add_message_sequence_step", "add_routing_step", "add_branch_step",
		"add_human_input_step", "add_todo_task_step", "add_todo_task_route",
		"update_scripted_step", "update_message_sequence_step", "update_routing_step", "update_branch_step",
		"update_human_input_step", "update_todo_task_step", "update_todo_task_route",
		"delete_todo_task_route", "delete_plan_steps", "cleanup_orphan_step_configs",
		"update_validation_schema",
		"change_step_type",
		"record_plan_drift_review",
		"check_plan_no_loss", "restore_step_from_changelog",
	)
	add("workshop execution tools",
		"execute_step", "query_step", "send_step_message", "debug_step", "list_executions",
		"stop_step", "stop_all_executions",
		"run_full_workflow",
	)
	add("workshop review/maintenance tools",
		"update_step_config", "get_step_prompts", "get_plan_prompt_health",
		"mark_changelog_artifact_reviewed",
		"review_workflow_timing", "review_workflow_costs", "review_step_code",
		"get_cost_summary",
	)
	add("workshop workflow/config tools",
		"get_llm_config", "get_workflow_config", "update_workflow_config",
		"request_workflow_folder_access",
		"set_workflow_contract_version",
		"update_variable", "add_group", "update_group", "delete_group",
		"list_schedules", "create_schedule", "create_calendar_schedule",
		"update_schedule", "delete_schedule", "trigger_schedule", "get_schedule_runs",
		"get_contract_upgrades",
		"list_skills", "import_skill", "uninstall_skill", "search_skills", "install_skill",
		"list_published_llms", "list_provider_models", "test_llm", "set_workflow_llm_config",
	)
	add("workshop report tools", "validate_report_html")
	add("workshop guidance/status tools",
		"get_workflow_command_guidance", "get_reference_doc",
	)

	return registered
}

// TestToolSetInvariants guards the bug class that hid notify_user: a tool that is
// allow-listed for a mode but never registered (or vice versa), and accidental
// loss of plan/workflow tools from the workshop allow-list.
func TestToolSetInvariants(t *testing.T) {
	// 1. The workflow tool pool (workflowMode=true) must register every human tool.
	tools, _, cats := createCustomTools(true, "default", "invariant-test")
	pool := map[string]bool{}
	counts := map[string]int{}
	for _, tl := range tools {
		if tl.Function != nil {
			pool[tl.Function.Name] = true
			counts[tl.Function.Name]++
		}
	}
	for name, count := range counts {
		if count != 1 {
			t.Fatalf("workflow pool contains %d definitions for %q", count, name)
		}
	}
	browserTools := virtualtools.CreateWorkspaceBrowserTools()
	withBrowser := append(append([]llmtypes.Tool(nil), tools...), browserTools...)
	browserExecutors := virtualtools.CreateWorkspaceBrowserToolExecutors()
	executors := map[string]interface{}{}
	_, baseExecutors, _ := createCustomTools(true, "default", "filter-invariant-test")
	for name, executor := range baseExecutors {
		executors[name] = executor
	}
	for name, executor := range browserExecutors {
		executors[name] = executor
	}
	filtered, _ := orchestrator.FilterCustomToolsByCategory(withBrowser, executors, []string{
		"workspace_advanced:*", "human_tools:*", "workspace_browser:*",
		"workflow_db:query_workflow_db", "workflow_db:mutate_workflow_db",
	})
	filteredCounts := map[string]int{}
	for _, tool := range filtered {
		if tool.Function != nil {
			filteredCounts[tool.Function.Name]++
		}
	}
	for name, count := range filteredCounts {
		if count != 1 {
			t.Fatalf("filtered workflow pool contains %d definitions for %q", count, name)
		}
	}
	for _, n := range []string{"human_feedback", "notify_user", "get_human_input_request", "create_human_input_request", "answer_human_input_request", "mark_human_input_consumed"} {
		if !pool[n] || cats[n] != "human_tools" {
			t.Fatalf("workflow pool missing human tool %q (in_pool=%v cat=%q)", n, pool[n], cats[n])
		}
	}
	if pool["submit_human_answer"] {
		t.Fatal("workflow pool still exposes removed submit_human_answer tool")
	}

	// human-tool name set (registerable human tools = source of truth)
	humanNames := map[string]bool{}
	for n := range virtualtools.CreateHumanToolExecutors() {
		humanNames[n] = true
	}
	for _, n := range virtualtools.HumanToolImplementationNames() {
		if !pool[n] || cats[n] != "human_tools" {
			t.Fatalf("human implementation %q is missing from the registered human_tools pool", n)
		}
		if !agentworksproduct.ChatAllowsTool("builder", n) && !agentworksproduct.ChatAllowsTool("run", n) {
			t.Fatalf("human implementation %q is not admitted by AgentWorks product.yaml in any mode", n)
		}
	}
	knownOutsidePool := knownWorkshopRegisteredToolNamesOutsideWorkflowPool()

	for _, mode := range []string{"workshop", "run"} {
		allow := todo_creation_human.GetToolsForWorkshopMode(mode)
		allowSet := map[string]bool{}
		for _, n := range allow {
			allowSet[n] = true
		}

		// 2. INVARIANT: every allow-listed HUMAN tool must be registerable in the
		//    workflow pool. (This is exactly what broke for notify_user.)
		for _, n := range allow {
			if humanNames[n] && !pool[n] {
				t.Fatalf("mode=%s: human tool %q is allow-listed but NOT in the workflow pool (would be invisible via get_api_spec)", mode, n)
			}
		}

		// Broad invariant: if a mode allow-lists a tool, it must have a real
		// registration path. Most tools come from the workflow pool; workshop
		// custom/guidance/virtual tools are registered separately and enumerated
		// above. This guards the same bug class as notify_user and Pulse state
		// tools for future additions.
		for _, n := range allow {
			if pool[n] {
				continue
			}
			if _, ok := knownOutsidePool[n]; ok {
				continue
			}
			t.Fatalf("mode=%s: allow-listed tool %q is neither in workflow pool nor known workshop/virtual registrations", mode, n)
		}

		// 3. Human interaction/notification/report tools must be usable in both modes.
		productMode := "builder"
		if mode == "run" {
			productMode = "run"
		}
		for _, n := range agentworksproduct.ChatTools(productMode) {
			if cats[n] != "human_tools" {
				continue
			}
			if !pool[n] || cats[n] != "human_tools" {
				t.Fatalf("workflow pool missing workshop human tool %q (in_pool=%v cat=%q)", n, pool[n], cats[n])
			}
			if !allowSet[n] {
				t.Fatalf("mode=%s: expected %q in allow-list", mode, n)
			}
		}
		if mode == "workshop" && allowSet["human_feedback"] {
			t.Fatal("workshop mode must ask through normal Builder chat, not blocking human_feedback")
		}
		if mode == "run" && !allowSet["human_feedback"] {
			t.Fatal("run mode must retain blocking human_feedback for urgent execution-only input")
		}
		if mode == "run" && allowSet["answer_human_input_request"] {
			t.Fatal("run mode must not let a scheduled/runtime agent answer a user decision")
		}
	}

	for _, name := range []string{"get_goal_metrics", "configure_goal_metrics", "record_goal_observations"} {
		if !pool[name] || cats[name] != "workflow" {
			t.Fatalf("missing goal tool %s", name)
		}
	}
	// 4. Pulse worklist tools are registered in the workflow tool pool and must
	//    be visible in workshop mode, otherwise scheduled Pulse turns can ask
	//    for record_pulse_result/get_pulse_state and then fail at
	//    runtime with "not callable in this chat session".
	for _, n := range []string{"get_pulse_state", "record_pulse_worklist", "record_pulse_result", "resolve_run_concern"} {
		if !pool[n] || cats[n] != "workflow" {
			t.Fatalf("workflow pool missing Pulse state tool %q (in_pool=%v cat=%q)", n, pool[n], cats[n])
		}
	}

	// 5. The workshop allow-list must still expose the plan/workflow tools
	//    (guards against accidental loss during refactors).
	workshop := map[string]bool{}
	for _, n := range todo_creation_human.GetToolsForWorkshopMode("workshop") {
		workshop[n] = true
	}
	for _, n := range []string{
		"create_plan", "migrate_message_sequence_code_items", "migrate_orchestrator_step_type", "add_orchestrator_step", "add_orchestrator_route", "update_orchestrator_step", "update_orchestrator_route", "delete_orchestrator_route", "add_scripted_step", "add_routing_step", "add_human_input_step",
		"update_scripted_step", "delete_plan_steps",
		"execute_step", "get_human_input_request", "create_human_input_request", "answer_human_input_request",
		"update_workflow_config", "update_step_config", "validate_report_html",
		"list_schedules", "update_schedule", "get_schedule_runs",
		"execute_shell_command", "diff_patch_workspace_file",
		"get_pulse_state", "record_pulse_worklist", "record_pulse_result", "resolve_run_concern",
		"mark_changelog_artifact_reviewed",
	} {
		if !workshop[todo_creation_human.ConsolidatedPlanToolName(n)] {
			t.Fatalf("workshop allow-list missing expected tool %q", n)
		}
	}
	for _, removed := range []string{"improve_learnings", "improve_kb", "improve_db", "review_artifact_sync"} {
		if workshop[removed] {
			t.Fatalf("workshop allow-list still exposes removed dedicated maintenance tool %q", removed)
		}
	}
	for _, renamed := range []string{"add_regular_step", "update_regular_step"} {
		if workshop[renamed] {
			t.Fatalf("workshop allow-list still exposes ambiguous legacy tool name %q", renamed)
		}
	}

	run := map[string]bool{}
	for _, n := range todo_creation_human.GetToolsForWorkshopMode("run") {
		run[n] = true
	}
	for _, n := range []string{"get_pulse_state", "record_pulse_worklist", "record_pulse_result", "resolve_run_concern", "mark_changelog_artifact_reviewed"} {
		if run[n] {
			t.Fatalf("run allow-list must not expose Pulse mutation tool %q", n)
		}
	}
}

func TestPrimaryWorkflowBuilderChatExcludesBlockingHumanFeedback(t *testing.T) {
	if agentworksproduct.ChatAllowsTool("builder", "human_feedback") {
		t.Fatal("Workflow Builder must not expose human_feedback")
	}
	if !agentworksproduct.ChatAllowsTool("builder", "notify_user") {
		t.Fatal("Workflow Builder must retain non-blocking notify_user")
	}
	if !agentworksproduct.ChatAllowsTool("run", "human_feedback") {
		t.Fatal("non-Builder execution contexts must retain human_feedback")
	}
}
