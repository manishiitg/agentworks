package knowledgebase

import (
	"context"
	"strings"
	"testing"
)

// Every save is a commit; "what changed since" is a commit range, filtered to what the caller may read (owner,
// 2026-10-07: "i prefer commit id approach").
func TestBrainChangesSinceACommit(t *testing.T) {
	s, admin, priya := fixture(t, false)
	ctx := context.Background()
	if _, err := s.EnsureGitRepository(ctx); err != nil {
		t.Fatal(err)
	}
	folder(t, s, admin, "", "Company")
	folder(t, s, admin, "", "Private")
	grant(t, s, admin, priya.IdentityID, "Company", "Reader", "priya-reads")
	note := create(t, s, admin, "Company", "about.md", "first line\n", "about")
	create(t, s, admin, "Private", "salaries.md", "secret\n", "salaries")
	head := call(t, s, priya, "list_knowledgebase_changes", nil)["head"].(string)
	if head == "" {
		t.Fatal("saves must be commits")
	}
	call(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": note["entry_id"], "expected_version": note["version"], "content": "second line\n", "request_id": "edit"})
	create(t, s, admin, "Private", "plans.md", "hidden\n", "plans")
	changes := call(t, s, priya, "list_knowledgebase_changes", map[string]any{"since": head})
	commits := asSlice(changes["commits"])
	if len(commits) != 1 {
		t.Fatalf("a reader of Company sees exactly the Company edit since %s: %v", head, changes)
	}
	c := asMap(commits[0])
	file := asMap(asSlice(c["files"])[0])
	if file["path"] != "Company/about.md" || file["change"] != "updated" || !strings.Contains(c["message"].(string), "Update") {
		t.Fatalf("commit: %v", c)
	}
	diff := call(t, s, priya, "read_knowledgebase_diff", map[string]any{"path": "Company/about.md", "since": head})
	if d := diff["diff"].(string); !strings.Contains(d, "-first line") || !strings.Contains(d, "+second line") {
		t.Fatalf("diff: %q", d)
	}
	if _, err := s.Call(ctx, priya, "read_knowledgebase_diff", map[string]any{"path": "Private/plans.md", "since": head}); err == nil {
		t.Fatal("no diff of a folder the caller cannot read")
	}
}
