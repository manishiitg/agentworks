package server

import (
	"testing"
)

func TestWorkProfileCanAuthorCustomSkillsWithoutSkillBuilderIdentity(t *testing.T) {
	grants := resolveConditionalGrants(QueryRequest{AgentProfileID: "work"})

	if !grants.HasGrant("work-custom-skills") {
		t.Fatal("Work profile did not receive its custom-skill authoring grant")
	}
	if grants.HasGrant("skill-creator") {
		t.Fatal("Work profile must not enter the separate Skill Builder mode")
	}
	if len(grants.WriteFolders) != 0 {
		t.Fatalf("skill authoring must use existing workspace permissions, got extra writes %v", grants.WriteFolders)
	}
	creator := resolveConditionalGrants(QueryRequest{SelectedSkills: []string{"skill-creator"}})
	if len(creator.WriteFolders) != 0 {
		t.Fatalf("Skill Builder must not grant shared-library writes: %v", creator.WriteFolders)
	}
}

func TestNonWorkProfileDoesNotReceiveCustomSkillAuthoringGrant(t *testing.T) {
	grants := resolveConditionalGrants(QueryRequest{AgentProfileID: "agentworks"})
	if grants.HasGrant("work-custom-skills") {
		t.Fatal("non-Work profile unexpectedly received Work's custom-skill authoring grant")
	}
}
