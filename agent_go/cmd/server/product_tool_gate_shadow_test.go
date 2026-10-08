package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"slices"
	"testing"
)

// A Goals chat is measured against its product.yaml list before it enforces it (PLAT-608 step 4): the shadow never
// drops a tool, and it records the ones the list would drop.
func TestShadowGateAdmitsEverythingAndRecordsWhatTheListMisses(t *testing.T) {
	gate := newProductToolGate(nil)
	gate.Shadow("goals-builder", []string{"add_step"})
	if !gate.Admit("add_step") || !gate.Admit("read_workspace_file") {
		t.Fatal("a shadow gate must not filter")
	}
	if !slices.Equal(uniqueSortedToolNames(gate.wouldFilter), []string{"read_workspace_file"}) || gate.profileID != "goals-builder" {
		t.Fatalf("would-filter = %v, surface = %q", gate.wouldFilter, gate.profileID)
	}
	// Regression (2026-10-06 to 10-08): the shadow surface name made every Goals chat and Pulse lose notify_user.
	gate.AllowWorkflowNotifications(true)
	if !gate.Admit("notify_user") {
		t.Fatal("a shadow gate must not remove workflow notifications")
	}
	enforcing := newProductToolGateForAllowlist("relays", []string{"x"})
	enforcing.Shadow("goals-run", nil)
	if enforcing.shadow != nil {
		t.Fatal("an enforcing gate is not shadowed")
	}
}

// Owner, 2026-10-08: Pulse reads and talks to the Builder chat; it changes, runs and notifies nothing itself,
// even with full autonomy.
func TestPulseToolGateIsReadOnlyPlusItsRecordsAndTheBuilder(t *testing.T) {
	gate := newProductToolGateForAllowlist(pulseToolSurface, agentworksproduct.PulseTools())
	gate.AllowWorkflowNotifications(true)
	for _, name := range []string{"get_pulse_state", "query_workflow_db", "record_pulse_goal_check", "ask_builder", "get_function_call"} {
		if !gate.Admit(name) {
			t.Fatalf("Pulse must get %s", name)
		}
	}
	// Pulse's own skill pack comes from product.yaml pulse.skills, every listed skill with its file.
	pack, err := guidance.MaterializePulseSkill(agentworksproduct.PulseSkills())
	if err != nil || pack.Name != "pulse" || len(pack.SupportingFiles) != len(agentworksproduct.PulseSkills()) {
		t.Fatalf("pulse skill pack = %v, %v", pack, err)
	}
	if got := agentworksproduct.PulseWritePaths(); len(got) != 2 || got[0] != "pulse/" || got[1] != "memory/" {
		t.Fatalf("Pulse may write only pulse/ and memory/, product.yaml says %v", got)
	}
	for _, name := range []string{"update_step", "add_step", "execute_step", "run_full_workflow", "set_workflow_secret", "set_workflow_llm_config", "install_skill", "update_schedule", "agent_browser", "notify_user"} {
		if gate.Admit(name) {
			t.Fatalf("Pulse must not get %s; it asks the Builder chat", name)
		}
	}
}
