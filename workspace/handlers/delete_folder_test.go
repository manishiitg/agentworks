package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// PLAT-597: private sandbox directories used to make RemoveAll delete the
// workflow manifest and then return 500, leaving the UI unable to retry.
func TestDeleteFolderWithPrivateSandboxFilesRemovesWorkspaceAndReportsCleanup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can traverse the permission-denied fixture")
	}
	parent := t.TempDir()
	docsDir := filepath.Join(parent, "docs")
	target := filepath.Join(docsDir, "Workflow", "delete-regression")
	private := filepath.Join(target, ".sandbox-cache", "private")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "workflow.json"), []byte(`{"id":"regression"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "credential"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		entries, _ := os.ReadDir(parent)
		for _, entry := range entries {
			_ = os.Chmod(filepath.Join(parent, entry.Name(), "folder", ".sandbox-cache", "private"), 0700)
		}
		_ = os.Chmod(private, 0700)
	})
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	r := gin.New()
	r.DELETE("/api/folders/*folderpath", DeleteFolder)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/folders/Workflow/delete-regression?confirm=true", nil))
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			CleanupPending bool `json:"cleanup_pending"`
		} `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Success || !result.Data.CleanupPending {
		t.Fatalf("expected successful removal with cleanup pending: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("deleted workflow remains in the served tree: %v", err)
	}
	entries, _ := os.ReadDir(parent)
	for _, entry := range entries {
		if entry.Name() == "docs" {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("cleanup staging must remain private: %v %v", info, err)
		}
		stagedPrivate := filepath.Join(parent, entry.Name(), "folder", ".sandbox-cache", "private")
		if err := os.Chmod(stagedPrivate, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(stagedPrivate, "credential")); err != nil {
			t.Fatalf("inaccessible sandbox files were not isolated for cleanup: %v", err)
		}
	}
}

// Gin's wildcard route capture always includes the leading slash
// ("/Chats/foo" for a request to /api/folders/Chats/foo). Without stripping
// it, SanitizeInputPath sees what looks like an absolute path, IsPerUserPath's
// prefix match never fires, resolution skips the _users/<id>/ prefix
// entirely, and the handler reports "Folder not found" for a folder that
// genuinely exists — confirmed live against a real desktop deployment before
// this fix (the delete silently no-op'd while the caller saw success).
func TestDeleteFolderResolvesPerUserPathDespiteWildcardLeadingSlash(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()

	target := filepath.Join(docsDir, utils.UsersDirectory, "default", "Chats", "ExampleApp", "activities", "old-one")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "activity.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write activity.json: %v", err)
	}

	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	r := gin.New()
	r.DELETE("/api/folders/*folderpath", DeleteFolder)

	req, _ := http.NewRequest("DELETE", "/api/folders/Chats/ExampleApp/activities/old-one?confirm=true", nil)
	req.Header.Set("X-User-ID", "default")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("folder still exists on disk after a reported-successful delete: %v", err)
	}
}

// Refusing an unconfirmed delete must not depend on the leading-slash fix —
// this stays a 400 either way.
func TestDeleteFolderRequiresConfirmation(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	r := gin.New()
	r.DELETE("/api/folders/*folderpath", DeleteFolder)

	req, _ := http.NewRequest("DELETE", "/api/folders/Chats/ExampleApp/activities/old-one", nil)
	req.Header.Set("X-User-ID", "default")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without confirm=true, got %d: %s", w.Code, w.Body.String())
	}
}

// PLAT-847-7: releases are a sibling namespace, not inside the deleted draft.
func TestDeleteRelayIncludesOnlyItsSiblingReleases(t *testing.T) {
	t.Setenv("AGENTWORKS_SLOTS", "")
	docs := filepath.Join(t.TempDir(), "docs")
	draft := filepath.Join(docs, "Workflow", "relay-fixture")
	releases := filepath.Join(docs, filepath.FromSlash(workspaceref.RelayReleaseRoot("Workflow/relay-fixture")))
	other := filepath.Join(docs, filepath.FromSlash(workspaceref.RelayReleaseRoot("Workflow/other")))
	for _, dir := range []string{draft, filepath.Join(releases, "v1", "runs", "run-1", ".relay_dbos"), other} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(draft, "workflow.json"), []byte(`{"kind":"relay"}`), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// A redirected namespace must leave the draft available for retry.
	moved := releases + "-real"
	if err := os.Rename(releases, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, releases); err != nil {
		t.Fatal(err)
	}
	if _, err := removeFolderAtomically(docs, draft); err == nil {
		t.Fatal("accepted redirected release root")
	}
	if _, err := os.Stat(filepath.Join(draft, "workflow.json")); err != nil {
		t.Fatalf("draft changed on refusal: %v", err)
	}
	if err := os.Remove(releases); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, releases); err != nil {
		t.Fatal(err)
	}
	if pending, err := removeFolderAtomically(docs, draft); err != nil || pending {
		t.Fatalf("delete: pending=%v err=%v", pending, err)
	}
	for _, dir := range []string{draft, releases} {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Fatalf("deleted root remains: %s %v", dir, err)
		}
	}
	for _, dir := range []string{other, filepath.Join(outside, "keep")} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("unrelated path changed: %s %v", dir, err)
		}
	}
}
