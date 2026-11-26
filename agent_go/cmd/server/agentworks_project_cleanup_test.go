package server

import "testing"

// The cleanup deletes projects, so it must match only the id the platform reserved for a person's AgentWorks project
// (#3692): an ordinary project someone named "AgentWorks" has a random id and is never touched.
func TestRetiredAgentWorksProjectIDMatchesReservedID(t *testing.T) {
	if got := retiredAgentWorksProjectID("default"); got != "dc8de1f8-953c-5ced-a5d5-7ae49d6be433" {
		t.Fatalf("reserved id for default = %s", got)
	}
	if retiredAgentWorksProjectID("owner@example.com") == retiredAgentWorksProjectID("teammate@example.com") {
		t.Fatal("two people share one reserved id")
	}
}
