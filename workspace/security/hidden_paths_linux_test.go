//go:build linux

package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A blocked file inside a writable folder (a Code project's db/db.sqlite) used to make the Landlock policy fail, and
// the command then fell back to the mount-namespace backend, which ran it as the service account with the host
// readable (Excellence 2026-10-03: an agent's shell read the platform's .env). The blocked file is now hidden by the
// launcher and the command stays under Landlock.
func TestBlockedFileInsideAWritableFolderIsHiddenNotAFallback(t *testing.T) {
	project := t.TempDir()
	db := filepath.Join(project, "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("SECRET-DB-CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	iso := &Isolator{ReadPaths: []string{project}, WritePaths: []string{project}, BlockedPaths: []string{db}, WorkDir: project, BaseDir: project}
	policy, err := iso.landlockPolicy()
	if err != nil {
		t.Fatalf("landlockPolicy() = %v, want the blocked file as a hidden path", err)
	}
	if len(policy.HiddenPaths) != 1 || policy.HiddenPaths[0] != canonicalPath(db) {
		t.Fatalf("HiddenPaths = %v, want [%s]", policy.HiddenPaths, db)
	}
	if _, err := landlockRunnerPath(); err != nil || !landlockNamespacesAvailable() {
		t.Skip("needs the Landlock launcher with its namespaces (AGENTWORKS_LANDLOCK_RUNNER)")
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("HOST-FILE"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`cat %q 2>&1; echo; echo junk > %q 2>&1 && echo WROTE_DB; rm -f %q 2>&1 && echo REMOVED_DB; echo ok > %q && echo WROTE_PROJECT; cat %q 2>&1`,
		db, db, db, filepath.Join(project, "ok.txt"), outside)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, "sh", []string{"-c", script})
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("ExecuteIsolated: %v", err)
	}
	out, _ := cmd.CombinedOutput()
	got := string(out)
	if strings.Contains(got, "SECRET-DB-CONTENT") {
		t.Errorf("the blocked file was readable: %s", got)
	}
	if strings.Contains(got, "WROTE_DB") || strings.Contains(got, "REMOVED_DB") {
		t.Errorf("the blocked file could be changed: %s", got)
	}
	if raw, _ := os.ReadFile(db); string(raw) != "SECRET-DB-CONTENT" {
		t.Errorf("the real blocked file changed: %q", raw)
	}
	if !strings.Contains(got, "WROTE_PROJECT") {
		t.Errorf("the rest of the project must stay writable: %s", got)
	}
	if strings.Contains(got, "HOST-FILE") {
		t.Errorf("a file outside the grants was readable (the weak fallback ran): %s", got)
	}
}

// A blocked path that contains a granted one cannot be expressed: it is refused, never run under a weaker sandbox.
func TestBlockedPathContainingAGrantIsRefused(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	iso := &Isolator{ReadPaths: []string{inner}, WritePaths: []string{inner}, BlockedPaths: []string{root}, WorkDir: inner, BaseDir: root}
	if abi, err := landlockABI(); err != nil || abi < 1 {
		t.Skip("needs Landlock")
	}
	cmd, cleanup, err := iso.ExecuteIsolated(context.Background(), "true", nil)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || cmd != nil {
		t.Fatal("a policy Landlock cannot carry must be refused, not run under the mount-namespace fallback")
	}
}
