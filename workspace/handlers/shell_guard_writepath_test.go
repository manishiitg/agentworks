package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestResolveGuardWritePath(t *testing.T) {
	docsDir := t.TempDir()
	outsideDir := t.TempDir()
	insideDir := filepath.Join(docsDir, "Workflow", "allowed")
	if err := os.MkdirAll(insideDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Symlink inside the boundary pointing outside it.
	escapeLink := filepath.Join(docsDir, "escape")
	if err := os.Symlink(outsideDir, escapeLink); err != nil {
		t.Fatal(err)
	}
	// Symlink inside the boundary pointing inside it.
	innerLink := filepath.Join(docsDir, "inner")
	if err := os.Symlink(insideDir, innerLink); err != nil {
		t.Fatal(err)
	}

	valid := []string{
		"Workflow/allowed",
		"Workflow/allowed/",
		"Workflow/new-nested/deep",
		insideDir, // absolute, inside the boundary
		filepath.Join(innerLink, "sub"),
	}
	for _, wp := range valid {
		if _, err := resolveGuardWritePath(wp, docsDir); err != nil {
			t.Errorf("resolveGuardWritePath(%q) = %v, want nil", wp, err)
		}
	}

	invalid := []string{
		outsideDir,                          // absolute, outside the boundary
		filepath.Join(docsDir, "..", "evil"), // .. escape
		"../evil",
		"Workflow/../../evil",
		filepath.Join(escapeLink, "sub"), // symlink redirect outside
	}
	for _, wp := range invalid {
		if got, err := resolveGuardWritePath(wp, docsDir); err == nil {
			t.Errorf("resolveGuardWritePath(%q) = %q, want rejection", wp, got)
		}
	}
}

// An escaping write path must fail the request before anything is created:
// the pre-create MkdirAll must never run on it.
func TestExecuteShellRejectsEscapingWritePath(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	outside := t.TempDir()
	target := filepath.Join(outside, "m1-pwn")

	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	r := gin.New()
	r.POST("/api/execute", ExecuteShellCommand)

	body := `{"command":"echo hi","folder_guard":{"enabled":true,"write_paths":["` + target + `"]}}`
	req, _ := http.NewRequest("POST", "/api/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "default")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("escaping write path was created on disk: %v", err)
	}
}

// A folder the person granted outside the workspace (Downloads, a project
// folder) is accepted when it already exists, and is never created; everything
// the boundary check exists to stop is still refused.
func TestIsExistingHostGrant(t *testing.T) {
	docsDir := t.TempDir()
	granted := t.TempDir() // outside docsDir, exists
	file := filepath.Join(granted, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	escapeLink := filepath.Join(docsDir, "escape")
	if err := os.Symlink(granted, escapeLink); err != nil {
		t.Fatal(err)
	}

	origOS := interactiveShellHostOS
	t.Cleanup(func() { interactiveShellHostOS = origOS })
	interactiveShellHostOS = "darwin"
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENTWORKS_SLOTS", "")
	t.Setenv("MULTI_USER_MODE", "")
	for _, wp := range []string{granted, granted + "/"} {
		if !isExistingHostGrant(wp, docsDir) {
			t.Errorf("isExistingHostGrant(%q) = false, want true", wp)
		}
	}
	for name, wp := range map[string]string{
		"missing outside path":         filepath.Join(granted, "does-not-exist"),
		"a file, not a directory":      file,
		"relative path":                "Downloads",
		".. escape":                    filepath.Join(granted, "..", "evil"),
		"symlink redirect in the docs": filepath.Join(escapeLink, "sub"),
		"inside the workspace":         docsDir,
	} {
		if isExistingHostGrant(wp, docsDir) {
			t.Errorf("%s: isExistingHostGrant(%q) = true, want false", name, wp)
		}
	}
	// A server never passes an outside folder through, however it was named.
	t.Setenv("NATIVE_WORKSPACE", "false")
	if isExistingHostGrant(granted, docsDir) {
		t.Error("accepted an outside folder outside native mode")
	}
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENTWORKS_SLOTS", "on")
	if isExistingHostGrant(granted, docsDir) {
		t.Error("accepted an outside folder with per-user accounts on")
	}
	t.Setenv("AGENTWORKS_SLOTS", "")
	interactiveShellHostOS = "linux"
	if isExistingHostGrant(granted, docsDir) {
		t.Error("accepted an outside folder on a Linux server in native mode")
	}
	interactiveShellHostOS = "darwin"
	t.Setenv("MULTI_USER_MODE", "true")
	if isExistingHostGrant(granted, docsDir) {
		t.Error("accepted an outside folder on a multi-user server")
	}
}
