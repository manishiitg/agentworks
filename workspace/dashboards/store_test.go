package dashboards

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// This drives the durable bundle path, including readers while a new draft
// exists. Publication must never expose a partial or unapproved revision.
func TestDashboardPublicationAndGuards(t *testing.T) {
	docs := t.TempDir()
	root := "Workflow/example"
	if err := os.MkdirAll(filepath.Join(docs, root), 0755); err != nil {
		t.Fatal(err)
	}
	call := func(req Request) Result {
		t.Helper()
		req.Root = root
		out, err := Execute(context.Background(), docs, req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := call(Request{Action: "create", ID: "overview", Title: "Overview", IncludeDraft: true, Files: map[string]string{"index.html": "<html><title>Overview</title><body>first {{dashboard_scripts}}</body></html>", "scripts/data.py": "print('{}')"}})
	rev1 := first.Dashboard.Revision
	if _, err := Execute(context.Background(), docs, Request{Root: root, Action: "resolve", DocumentPath: Document("overview", rev1)}); wf.StatusCode(err) != 403 {
		t.Fatalf("unpublished draft exposed: %v", err)
	}
	call(Request{Action: "publish", ID: "overview", ExpectedRevision: rev1, IncludeDraft: true})
	second := call(Request{Action: "update", ID: "overview", ExpectedRevision: rev1, IncludeDraft: true, Files: map[string]string{"index.html": "<html><title>Overview</title><body>second</body></html>"}})
	rev2 := second.Dashboard.Revision
	live := call(Request{Action: "resolve", DocumentPath: Document("overview", "")})
	if live.ResolvedPath != Document("overview", rev1) {
		t.Fatalf("draft replaced live revision: %+v", live)
	}
	published := call(Request{Action: "get", ID: "overview"})
	if published.Snapshot.Files["index.html"] != first.Snapshot.Files["index.html"] {
		t.Fatal("published snapshot mutated")
	}
	if _, err := Execute(context.Background(), docs, Request{Root: root, Action: "update", ID: "overview", ExpectedRevision: rev1, IncludeDraft: true}); wf.StatusCode(err) != 409 {
		t.Fatalf("stale edit accepted: %v", err)
	}
	for _, p := range []string{"../db.sqlite", "db.sqlite", "scripts/.env", "scripts/../../workflow.json"} {
		_, err := Execute(context.Background(), docs, Request{Root: root, Action: "update", ID: "overview", ExpectedRevision: rev2, IncludeDraft: true, Files: map[string]string{p: "bad"}})
		if wf.StatusCode(err) != 400 {
			t.Fatalf("unsafe %q accepted: %v", p, err)
		}
	}
	denied := Request{Root: root, Action: "publish", ID: "overview", ExpectedRevision: rev2, IncludeDraft: true, Guard: &wf.FolderGuard{WritePaths: []string{"docs"}}}
	if _, err := Execute(context.Background(), docs, denied); wf.StatusCode(err) != 403 {
		t.Fatalf("guard widened: %v", err)
	}
	call(Request{Action: "publish", ID: "overview", ExpectedRevision: rev2, IncludeDraft: true})
	call(Request{Action: "restore", ID: "overview", Revision: rev1, ExpectedRevision: rev2, IncludeDraft: true})
	if call(Request{Action: "resolve", DocumentPath: Document("overview", "")}).ResolvedPath != Document("overview", rev1) {
		t.Fatal("restore did not switch live pointer")
	}
	if err := os.MkdirAll(filepath.Join(docs, "Workflow/unsafe"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(docs, root), filepath.Join(docs, "Workflow/unsafe/db")); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), docs, Request{Root: "Workflow/unsafe", Action: "create", ID: "bad", Title: "Bad", IncludeDraft: true, Files: map[string]string{"index.html": "bad"}}); wf.StatusCode(err) != 403 {
		t.Fatalf("linked write accepted: %v", err)
	}
}
