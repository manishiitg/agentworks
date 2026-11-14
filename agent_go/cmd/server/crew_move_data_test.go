package server

import "testing"

// PLAT-746-885: a physical agent keeps its identity when moving to a Crew
// that has no Main; a root alias must not fabricate a new agent.
func TestCrewMovePhysicalAgentNeedsNoMain(t *testing.T) {
	header := crewMoveHeader{SourceID: "source", MainKey: "source:chat:original"}
	for _, key := range []string{"source:chat:original", "source:chat:helper"} {
		got, err := crewMoveKey(key, header, "destination", "")
		if err != nil || got != "destination"+key[len("source"):] {
			t.Fatalf("physical agent mapping: %q %v", got, err)
		}
	}
	if _, err := crewMoveKey("source", header, "destination", ""); err == nil {
		t.Fatal("missing Main must require an agent choice")
	}
}
