package server

import (
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

// Owner, 2026-10-08: Pulse reads and talks to the Builder chat; it changes and runs nothing itself, even with
// full autonomy, and keeps notify_user for its goal message.
func TestPulseToolGateIsReadOnlyPlusItsRecordsAndTheBuilder(t *testing.T) {
	gate := newProductToolGateForAllowlist(pulseToolSurface, pulseTools)
	gate.AllowWorkflowNotifications(true)
	for _, name := range []string{"get_pulse_state", "query_workflow_db", "record_pulse_goal_check", "ask_builder", "notify_user"} {
		if !gate.Admit(name) {
			t.Fatalf("Pulse must get %s", name)
		}
	}
	for _, name := range []string{"update_step", "add_step", "execute_step", "run_full_workflow", "set_workflow_secret", "set_workflow_llm_config", "install_skill", "update_schedule", "agent_browser"} {
		if gate.Admit(name) {
			t.Fatalf("Pulse must not get %s; it asks the Builder chat", name)
		}
	}
}
