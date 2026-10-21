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
	_, err := Execute(context.Background(), docs, Request{Root: root, Action: "create", ID: "overview", Title: "Overview", IncludeDraft: true, Files: map[string]string{"index.html": "<html><body>Example</body></html>", "summary.json": "{}", "scripts/data.py": "print('{}')"}})
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

func TestDashboardSlotReadsOnlyPublicArtifacts(t *testing.T) {
	docs := t.TempDir()
	project := "Crew/example"
	abs := filepath.Join(docs, project)
	if err := os.MkdirAll(abs, 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(abs, os.ModeSetgid|0770); err != nil {
		t.Fatal(err)
	}
	out, err := Execute(context.Background(), docs, Request{Root: project, Action: "create", ID: "overview", Title: "Example", IncludeDraft: true, Files: map[string]string{"index.html": "<html>Example</html>", "summary.json": "{}", "scripts/data.py": "print('{}')"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{Document("overview", out.Dashboard.Revision): 0640, renderFile("overview", out.Dashboard.Revision, "summary.json"): 0640, StatePath("overview"): 0600, snapshotPath("overview", out.Dashboard.Revision): 0600, renderFile("overview", out.Dashboard.Revision, "scripts/data.py"): 0600} {
		info, err := os.Stat(filepath.Join(abs, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode %o, want %o", name, info.Mode().Perm(), want)
		}
	}
}
