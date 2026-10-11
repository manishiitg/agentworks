package step_based_workflow

import (
	"context"
	"encoding/json"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// TestCompletePlanChangelogEntryPrefersDeletedStepsOverEmptyChanges is the
// PLAT-074 regression: delete_plan_steps and delete_todo_task_route capture
// the real deleted content as DeletedSteps but never set Changes or
// BeforeSnapshot, so before this fix every deletion collapsed to the same
// sha256("[]") placeholder as a call site with no data at all — despite real
// before-state content being on hand at the call site.
func TestCompletePlanChangelogEntryPrefersDeletedStepsOverEmptyChanges(t *testing.T) {
	entry := PlanChangelogEntry{
		Tool:         "delete_plan_steps",
		DeletedSteps: []json.RawMessage{json.RawMessage(`{"id":"step-1","title":"Old step"}`)},
	}
	completePlanChangelogEntry(&entry)

	if entry.BeforeRef == emptyChangesRefHash {
		t.Fatalf("before_ref still collapsed to the empty-array placeholder despite real DeletedSteps content: %q", entry.BeforeRef)
	}
	if entry.BeforeRef == "" {
		t.Fatal("expected a non-empty before_ref")
	}
}

// TestCompletePlanChangelogEntryPrefersAddedStepsOverEmptyChanges mirrors the
// above for the add side (add_scripted_step and its four siblings share one
// call site that only ever set AddedSteps).
func TestCompletePlanChangelogEntryPrefersAddedStepsOverEmptyChanges(t *testing.T) {
	entry := PlanChangelogEntry{
		Tool:       "add_scripted_step",
		AddedSteps: []json.RawMessage{json.RawMessage(`{"id":"step-2","title":"New step"}`)},
	}
	completePlanChangelogEntry(&entry)

	if entry.AfterRef == emptyChangesRefHash {
		t.Fatalf("after_ref still collapsed to the empty-array placeholder despite real AddedSteps content: %q", entry.AfterRef)
	}
	if entry.AfterRef == "" {
		t.Fatal("expected a non-empty after_ref")
	}
}

// TestCompletePlanChangelogEntryDeletedStepsProducesDistinctRefsFromAdd
// proves a delete (DeletedSteps only) and an add (AddedSteps only) of the
// same step content produce different before/after refs — the whole point of
// tracking which side the content came from rather than mixing them.
func TestCompletePlanChangelogEntryDeletedStepsProducesDistinctRefsFromAdd(t *testing.T) {
	sameContent := json.RawMessage(`{"id":"step-3","title":"Same content either way"}`)

	deleteEntry := PlanChangelogEntry{DeletedSteps: []json.RawMessage{sameContent}}
	completePlanChangelogEntry(&deleteEntry)

	addEntry := PlanChangelogEntry{AddedSteps: []json.RawMessage{sameContent}}
	completePlanChangelogEntry(&addEntry)

	if deleteEntry.BeforeRef != addEntry.AfterRef {
		t.Fatalf("expected the delete's before_ref to equal the add's after_ref for identical content (same hash input either side): delete_before=%q add_after=%q", deleteEntry.BeforeRef, addEntry.AfterRef)
	}
	if deleteEntry.AfterRef != emptyChangesRefHash {
		t.Fatalf("a pure deletion's after_ref should stay the empty placeholder (nothing was added), got %q", deleteEntry.AfterRef)
	}
	if addEntry.BeforeRef != emptyChangesRefHash {
		t.Fatalf("a pure addition's before_ref should stay the empty placeholder (nothing existed before), got %q", addEntry.BeforeRef)
	}
}

// TestCompletePlanChangelogEntryExplicitSnapshotStillWinsOverAddedDeletedSteps
// guards the precedence order: a caller that supplies BOTH an explicit
// snapshot and AddedSteps/DeletedSteps (shouldn't normally happen, but
// defends the intended priority) must have the snapshot — the most direct
// evidence — win.
func TestCompletePlanChangelogEntryExplicitSnapshotStillWinsOverAddedDeletedSteps(t *testing.T) {
	entry := PlanChangelogEntry{
		BeforeSnapshot: map[string]interface{}{"explicit": "before"},
		AfterSnapshot:  map[string]interface{}{"explicit": "after"},
		DeletedSteps:   []json.RawMessage{json.RawMessage(`{"id":"ignored"}`)},
		AddedSteps:     []json.RawMessage{json.RawMessage(`{"id":"ignored"}`)},
	}
	completePlanChangelogEntry(&entry)

	want := PlanChangelogEntry{
		BeforeSnapshot: map[string]interface{}{"explicit": "before"},
		AfterSnapshot:  map[string]interface{}{"explicit": "after"},
	}
	completePlanChangelogEntry(&want)

	if entry.BeforeRef != want.BeforeRef || entry.AfterRef != want.AfterRef {
		t.Fatal("AddedSteps/DeletedSteps changed the refs even though an explicit snapshot was present")
	}
}

// TestLogCanonicalArtifactChangeStyleMigrationSnapshotsProduceDistinctRefs
// exercises the migrate_agent_code_items shape end-to-end: real
// pre/post plan.json content passed as Before/AfterSnapshot through
// logPlanChange must persist distinct, non-placeholder refs.
func TestLogCanonicalArtifactChangeStyleMigrationSnapshotsProduceDistinctRefs(t *testing.T) {
	var persisted string
	readFile := func(context.Context, string) (string, error) { return "", nil }
	writeFile := func(_ context.Context, _ string, content string) error {
		persisted = content
		return nil
	}

	logPlanChange(context.Background(), "Workflow/demo", PlanChangelogEntry{
		Tool:           "migrate_agent_code_items",
		Reason:         "test migration",
		BeforeSnapshot: json.RawMessage(`{"steps":[{"id":"a"}]}`),
		AfterSnapshot:  json.RawMessage(`{"steps":[{"id":"a-1"},{"id":"a-2"}]}`),
	}, readFile, writeFile, loggerv2.NewNoop())

	var clog PlanChangelog
	if err := json.Unmarshal([]byte(persisted), &clog); err != nil {
		t.Fatalf("persisted changelog did not parse: %v", err)
	}
	if len(clog.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(clog.Entries))
	}
	got := clog.Entries[0]
	if got.BeforeRef == emptyChangesRefHash || got.AfterRef == emptyChangesRefHash {
		t.Fatalf("refs collapsed to the empty-array placeholder: before=%q after=%q", got.BeforeRef, got.AfterRef)
	}
	if got.BeforeRef == got.AfterRef {
		t.Fatalf("before_ref == after_ref (%q) despite different plan content", got.BeforeRef)
	}
}
