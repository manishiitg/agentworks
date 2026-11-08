package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/spf13/viper"
)

func TestResolveGuardWritePath(t *testing.T) {
	docsDir := t.TempDir()
	docsDir, err := filepath.EvalSymlinks(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
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
	// PLAT-746: the first browser command of a moved workflow recreated its
	// retired folders while preparing shell write grants. Kernel grants already
	// resolve the Crew binding; directory creation must use the same root.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	for _, dir := range []string{filepath.Join(docsDir, "Crew/example/.agentworks"), filepath.Join(docsDir, crewroot.PrivateDirectory, "crew"), filepath.Join(state, "ownership/crew-executables")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(filepath.Join(state, "ownership/projects.json"), map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", ID: "crew", Owner: "owner"}}})
	binding := crewroot.ExecutableBinding{ID: "sequence", Root: "Workflow/sequence", CrewID: "crew", CrewRoot: "Crew/example", Kind: "workflow", RuntimeRoot: "workflows/sequence"}
	writeJSON(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), binding)
	logical := binding.Root + "/runs/run-one/execution/step-output"
	physical, err := resolveGuardWritePath(logical, docsDir)
	want := filepath.Join(docsDir, crewroot.PrivateDirectory, "crew/workflows/sequence/runs/run-one/execution/step-output")
	if err != nil || physical != want {
		t.Fatalf("moved write grant: %q %v, want %q", physical, err, want)
	}
	if err := prepareGuardWriteDirectory(physical, docsDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(docsDir, binding.Root)); !os.IsNotExist(err) {
		t.Fatal("write grant preparation recreated the retired physical workflow")
	}
}

func TestGuardWritePathToCreatePreservesExistingHostGrants(t *testing.T) {
	// PLAT-746-369: this allowance is for a native Mac. A Linux verifier must
	// exercise the same production platform branch instead of assuming its host.
	originalOS := interactiveShellHostOS
	t.Cleanup(func() { interactiveShellHostOS = originalOS })
	interactiveShellHostOS = "darwin"
	t.Setenv("AGENTWORKS_SLOTS", "")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
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
	originalOS := interactiveShellHostOS
	t.Cleanup(func() { interactiveShellHostOS = originalOS })
	docsDir, hostDir := t.TempDir(), t.TempDir()
	for _, mode := range []struct{ name, hostOS, local, multi, native, slots string }{
		{"defaults", "darwin", "", "", "", ""},
		{"Linux even with local native flags", "linux", "local", "false", "true", ""},
		{"Mac without native mode", "darwin", "local", "false", "false", ""},
		{"Mac outside local deployment", "darwin", "", "false", "true", ""},
		{"multi-user Mac", "darwin", "local", "true", "true", ""},
		{"missing explicit single-user flag", "darwin", "local", "", "true", ""},
		{"Mac with per-user accounts", "darwin", "local", "false", "true", "on"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			interactiveShellHostOS = mode.hostOS
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", mode.local)
			t.Setenv("MULTI_USER_MODE", mode.multi)
			t.Setenv("NATIVE_WORKSPACE", mode.native)
			t.Setenv("AGENTWORKS_SLOTS", mode.slots)
			if _, err := guardWritePathToCreate(hostDir, docsDir); err == nil {
				t.Fatal("existing host directory accepted outside native single-user Mac")
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
