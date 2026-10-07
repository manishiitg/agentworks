package server

import "testing"

// A workflow handed to a new owner kept running its schedules as its creator, who was no longer an owner, and would
// have run as a disabled account once the creator was disabled (Dominion trading workflow, 2026-10-07).
func TestScheduledRunsUseAnActiveOwner(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"old","username":"old","email":"old@x.com","admin":true,"disabled":true},{"id":"new","username":"new","email":"new@x.com","admin":true},{"id":"creator","username":"creator","email":"c@x.com","admin":true}]}`)
	handed := &WorkflowManifest{CreatedBy: "old", Access: &WorkflowAccess{Owners: []string{"new"}}}
	if got := workflowExecutionOwnerUserID(handed); got != "new" {
		t.Fatalf("a handed-over workflow must run as its active owner, got %q", got)
	}
	unchanged := &WorkflowManifest{CreatedBy: "creator"}
	if got := workflowExecutionOwnerUserID(unchanged); got != "creator" {
		t.Fatalf("an ordinary workflow keeps running as its creator, got %q", got)
	}
}
