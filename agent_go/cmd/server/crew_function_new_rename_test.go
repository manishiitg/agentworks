package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The one-off changes only a function's "fresh" to "new", keeps every other field and number, copies the original
// first, and leaves a file with nothing to change untouched.
func TestFunctionFreshRenamedToNewKeepingEverythingElse(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "functions.json")
	original := `{"functions":[{"id":"a","name":"check","action":{"type":"message","conversation":"fresh"},"large":12345678901234567890},` +
		`{"id":"b","name":"tab","action":{"type":"message","tab":"fn.tab"}},{"id":"c","name":"hook","action":{"type":"message","conversation":"isolated"}},` +
		`{"id":"d","name":"old","execution":"fresh","action":{"type":"message"}}]}`
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := renameFunctionFreshInFile(file)
	if err != nil || changed != 2 {
		t.Fatalf("changed = %d, %v; want the two fresh functions", changed, err)
	}
	updated, _ := os.ReadFile(file)
	text := string(updated)
	if strings.Contains(text, `"fresh"`) || strings.Count(text, `"new"`) != 2 || !strings.Contains(text, `"isolated"`) || !strings.Contains(text, "12345678901234567890") || !strings.Contains(text, `"fn.tab"`) {
		t.Fatalf("rewritten file: %s", text)
	}
	if backup, err := os.ReadFile(file + crewFunctionNewRenameBackup); err != nil || string(backup) != original {
		t.Fatalf("the original was not copied first: %v", err)
	}
	if again, err := renameFunctionFreshInFile(file); err != nil || again != 0 {
		t.Fatalf("a second pass changed %d: %v", again, err)
	}
	other := filepath.Join(dir, "other", "functions.json")
	_ = os.MkdirAll(filepath.Dir(other), 0o755)
	_ = os.WriteFile(other, []byte(`{"functions":[{"id":"x","name":"x","action":{"type":"message","tab":"t"}}]}`), 0o600)
	if untouched, err := renameFunctionFreshInFile(other); err != nil || untouched != 0 {
		t.Fatalf("a file with nothing to change was changed: %d %v", untouched, err)
	}
	if _, err := os.Stat(other + crewFunctionNewRenameBackup); err == nil {
		t.Fatal("a backup was written for an untouched file")
	}
}
