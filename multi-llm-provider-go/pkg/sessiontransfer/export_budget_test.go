package sessiontransfer

import (
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746: children/tool results count before any destination is staged.
func TestExportBudgetCountsAllResolvedSources(t *testing.T) {
	dir := t.TempDir()
	sources := map[string]string{}
	for _, name := range []string{"main", "child", "tool"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("1234"), 0600); err != nil {
			t.Fatal(err)
		}
		sources[name] = path
	}
	if err := checkExportBudget(sources, 12); err != nil {
		t.Fatal(err)
	}
	if err := checkExportBudget(sources, 11); err == nil {
		t.Fatal("child/tool sources escaped copy budget")
	}
}
