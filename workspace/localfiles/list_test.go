package localfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A big folder (a Downloads folder) is paged and searched, not refused, and the reply says how much matched and whether more follows.
func TestLocalListPagesAndSearchesABigFolder(t *testing.T) {
	e, g := shellFixture(t)
	for i := 0; i < 250; i++ {
		if err := os.WriteFile(filepath.Join(g.Root, fmt.Sprintf("item-%03d.txt", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(g.Root, "special-report.pdf"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	list := func(change func(*Request)) Response {
		r := Request{Operation: "list", ResourceID: "project", Path: ".", RequestID: "list"}
		change(&r)
		return e.Execute(t.Context(), r)
	}
	// 250 items, the report and the read-only "locked" folder (the "blocked" folder is hidden by its rule).
	first := list(func(*Request) {})
	if first.Status != 200 || len(first.Entries) != 200 || first.Total != 252 || !first.Truncated {
		t.Fatalf("first page: status=%d entries=%d total=%d truncated=%v", first.Status, len(first.Entries), first.Total, first.Truncated)
	}
	rest := list(func(r *Request) { r.Offset = 200 })
	if len(rest.Entries) != 52 || rest.Truncated || rest.Entries[len(rest.Entries)-1].Path != "special-report.pdf" {
		t.Fatalf("last page: entries=%d truncated=%v", len(rest.Entries), rest.Truncated)
	}
	found := list(func(r *Request) { r.Query = "ITEM-1" })
	if len(found.Entries) != 100 || found.Total != 100 || found.Truncated {
		t.Fatalf("search: entries=%d total=%d truncated=%v", len(found.Entries), found.Total, found.Truncated)
	}
	small := list(func(r *Request) { r.Query = "item-1"; r.Limit = 30 })
	if len(small.Entries) != 30 || small.Total != 100 || !small.Truncated {
		t.Fatalf("small page: entries=%d total=%d truncated=%v", len(small.Entries), small.Total, small.Truncated)
	}
	if one := list(func(r *Request) { r.Query = "report" }); len(one.Entries) != 1 || one.Entries[0].Path != "special-report.pdf" {
		t.Fatalf("name search: %+v", one.Entries)
	}
}
