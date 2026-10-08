package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GoalWorkPermissions mirror workflow.json pulse.autonomy. Each is true for
// "auto" (Goal Work does it itself) and false for "ask" (it prepares the work
// and creates a decision). Run defaults to auto; Outward and Change to ask.
type GoalWorkPermissions struct {
	// Level is the autonomy ladder step, 0-5 (AutonomyLadder); the booleans
	// below are what the tools enforce for it.
	Level int
	// Run: run existing steps and routes, including what they normally do.
	Run bool
	// Outward: post, send or contact anyone beyond what existing steps do.
	Outward bool
	// Change: edit existing steps (prompts, items, settings). soul.md goals
	// and constraints always go to the user.
	Change bool
	// Reshape: change schedules and the plan's structure (add steps, routes,
	// groups, workflow settings). Never deletes.
	Reshape bool
}

// AutonomyStep is one step of the autonomy ladder: each adds one kind of
// action Pulse may get done without asking the owner (owner, 2026-10-08).
type AutonomyStep struct {
	Name string
	Adds string
}

// AutonomyLadder is pulse.autonomy.level 0-5.
var AutonomyLadder = []AutonomyStep{
	{"Advise only", "Pulse recommends; nothing happens without the owner."},
	{"Measure", "run steps to measure the goal and recover missed runs, and set up goal metrics."},
	{"Fix", "fix broken steps (bugs, wrong inputs), reversible."},
	{"Tune", "improve prompts and step settings."},
	{"Publish", "publish and post through the workflow's own steps and accounts."},
	{"Reshape", "change schedules and the plan's structure (never deletes)."},
}

// MaxAutonomyLevel is the top of the ladder.
const MaxAutonomyLevel = 5

// PermissionsForLevel is what the tools allow at a ladder step. Fix and Tune
// use the same edit tools (the text separates them); Publish turns on the
// outward tools; Reshape the schedule and structure tools.
func PermissionsForLevel(level int) GoalWorkPermissions {
	if level < 0 {
		level = 0
	}
	if level > MaxAutonomyLevel {
		level = MaxAutonomyLevel
	}
	return GoalWorkPermissions{Level: level, Run: level >= 1, Change: level >= 2, Outward: level >= 4, Reshape: level >= 5}
}

// LegacyAutonomyLevel maps the old run/outward/change switches to the ladder,
// never granting more than they did: no run is Advise only; run alone is
// Measure; run and change is Tune; all three is Reshape.
func LegacyAutonomyLevel(run, outward, change bool) int {
	switch {
	case !run:
		return 0
	case !change:
		return 1
	case !outward:
		return 3
	}
	return 5
}

// AutonomyLadderText lists the ladder with the current step marked, for the
// Pulse and Builder prompts.
func AutonomyLadderText(level int) string {
	perms := PermissionsForLevel(level)
	var b strings.Builder
	fmt.Fprintf(&b, "Autonomy: level %d of %d, %s (workflow.json pulse.autonomy.level). Each level adds one kind of action done without asking the owner:\n", perms.Level, MaxAutonomyLevel, AutonomyLadder[perms.Level].Name)
	for i, step := range AutonomyLadder {
		mark := "  "
		if i <= perms.Level {
			mark = "✓ "
		}
		fmt.Fprintf(&b, "%s%d %s: %s\n", mark, i, step.Name, step.Adds)
	}
	b.WriteString("Above the current level, prepare the work and have it put to the owner as one decision. Always the owner's, at every level: spending money, deleting steps or schedules, replacing the plan, editing soul.md, new kinds of outreach, and schedules the owner paused.")
	return b.String()
}

// PulseAutonomyPermissions reads workflow.json pulse.autonomy. Run is off only
// for an explicit "ask"; Outward and Change are on only for an explicit
// "auto". Missing or unreadable settings mean those defaults.
func PulseAutonomyPermissions(manifestJSON string) GoalWorkPermissions {
	perms := PermissionsForLevel(1)
	var manifest struct {
		Pulse *struct {
			Autonomy *struct {
				Level   *int   `json:"level"`
				Run     string `json:"run"`
				Outward string `json:"outward"`
				Change  string `json:"change"`
			} `json:"autonomy"`
		} `json:"pulse"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil || manifest.Pulse == nil || manifest.Pulse.Autonomy == nil {
		return perms
	}
	a := manifest.Pulse.Autonomy
	if a.Level != nil {
		return PermissionsForLevel(*a.Level)
	}
	level := func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
	return PermissionsForLevel(LegacyAutonomyLevel(level(a.Run) != "ask", level(a.Outward) == "auto", level(a.Change) == "auto"))
}

// goalWorkPermissionInstructions tells Goal Work what it may do itself and what
// it must prepare for the user's approval, one sentence per permission. The
// server refuses the matching tools for an "ask" level during the Goal Work
// turn (cmd/server/pulse_autonomy_guard.go), so the text and the tools agree.
func goalWorkPermissionInstructions(perms GoalWorkPermissions) string {
	return AutonomyLadderText(perms.Level) + " The tools hold the level: running steps needs Measure, editing steps Fix, posting tools Publish, schedules and plan structure Reshape."
}

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

// GoalWorkEffectivePermissions are the levels Goal Work holds in this turn:
// the workflow's pulse.autonomy. Workflow Review no longer withholds Run or
// Change: it runs before every run (PLAT-697 phase 0), so a run Goal Work
// starts is reviewed first like any other.
func GoalWorkEffectivePermissions(manifestJSON string) GoalWorkPermissions {
	return PulseAutonomyPermissions(manifestJSON)
}

// GoalWorkAutonomyInstructions is the permission text for a Goal Work turn.
// The server enforces the same levels on the tools for that turn (PLAT-697
// phase 2), so a refused call is the level working, not an error to retry.
func GoalWorkAutonomyInstructions(perms GoalWorkPermissions) string {
	text := "Your permission levels for this turn (workflow.json pulse.autonomy). The tools hold them: a call refused for a level means prepare the work and create a decision request instead; do not retry it another way.\n"
	text += "A run you start is checked by the Workflow Review first, like every run: if the plan changed it is reviewed before it starts, and a run tool may answer that the review is running; wait for its result, then run again.\n"
	return text + goalWorkPermissionInstructions(perms)
}
