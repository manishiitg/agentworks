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
		outsideDir,                           // absolute, outside the boundary
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

func TestGuardWritePathToCreatePreservesExistingHostGrants(t *testing.T) {
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("NATIVE_WORKSPACE", "true")
	docsDir, hostDir := t.TempDir(), t.TempDir()
	got, err := guardWritePathToCreate(hostDir, docsDir)
	if err != nil || got != "" {
		t.Fatalf("existing host grant must not be created: path=%q err=%v", got, err)
	}
	inside := filepath.Join(docsDir, "project", "new")
	if got, err := guardWritePathToCreate("project/new", docsDir); err != nil || got != inside {
		t.Fatalf("workspace directory should be prepared: path=%q err=%v", got, err)
	}
	file := filepath.Join(hostDir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(docsDir, "escape")
	if err := os.Symlink(hostDir, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(hostDir, "missing"), file, link, "escape", "../outside", hostDir + "/../" + filepath.Base(hostDir)} {
		if _, err := guardWritePathToCreate(path, docsDir); err == nil {
			t.Errorf("unsafe or missing grant accepted: %s", path)
		}
	}
}

func TestGuardWritePathToCreateRejectsHostGrantsOnServer(t *testing.T) {
	docsDir, hostDir := t.TempDir(), t.TempDir()
	for _, mode := range []struct{ name, local, multi, native string }{
		{"defaults", "", "", ""},
		{"native server", "false", "true", "true"},
		{"multi-user local flag", "true", "true", "true"},
		{"missing single-user mode", "true", "", "true"},
		{"container workspace", "true", "false", "false"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("LOCAL_MODE", mode.local)
			t.Setenv("MULTI_USER_MODE", mode.multi)
			t.Setenv("NATIVE_WORKSPACE", mode.native)
			if _, err := guardWritePathToCreate(hostDir, docsDir); err == nil {
				t.Fatal("existing host directory accepted outside local native single-user mode")
			}
			if _, err := guardWritePathToCreate("project/new", docsDir); err != nil {
				t.Fatalf("workspace path rejected: %v", err)
			}
		})
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
