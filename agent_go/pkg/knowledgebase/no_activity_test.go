package knowledgebase

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMutationsAndBackupsDoNotRecordActivity(t *testing.T) {
	s, admin, _ := fixture(t, true)
	folder(t, s, admin, "", "Payments")
	grant(t, s, admin, "priya", "Payments", "Reader", "no-activity-grant")
	entry := create(t, s, admin, "Payments", "guide.md", "# Guide\n", "no-activity-create")
	entry = call(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "content": "# Updated\n", "expected_version": entry["version"], "request_id": "no-activity-update"})
	receipt := commit(t, s, admin, entry, "no-activity-commit")
	push(t, s, admin, receipt, "no-activity-push")
	if _, err := s.ReconcileBackup(t.Context(), admin); err != nil {
		t.Fatal(err)
	}
	call(t, s, admin, "delete_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"], "request_id": "no-activity-delete"})
	if _, err := os.Stat(filepath.Join(s.private, "activity")); !os.IsNotExist(err) {
		t.Fatalf("activity state created: %v", err)
	}
	if _, err := s.Call(t.Context(), admin, "get_knowledgebase_activity", map[string]any{}); err == nil {
		t.Fatal("removed activity operation remains callable")
	}
}
