package step_based_workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedDBScriptsStampRefusedUntilScriptsAreConverted(t *testing.T) {
	workflowDir := t.TempDir()
	script := filepath.Join(workflowDir, "code", "fetch", "main.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("import sqlite3, os\nsqlite3.connect(os.environ['DB_PATH'])\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := validateManagedDBScriptsStamp(ManagedDBScriptsContractVersion, workflowDir)
	if err == nil || !strings.Contains(err.Error(), "code/fetch/main.py") {
		t.Fatalf("stamp with a raw sqlite script = %v, want a refusal naming the file", err)
	}
	if err := validateManagedDBScriptsStamp("1.0.44", workflowDir); err != nil {
		t.Errorf("another version is not gated by this scan: %v", err)
	}
	if err := os.WriteFile(script, []byte("from agentworks_db import query\nquery('SELECT 1')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedDBScriptsStamp(ManagedDBScriptsContractVersion, workflowDir); err != nil {
		t.Errorf("a converted workflow must stamp: %v", err)
	}
}
