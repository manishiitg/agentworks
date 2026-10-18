package step_based_workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run observations must not crawl workflow knowledge/history, and their plan
// reads must be bounded. The full Pulse map and strict graph path remain separate.
func TestQuickRunReferencesStayBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "planning"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "knowledgebase"), 0755); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(root, "planning", "plan.json")
	if err := os.WriteFile(plan, []byte(`{"steps":[{"id":"step-consumer","type":"scripted","context_dependencies":["missing.json"]}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	// This document would add a second break to the full reference map.
	if err := os.WriteFile(filepath.Join(root, "knowledgebase", "guide.md"), []byte("Read code/removed-step/main.py."), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := quickRunReferenceMap(root)
	if err != nil || report.Breaks != 1 || len(report.Issues) != 1 || report.Issues[0].Kind != "dependency_unproduced" {
		t.Fatalf("quick graph observation = %+v, %v", report, err)
	}
	if err := os.WriteFile(plan, []byte(strings.Repeat(" ", (1<<20)+1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := quickRunReferenceMap(root); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized plan was read: %v", err)
	}
}
