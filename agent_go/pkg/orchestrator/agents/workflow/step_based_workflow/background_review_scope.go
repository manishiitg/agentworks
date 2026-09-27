package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"strings"
)

type backgroundReviewScopeKey struct{}
type backgroundReviewScope struct {
	Module, RunID string
	// Permissions are the workflow's pulse.autonomy levels for Goal Work.
	Permissions goalWorkPermissions
}

// goalWorkPermissions mirror workflow.json pulse.autonomy. Each is true for
// "auto" (Goal Work does it itself) and false for "ask" (it prepares the work
// and creates a decision). Run defaults to auto; Outward and Change to ask.
type goalWorkPermissions struct {
	// Run: run existing steps and routes, including what they normally do.
	Run bool
	// Outward: post, send or contact anyone beyond what existing steps do.
	Outward bool
	// Change: edit the plan, step settings and schedules. soul.md goals and
	// constraints always go to the user.
	Change bool
}

// researchOnly is Architecture: research and propose, never act.
func (s backgroundReviewScope) researchOnly() bool {
	return s.Module == "architecture_review"
}

// goalWork is the strategic_review module in its Goal Work role: it does
// goal-advancing work for the user instead of only proposing it. It prepares
// work in pulse/work/ and acts as far as its permissions allow.
func (s backgroundReviewScope) goalWork() bool {
	return s.Module == "strategic_review"
}

// goalWorkRunTools are the workflow execution tools Goal Work may use when the
// workflow's Run permission is auto.
var goalWorkRunTools = map[string]bool{
	"execute_step": true, "query_step": true, "send_step_message": true, "stop_step": true,
	"run_full_workflow": true, "list_executions": true,
}

// goalWorkChangeTools are the typed Builder edit tools Goal Work may use when
// the workflow's Change permission is auto. They record plan changelog
// entries, so Plan Drift reviews the dependents afterwards. Deleting steps or
// schedules, migrations and contract tools stay withheld.
var goalWorkChangeTools = map[string]bool{
	"validate_plan_change": true, "get_plan_prompt_health": true,
	"add_scripted_step": true, "add_message_sequence_step": true, "add_routing_step": true, "add_branch_step": true,
	"update_scripted_step": true, "update_message_sequence_step": true, "update_routing_step": true, "update_branch_step": true,
	"update_orchestrator_step": true, "update_orchestrator_route": true, "update_validation_schema": true,
	"update_step_config": true, "update_variable": true,
	"list_schedules": true, "create_schedule": true, "create_calendar_schedule": true, "update_schedule": true,
}

func goalWorkToolAllowed(name string, perms goalWorkPermissions) bool {
	return researchReviewToolAllowed(name) || name == "record_pulse_goal_work" ||
		// Other workflows and Crews are read-only context; having a Crew do
		// work is running work, so it follows the Run permission.
		name == "search_platform" || (perms.Run && name == "ask_platform_crew") ||
		(perms.Run && goalWorkRunTools[name]) || (perms.Change && goalWorkChangeTools[name])
}

func filterGoalWorkTools(tools []llmtypes.Tool, executors map[string]interface{}, perms goalWorkPermissions) ([]llmtypes.Tool, map[string]interface{}) {
	result := []llmtypes.Tool{}
	handlers := map[string]interface{}{}
	for _, tool := range tools {
		if tool.Function != nil && goalWorkToolAllowed(tool.Function.Name, perms) {
			result = append(result, tool)
			if handler, ok := executors[tool.Function.Name]; ok {
				handlers[tool.Function.Name] = handler
			}
		}
	}
	return result, handlers
}

// pulseAutonomyPermissions reads workflow.json pulse.autonomy. Run is off only
// for an explicit "ask"; Outward and Change are on only for an explicit
// "auto". Missing or unreadable settings mean those defaults.
func pulseAutonomyPermissions(manifestJSON string) goalWorkPermissions {
	perms := goalWorkPermissions{Run: true}
	var manifest struct {
		Pulse *struct {
			Autonomy *struct {
				Run     string `json:"run"`
				Outward string `json:"outward"`
				Change  string `json:"change"`
			} `json:"autonomy"`
		} `json:"pulse"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil || manifest.Pulse == nil || manifest.Pulse.Autonomy == nil {
		return perms
	}
	level := func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
	a := manifest.Pulse.Autonomy
	perms.Run = level(a.Run) != "ask"
	perms.Outward = level(a.Outward) == "auto"
	perms.Change = level(a.Change) == "auto"
	return perms
}

// goalWorkPermissionInstructions tells Goal Work what it may do itself and what
// it must prepare for the user's approval, one sentence per permission.
func goalWorkPermissionInstructions(perms goalWorkPermissions) string {
	parts := []string{}
	if perms.Run {
		parts = append(parts, "Run permission: auto. Run existing workflow steps or routes yourself (execute_step, run_full_workflow) when that directly advances the goal or recovers work that did not happen; they do what they normally do, including their usual posts.")
	} else {
		parts = append(parts, "Run permission: ask. Do not run workflow steps; prepare the work fully and create a decision asking the user to run it.")
	}
	if perms.Outward {
		parts = append(parts, "Outward permission: auto. You may post, send or contact people yourself with the workflow's own accounts and tools when it advances the goal, within soul.md limits and the workflow's own caps and dedupe records; verify each action landed and record it where the workflow records its own.")
	} else {
		parts = append(parts, "Outward permission: ask. Beyond what existing steps normally do, never post, send or contact anyone yourself: prepare it fully and create a decision for the user to approve.")
	}
	if perms.Change {
		parts = append(parts, "Change permission: auto. You may change how the workflow works yourself with the typed Builder tools (step prompts and items, step settings, schedules) when it advances the goal and every soul.md constraint still holds. soul.md goals and constraints are not yours to edit: challenge them through a decision. Never delete steps or schedules; propose that through a decision.")
	} else {
		parts = append(parts, "Change permission: ask. Never edit the plan, steps, schedules or soul.md; propose them with a ready patch through a decision.")
	}
	return strings.Join(parts, " ")
}

func validateBackgroundReviewScope(module, runID string) error {
	if module != "plan_drift_review" && module != "technical_review" && module != "architecture_review" && module != "strategic_review" {
		return fmt.Errorf("unknown review_module %q", module)
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(runID) != runID || runID == "." || runID == ".." || strings.ContainsAny(runID, "/\\\x00") {
		return fmt.Errorf("review_module requires a single safe pulse_run_id")
	}
	return nil
}

// This filters workflow-owned custom tools only. Connected MCP server tools
// retain their existing configured grants; research access never expands them.
func researchReviewToolAllowed(name string) bool {
	switch name {
	case "execute_shell_command", "read_workspace_file", "list_workspace_files", "search_workspace_files", "write_workspace_file", "update_workspace_file",
		"agent_browser", "web_search", "web_fetch", "google_workspace_cli", "read_skill", "get_api_spec", "get_prompt", "get_resource",
		"query_workflow_db", "query_workflow_costs", "get_step_prompts", "get_plan_prompt_health", "get_workflow_config", "get_llm_config", "get_cost_summary",
		"list_llm_capabilities", "list_published_llms", "list_provider_models", "list_executions", "get_sub_agent_conversation", "get_route_description",
		"get_goal_metrics", "get_pulse_state", "record_pulse_finding", "record_pulse_result", "merge_pulse_issues", "read_crew_calls",
		"get_human_input_request", "list_human_input_requests", "create_human_input_request", "resolve_run_concern":
		return true
	}
	return false
}
func filterResearchReviewTools(tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	result := []llmtypes.Tool{}
	handlers := map[string]interface{}{}
	for _, tool := range tools {
		if tool.Function != nil && researchReviewToolAllowed(tool.Function.Name) {
			result = append(result, tool)
			if handler, ok := executors[tool.Function.Name]; ok {
				handlers[tool.Function.Name] = handler
			}
		}
	}
	return result, handlers
}

// Generic read-only execution replaces specialized read-only reviewer launchers.
type backgroundReadOnlyKey struct{}

func parseBackgroundReadOnlyAccess(args map[string]interface{}, agentType string) (bool, error) {
	value, present := args["access_mode"]
	if !present {
		return false, nil
	}
	mode, ok := value.(string)
	if !ok || (mode != "read_write" && mode != "read_only") {
		return false, fmt.Errorf("access_mode must be read_write or read_only")
	}
	if mode == "read_only" && agentType != "executor" {
		return false, fmt.Errorf("read_only requires an executor")
	}
	return mode == "read_only", nil
}
func readOnlyBackgroundToolAllowed(name string) bool {
	switch name {
	case "execute_shell_command", "read_workspace_file", "list_workspace_files", "search_workspace_files", "read_skill", "get_api_spec", "get_prompt", "get_resource",
		"query_workflow_db", "query_workflow_costs", "get_step_prompts", "get_plan_prompt_health", "get_workflow_config", "get_llm_config", "get_cost_summary",
		"list_executions", "get_sub_agent_conversation", "get_route_description", "get_goal_metrics", "get_pulse_state":
		return true
	}
	return false
}
func filterReadOnlyBackgroundTools(tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	result := []llmtypes.Tool{}
	handlers := map[string]interface{}{}
	for _, tool := range tools {
		if tool.Function != nil && readOnlyBackgroundToolAllowed(tool.Function.Name) {
			result = append(result, tool)
			if handler, ok := executors[tool.Function.Name]; ok {
				handlers[tool.Function.Name] = handler
			}
		}
	}
	return result, handlers
}

// withoutBackgroundTool removes one tool from a background agent's bundle
// without touching the shared parent bundle it was copied from.
func withoutBackgroundTool(tools []llmtypes.Tool, executors map[string]interface{}, name string) ([]llmtypes.Tool, map[string]interface{}) {
	result := make([]llmtypes.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == name {
			continue
		}
		result = append(result, tool)
	}
	handlers := make(map[string]interface{}, len(executors))
	for key, handler := range executors {
		if key != name {
			handlers[key] = handler
		}
	}
	return result, handlers
}
