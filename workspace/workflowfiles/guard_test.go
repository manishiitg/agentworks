package workflowfiles

import "testing"

func TestFolderGuardPreservesCaseAndConservativelyDeniesAliases(t *testing.T) {
	guard := &FolderGuard{ReadPaths: []string{"src"}, WritePaths: []string{"src"}, BlockedPaths: []string{"src/private"}, ReadOnlyPaths: []string{"src/locked"}}
	for _, p := range []string{"SRC/file", "sRc/file", "src/PRIVATE/token", "src/LOCKED/file"} {
		if guard.Allows(p, true) {
			t.Fatalf("write grant widened for %s", p)
		}
	}
	if guard.Allows("SRC/file", false) || guard.AllowsTraversal("SRC") || !guard.Allows("src/file", true) || !guard.Allows("src/locked/file", false) {
		t.Fatal("case-sensitive grant boundary lost")
	}
	for _, p := range []string{"costs/usage.json", "schedule-runs.json", "workflow.json.kb-lock", "product.json", "functions.json"} {
		if !ProtectedWrite(p) {
			t.Fatalf("managed file writable: %s", p)
		}
	}
	for _, p := range []string{"key.pem", "backup.P12", "passwords.kdbx", "private.key"} {
		if !Private(p) {
			t.Fatalf("key material exposed: %s", p)
		}
	}
}
