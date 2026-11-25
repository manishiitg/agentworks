package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4078: removing a Mac project's functions deletes data, so the original is backed up first and caller bindings
// (records with input_mapping, which are not functions) stay.
func TestMacFunctionRemovalBacksUpAndKeepsBindings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "functions.json")
	original := `{"functions":[{"id":"f1","name":"trending"},{"id":"b1","name":"daily","input_mapping":{"topic":"x"}}]}`
	if err := os.WriteFile(file, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := removeFunctionsFromFile(file)
	if err != nil || len(removed) != 1 || removed[0] != "trending" {
		t.Fatalf("removed %v, %v", removed, err)
	}
	backup, _ := os.ReadFile(file + crewFunctionMacRemovalBackup)
	if string(backup) != original {
		t.Fatal("no backup of the original before removing")
	}
	after, _ := os.ReadFile(file)
	if strings.Contains(string(after), "trending") || !strings.Contains(string(after), "input_mapping") {
		t.Fatalf("wrong records kept: %s", after)
	}
}
