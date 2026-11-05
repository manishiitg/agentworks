package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-871-31: remove every Brain job only after a private exact backup,
// without changing another product's state or any conversation/history file.
func TestRetireBrainSchedulesPreservesOtherProductsAndHistory(t *testing.T) {
	docs := t.TempDir()
	dir := filepath.Join(docs, "_users", "person", "chat_history")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, productScheduleStateFile)
	raw := []byte(`{"knowledgebase/organize":{"enabled":true},"knowledgebase/another-job":{"enabled":false},"work/project/task":{"enabled":true,"unknown":"keep"},"knowledgebase-other/task":{"enabled":true}}`)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(dir, "conversation.json")
	if err := os.WriteFile(history, []byte("history stays"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := retireBrainSchedules(docs); err != nil {
		t.Fatal(err)
	}
	next, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(next, &state); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(state["work/project/task"], &got); err != nil {
		t.Fatal(err)
	}
	if len(state) != 2 || got["enabled"] != true || got["unknown"] != "keep" {
		t.Fatalf("unrelated state changed: %s", next)
	}
	if _, ok := state["knowledgebase-other/task"]; !ok {
		t.Fatal("prefix neighbor removed")
	}
	backups, _ := filepath.Glob(file + ".before-brain-retirement-*.bak")
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	saved, _ := os.ReadFile(backups[0])
	if !bytes.Equal(saved, raw) {
		t.Fatal("backup was not original bytes")
	}
	info, _ := os.Stat(backups[0])
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup not private")
	}
	kept, _ := os.ReadFile(history)
	if string(kept) != "history stays" {
		t.Fatal("conversation changed")
	}
	if err := retireBrainSchedules(docs); err != nil {
		t.Fatal(err)
	}
	backups, _ = filepath.Glob(file + ".before-brain-retirement-*.bak")
	if len(backups) != 1 {
		t.Fatal("second start rewrote backup")
	}
	for _, content := range []string{`{"broken":`, string(raw)} {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if content == string(raw) {
			if err := os.Chmod(dir, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		}
		if err := retireBrainSchedules(docs); err == nil {
			t.Fatal("invalid state/failed backup must report an error")
		}
		unchanged, _ := os.ReadFile(file)
		if string(unchanged) != content {
			t.Fatal("changed state without a valid backup")
		}
	}
}
