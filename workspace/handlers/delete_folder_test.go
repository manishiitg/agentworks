package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/utils"

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

	target := filepath.Join(docsDir, utils.UsersDirectory, "default", "Chats", "SparkQuill", "activities", "old-one")
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

	req, _ := http.NewRequest("DELETE", "/api/folders/Chats/SparkQuill/activities/old-one?confirm=true", nil)
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

	req, _ := http.NewRequest("DELETE", "/api/folders/Chats/SparkQuill/activities/old-one", nil)
	req.Header.Set("X-User-ID", "default")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without confirm=true, got %d: %s", w.Code, w.Body.String())
	}
}
