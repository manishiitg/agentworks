package crewsync

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-508: an executable script from a Unix server must survive Windows
// capture, edits, retries and deletion without inventing a Unix chmod change.
func TestWindowsSyncPreservesScriptMetadataAndExternalEdits(t *testing.T) {
	root := t.TempDir()
	roots := crewroot.Roots{Files: root, Resources: root}
	name := "resources/notes café/start.sh"
	data := []byte("#!/bin/sh\nprintf 'hello\\n'\n")
	file := fingerprint(data, true)
	first := Snapshot{Files: Manifest{name: file}, Changes: []Change{{Path: name, After: &file, DataPath: testDataPath(t, data)}}}
	if err := Apply(t.Context(), roots, first); err != nil {
		t.Fatal(err)
	}
	if err := Apply(t.Context(), roots, first); err != nil {
		t.Fatalf("retry executable import: %v", err)
	}
	unchanged, err := Capture(t.Context(), roots, first.Files, t.TempDir())
	if err != nil || len(unchanged.Changes) != 0 {
		t.Fatalf("Windows invented a chmod change: %+v %v", unchanged.Changes, err)
	}
	path := filepath.Join(root, "notes café", "start.sh")
	if err := os.WriteFile(path, []byte("Windows edit\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	edited, err := Capture(t.Context(), roots, first.Files, t.TempDir())
	if err != nil || len(edited.Changes) != 1 || !edited.Files[name].Executable || string(testChangeData(t, edited.Changes[0])) != "Windows edit\r\n" {
		t.Fatalf("Windows edit lost mode or exact bytes: %+v %v", edited, err)
	}
	if err := Apply(t.Context(), roots, first); err == nil {
		t.Fatal("overwrote an external Windows edit")
	}
	if err := CopyContent(t.Context(), io.Discard, roots, name, edited.Files[name]); err != nil {
		t.Fatalf("bundle capture rejected retained executable bit: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	deleted, err := Capture(t.Context(), roots, edited.Files, t.TempDir())
	if err != nil || len(deleted.Changes) != 1 || deleted.Changes[0].After != nil {
		t.Fatalf("deletion lost: %+v %v", deleted, err)
	}
}
