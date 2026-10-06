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
	enforcing := newProductToolGateForAllowlist("relays", []string{"x"})
	enforcing.Shadow("goals-run", nil)
	if enforcing.shadow != nil {
		t.Fatal("an enforcing gate is not shadowed")
	}
}
