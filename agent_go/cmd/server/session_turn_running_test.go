package server

import "testing"

// A turn that holds the input lane is running, whatever the display busy flag
// says. MCP turn_status read only the flag and reported idle mid-turn.
func TestTurnHoldingTheInputLaneIsRunningForStatusAndLifecycle(t *testing.T) {
	api := &StreamingAPI{}
	release := api.lockSessionInputLane("s1")
	response := map[string]interface{}{}
	api.externalTurnFields(response, "s1", false)
	if response["turn_status"] != "running" || !api.sessionRunsATurn("s1") {
		t.Fatalf("a turn holding the lane must be running: %v", response)
	}
	release()
	response = map[string]interface{}{}
	api.externalTurnFields(response, "s1", false)
	if response["turn_status"] != "idle" || api.sessionRunsATurn("s1") {
		t.Fatalf("a released lane must be idle: %v", response)
	}
}
