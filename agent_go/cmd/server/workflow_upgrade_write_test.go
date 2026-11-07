package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// PLAT-746-396: the narrow writer's typed permit must not let a Contributor
// who can manage schedules change the shared workflow's contract version.
func TestWorkflowUpgradeWriterKeepsTeamLeaderPermission(t *testing.T) {
	f := newTeamTestFixture(t)
	var saved WorkflowManifest
	if err := json.Unmarshal([]byte(f.file("workflow.json")), &saved); err != nil {
		t.Fatal(err)
	}
	saved.Version, saved.CodeLayoutVersion, saved.CreatedBy = workflowContractSkillsVersion, 1, "leader"
	saved.Team.Contributors[0].ManageSchedules = true
	f.manifest(t, &saved)
	before := f.file("workflow.json")
	candidate := saved
	candidate.Version = workflowContractPortableVersion
	writeAs := func(actor string) error {
		ctx := teamTestContext(actor)
		release, err := teamLock(ctx, teamTestRoot)
		if err != nil {
			return err
		}
		defer release()
		return writeWorkflowUpgradeField(context.WithValue(ctx, teamLockHeldKey{}, teamTestRoot), teamTestRoot, "version", &candidate)
	}
	if err := writeAs("first"); err == nil || !strings.Contains(err.Error(), "schedule managers cannot change shared workflow settings") {
		t.Fatalf("schedule-manager refusal came from the wrong rule: %v", err)
	}
	if f.file("workflow.json") != before {
		t.Fatal("refused Contributor upgrade changed the manifest")
	}
	if err := writeAs("leader"); err != nil {
		t.Fatalf("authorized Leader could not save the same change: %v", err)
	}
	var after WorkflowManifest
	if err := json.Unmarshal([]byte(f.file("workflow.json")), &after); err != nil || after.Version != workflowContractPortableVersion {
		t.Fatalf("Leader's version was not saved: %+v %v", after, err)
	}
}
