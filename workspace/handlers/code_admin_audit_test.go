package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeAdminAuditIsAppendOnly(t *testing.T) {
	docs := t.TempDir()
	for _, entry := range []string{`{"action":"a"}`, `{"action":"b"}`} {
		if err := appendCodeAdminAuditLine(docs, "2026-09", entry); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(docs, "config/code-admin-audit/2026-09.jsonl"))
	if err != nil || string(data) != "{\"action\":\"a\"}\n{\"action\":\"b\"}\n" {
		t.Fatalf("log = %q %v", data, err)
	}
	// A link planted as the log is refused, never written through.
	other := filepath.Join(docs, "elsewhere")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(docs, "config/code-admin-audit/2026-10.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := appendCodeAdminAuditLine(docs, "2026-10", `{"action":"c"}`); err == nil {
		t.Fatal("appended through a link")
	}
	if data, _ := os.ReadFile(other); strings.Contains(string(data), "action") {
		t.Fatal("the link target was written")
	}
}
