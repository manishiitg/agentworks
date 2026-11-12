package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// setupTestDocsDir creates a realistic workspace directory structure for testing
// with per-user isolation: Chats/ and Downloads/ live under _users/{userID}/
func setupTestDocsDir(t *testing.T) (string, func()) {
	t.Helper()
	docsDir, err := os.MkdirTemp("", "docs-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Create shared folders at root level
	for _, folder := range []string{"skills", "Workflow"} {
		os.MkdirAll(filepath.Join(docsDir, folder), 0755)
	}

	// Create per-user folders under _users/default/
	defaultUserDir := filepath.Join(docsDir, utils.UsersDirectory, "default")
	for _, folder := range []string{"Chats", "Downloads"} {
		os.MkdirAll(filepath.Join(defaultUserDir, folder), 0755)
	}

	// Add files to default user's Chats
	os.WriteFile(
		filepath.Join(defaultUserDir, "Chats", "session1.json"),
		[]byte(`{"id":"s1"}`), 0644,
	)

	// Add files to skills (shared)
	os.WriteFile(filepath.Join(docsDir, "skills", "shared-skill.json"), []byte(`{}`), 0644)

	return docsDir, func() { os.RemoveAll(docsDir) }
}

// setupRouter creates a gin router with the documents endpoint and viper config
func setupRouter(docsDir string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)

	r := gin.New()
	r.GET("/api/documents", ListDocuments)
	r.GET("/api/documents/*filepath", GetDocument)
	return r
}

// listDocs makes a GET /api/documents request and returns the parsed response
func listDocs(t *testing.T, router *gin.Engine, folder string, userID string) models.APIResponse[[]models.Document] {
	t.Helper()
	url := "/api/documents"
	if folder != "" {
		url += "?folder=" + folder
	}

	req, _ := http.NewRequest("GET", url, nil)
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.APIResponse[[]models.Document]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, w.Body.String())
	}
	return resp
}

// collectFilePaths flattens a hierarchical document tree into a list of file paths
func collectFilePaths(docs []models.Document) []string {
	var paths []string
	for _, doc := range docs {
		paths = append(paths, doc.FilePath)
		if len(doc.Children) > 0 {
			paths = append(paths, collectFilePaths(doc.Children)...)
		}
	}
	return paths
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func TestJSONLIsTextBasedFile(t *testing.T) {
	if !isTextBasedFile("metrics_history.jsonl", "") {
		t.Fatal("expected .jsonl files to be treated as text")
	}
	if !isTextBasedFile("events.ndjson", "") {
		t.Fatal("expected .ndjson files to be treated as text")
	}
}

func TestGetDocumentReturnsJSONLContentAsText(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	if err := os.WriteFile(
		filepath.Join(docsDir, "Workflow", "metrics_history.jsonl"),
		[]byte("{\"ok\":true}\n"),
		0644,
	); err != nil {
		t.Fatalf("write jsonl fixture: %v", err)
	}

	router := setupRouter(docsDir)
	req, _ := http.NewRequest("GET", "/api/documents/Workflow/metrics_history.jsonl", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.APIResponse[models.Document]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, w.Body.String())
	}
	if resp.Data.IsBinary {
		t.Fatal("jsonl response should not be marked binary")
	}
	if resp.Data.Content != "{\"ok\":true}\n" {
		t.Fatalf("unexpected content: %q", resp.Data.Content)
	}
	if resp.Data.Encoding != "utf-8" {
		t.Fatalf("unexpected encoding: %q", resp.Data.Encoding)
	}
}

func TestGetDocumentReturnsBinaryMetadataWithoutFakeContent(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	if err := os.WriteFile(
		filepath.Join(docsDir, "Workflow", "payload.dat"),
		[]byte{0x00, 0x01, 0x02},
		0644,
	); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}

	router := setupRouter(docsDir)
	req, _ := http.NewRequest("GET", "/api/documents/Workflow/payload.dat", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.APIResponse[models.Document]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, w.Body.String())
	}
	if !resp.Data.IsBinary {
		t.Fatal("binary response should be marked binary")
	}
	if resp.Data.Content != "" {
		t.Fatalf("binary response should not contain fake content, got %q", resp.Data.Content)
	}
	if resp.Data.Size != 3 {
		t.Fatalf("unexpected binary size: %d", resp.Data.Size)
	}
}

// --- Tests ---

func TestRootListingFiltersUsersDirectory(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()

	router := setupRouter(docsDir)

	resp := listDocs(t, router, "", "default")
	paths := collectFilePaths(resp.Data)

	// _users/ should NOT appear in root listing (internal directory)
	if contains(paths, utils.UsersDirectory) {
		t.Error("_users/ should NOT appear in root listing")
	}

	// Per-user folders should appear with clean paths (from _users/default/)
	if !contains(paths, "Chats") {
		t.Error("Chats/ should appear in root listing (from user's directory)")
	}

	// Shared folders should appear
	if !contains(paths, "skills") {
		t.Error("skills/ should appear in root listing")
	}

	// User's file should be visible under Chats
	if !contains(paths, "Chats/session1.json") {
		t.Errorf("session1.json should appear under Chats/, got: %v", paths)
	}
}

func TestRootListingWithDotFolder(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()

	router := setupRouter(docsDir)

	resp := listDocs(t, router, ".", "default")
	paths := collectFilePaths(resp.Data)

	// folder=. should behave the same as empty folder (root listing)
	// _users/ should NOT be visible
	if contains(paths, utils.UsersDirectory) {
		t.Error("_users/ should NOT appear in root listing with folder=.")
	}

	// Per-user Chats should still appear
	if !contains(paths, "Chats") {
		t.Error("Chats/ should appear in root listing with folder=.")
	}
}

func TestPerUserFolderIsolation(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()

	// Create a second user with different Chats content
	user2Dir := filepath.Join(docsDir, utils.UsersDirectory, "user2", "Chats")
	os.MkdirAll(user2Dir, 0755)
	os.WriteFile(filepath.Join(user2Dir, "user2-secret.json"), []byte(`{"secret":true}`), 0644)

	router := setupRouter(docsDir)

	// Default user sees their own Chats
	resp1 := listDocs(t, router, "Chats", "default")
	paths1 := collectFilePaths(resp1.Data)

	if !contains(paths1, "Chats/session1.json") {
		t.Errorf("Default user should see session1.json, got: %v", paths1)
	}
	if contains(paths1, "Chats/user2-secret.json") {
		t.Error("Default user should NOT see user2's files")
	}

	// User2 sees their own Chats
	resp2 := listDocs(t, router, "Chats", "user2")
	paths2 := collectFilePaths(resp2.Data)

	if !contains(paths2, "Chats/user2-secret.json") {
		t.Errorf("User2 should see user2-secret.json, got: %v", paths2)
	}
	if contains(paths2, "Chats/session1.json") {
		t.Error("User2 should NOT see default user's files")
	}
}

func TestSharedFoldersSameForAllUsers(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	router := setupRouter(docsDir)

	resp1 := listDocs(t, router, "skills", "default")
	resp2 := listDocs(t, router, "skills", "user2")

	paths1 := collectFilePaths(resp1.Data)
	paths2 := collectFilePaths(resp2.Data)

	if len(paths1) != len(paths2) {
		t.Errorf("Shared folder should return same content for all users: default=%v, user2=%v", paths1, paths2)
	}

	if !contains(paths1, "shared-skill.json") && !contains(paths1, "skills/shared-skill.json") {
		t.Errorf("skills/ should contain shared-skill.json, got: %v", paths1)
	}
}

func TestNoUserIDFallsToDefault(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	router := setupRouter(docsDir)

	// No X-User-ID header — should fall back to default user
	resp := listDocs(t, router, "Chats", "")
	paths := collectFilePaths(resp.Data)

	found := contains(paths, "session1.json") || contains(paths, "Chats/session1.json")
	if !found {
		t.Errorf("No user ID should fall back to default and see session1.json, got: %v", paths)
	}
}

func TestNormalizeFolderPath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty", "", ""},
		{"root-slash", "/", "/"},
		{"trailing-slash", "Chats/", "Chats"},
		{"multiple-trailing", "Chats///", "Chats"},
		{"no-trailing", "Chats", "Chats"},
		{"nested-trailing", "Chats/sub/", "Chats/sub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeFolderPath(tt.input)
			if got != tt.expect {
				t.Errorf("normalizeFolderPath(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

// PLAT-746-919: real document PUT must keep durable dispatch caller/config
// state owner-only, including replacement of an existing broader-mode file.
func TestDispatchStateDocumentWritesOwnerOnly(t *testing.T) {
	docs, cleanup := setupTestDocsDir(t)
	defer cleanup()
	router := setupRouter(docs)
	router.PUT("/api/documents/*filepath", UpdateDocument)
	dir := filepath.Join(docs, "_users", "default", "chat_history")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"restart-continue.json", "conversation-turn-dispatch.json"} {
		for _, existing := range []bool{false, true} {
			if existing {
				if err := os.Chmod(filepath.Join(dir, name), 0644); err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodPut, "/api/documents/_users/default/chat_history/"+name, strings.NewReader(`{"content":"{}"}`))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusOK && response.Code != http.StatusCreated {
				t.Fatalf("PUT=%d %s", response.Code, response.Body.String())
			}
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("dispatch mode=%o", info.Mode().Perm())
			}
		}
	}
}
