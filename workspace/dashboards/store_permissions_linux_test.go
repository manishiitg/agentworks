//go:build linux

package dashboards

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A dashboard used to create db/ as 2750 in an otherwise writable Crew,
// preventing the owner's slot from creating its db/migrations/ source.
func TestDashboardDirectoriesPreserveProjectGroupWrite(t *testing.T) {
	docs := t.TempDir()
	root := "Crew/example"
	abs := filepath.Join(docs, root)
	if err := os.MkdirAll(abs, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(abs, os.ModeSetgid|0o770); err != nil {
		t.Fatal(err)
	}
	_, err := Execute(context.Background(), docs, Request{Root: root, Action: "create", ID: "overview", Title: "Overview", IncludeDraft: true, Files: map[string]string{"index.html": "<html><body>Example</body></html>"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(filepath.Join(abs, "db"), func(p string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err == nil && (info.Mode().Perm() != 0o770 || info.Mode()&os.ModeSetgid == 0) {
			t.Errorf("service-created directory %s has mode %v", p, info.Mode())
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
