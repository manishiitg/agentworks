package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestApplyDiffPatchDirectCreatesContentFromEmptyFile(t *testing.T) {
	diff := "--- a/references/unfollow-cleanup.md\n" +
		"+++ b/references/unfollow-cleanup.md\n" +
		"@@ -0,0 +1,3 @@\n" +
		"+# X Unfollow Cleanup\n" +
		"+\n" +
		"+Use the shared browser and confirm each unfollow dialog.\n"

	got, err := ApplyDiffPatchDirect("", diff)
	if err != nil {
		t.Fatalf("ApplyDiffPatchDirect returned error: %v", err)
	}
	want := "# X Unfollow Cleanup\n\nUse the shared browser and confirm each unfollow dialog.\n"
	if got != want {
		t.Fatalf("patched content = %q, want %q", got, want)
	}
}

func TestApplyExplicitCreationDiffPreservesExactJSONFormatting(t *testing.T) {
	diff := "--- /dev/null\n" +
		"+++ b/created.json\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+{\"created\": true}\n" +
		"\\ No newline at end of file\n"

	got, err := ApplyDiffPatchDirect("", diff)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"created\": true}"
	if got != want {
		t.Fatalf("created content=%q want=%q", got, want)
	}
}

func TestApplyDiffPatchPreservesExactJSONFormattingOnUpdate(t *testing.T) {
	current := "{\"connected\": false, \"browser_version\": \"Chrome/unknown\"}"
	diff := "--- a/status.json\n" +
		"+++ b/status.json\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-{\"connected\": false, \"browser_version\": \"Chrome/unknown\"}\n" +
		"\\ No newline at end of file\n" +
		"+{\"connected\": true, \"browser_version\": \"Chrome/unknown\"}\n" +
		"\\ No newline at end of file\n"

	got, err := ApplyDiffPatchDirect(current, diff)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"connected\": true, \"browser_version\": \"Chrome/unknown\"}"
	if got != want {
		t.Fatalf("updated content=%q want=%q", got, want)
	}
}

func TestApplyExplicitCreationDiffIsIdempotentForExactContent(t *testing.T) {
	diff := "--- /dev/null\n" +
		"+++ b/created.json\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+{\"created\": true}\n"

	current := "{\"created\": true}\n"
	got, err := ApplyDiffPatchDirect(current, diff)
	if err != nil {
		t.Fatal(err)
	}
	if got != current {
		t.Fatalf("idempotent result=%q want=%q", got, current)
	}
}

func TestApplyExplicitCreationDiffRejectsEquivalentJSONWithDifferentFormatting(t *testing.T) {
	diff := "--- /dev/null\n" +
		"+++ b/created.json\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+{\"created\": true}\n"

	_, err := ApplyDiffPatchDirect("{\n  \"created\": true\n}\n", diff)
	if err == nil || !strings.Contains(err.Error(), "already exists with different content") {
		t.Fatalf("expected differently formatted JSON to require an update diff, got %v", err)
	}
}

func TestApplyExplicitCreationDiffRejectsDifferentExistingContent(t *testing.T) {
	diff := "--- /dev/null\n" +
		"+++ b/created.json\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+{\"created\": true}\n"

	_, err := ApplyDiffPatchDirect("{\"created\": false}\n", diff)
	if err == nil || !strings.Contains(err.Error(), "already exists with different content") {
		t.Fatalf("expected an existing-content conflict, got %v", err)
	}
}

func TestApplyDiffPatchHonorsContextCancellation(t *testing.T) {
	binDir := t.TempDir()
	patchPath := filepath.Join(binDir, "patch")
	if err := os.WriteFile(patchPath, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := applyDiffPatch(ctx, "before\n", "--- a/file\n+++ b/file\n@@ -1,1 +1,1 @@\n-before\n+after\n")
	if err == nil || !strings.Contains(err.Error(), "patch command canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestDiffPatchErrorPreviewTruncatesLargeDiff(t *testing.T) {
	diff := "--- a/file\n+++ b/file\n@@ -0,0 +1,1 @@\n+" + strings.Repeat("x", 5000)

	got := diffPatchErrorPreview(diff)
	if len(got) >= len(diff) {
		t.Fatalf("expected preview to be shorter than original diff: preview=%d original=%d", len(got), len(diff))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation marker in preview, got %q", got[len(got)-80:])
	}
}

func TestDiffPatchDocumentErrorDoesNotEchoFullDiff(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)

	router := gin.New()
	router.PATCH("/api/documents/*filepath", HandleDocumentRequest)

	diff := "this is not a diff\n" + strings.Repeat("x", 50000)
	body, err := json.Marshal(map[string]string{"diff": diff})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/documents/_tmp_codex_probe/bad%20diff.md/diff", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	responseBody := w.Body.String()
	if len(responseBody) > 12000 {
		t.Fatalf("error response too large: %d bytes", len(responseBody))
	}
	if strings.Contains(responseBody, strings.Repeat("x", 20000)) {
		t.Fatalf("error response echoed the full diff")
	}
	if !strings.Contains(responseBody, "truncated") {
		t.Fatalf("error response should contain truncation marker: %s", responseBody)
	}
	if _, err := os.Stat(filepath.Join(docsDir, "_tmp_codex_probe", "bad diff.md")); !os.IsNotExist(err) {
		t.Fatalf("failed creation patch left a target file behind: %v", err)
	}
}

// PUL-4AD362CD (latency-report): a degraded rerun "created" a report that already
// existed with ordinary --- a/ headers and a @@ -0,0 @@ hunk; patch(1)
// prepended it, leaving two concatenated JSON documents.
func TestDiffPatchRejectsLineZeroInsertionIntoExistingFile(t *testing.T) {
	existing := "{\"ok\": true}\n"
	diff := "--- a/daily_report.json\n+++ b/daily_report.json\n@@ -0,0 +1 @@\n+{\"ok\": false}\n"
	got, err := ApplyDiffPatchDirect(existing, diff)
	if err == nil {
		t.Fatalf("line-0 insertion into an existing file was applied: %q", got)
	}
	if created, err := ApplyDiffPatchDirect("", diff); err != nil || created != "{\"ok\": false}\n" {
		t.Fatalf("creating content in an empty file must still work: %q %v", created, err)
	}
}
