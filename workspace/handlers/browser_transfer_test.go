package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/spf13/viper"
)

func TestExecuteShellBrowserArtifactDestinationIsWorkspaceRelative(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "Downloads"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(security.BrowserArtifactStagingDir(), 0700); err != nil {
		t.Fatal(err)
	}
	staged, err := os.CreateTemp(security.BrowserArtifactStagingDir(), "handler-*.download")
	if err != nil {
		t.Fatal(err)
	}
	stagedPath := staged.Name()
	if _, err := staged.WriteString("download-body"); err != nil {
		t.Fatal(err)
	}
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(stagedPath) })

	oldDocsDir := viper.GetString("docs-dir")
	viper.Set("docs-dir", base)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocsDir) })

	request := models.ExecuteShellRequest{
		Command:          "true",
		WorkingDirectory: "Downloads",
		FolderGuard: &models.FolderGuardConfig{
			Enabled:    true,
			ReadPaths:  []string{"Downloads"},
			WritePaths: []string{"Downloads"},
		},
		ArtifactTransfer: &models.BrowserArtifactTransfer{
			SourcePath:      stagedPath,
			DestinationPath: "Downloads/report.txt",
			Kind:            "download",
			Finalize:        true,
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/execute", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ExecuteShellCommand(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	want := filepath.Join(base, "Downloads", "report.txt")
	got, err := os.ReadFile(want)
	if err != nil || string(got) != "download-body" {
		t.Fatalf("workspace-relative artifact = %q, err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(base, "Downloads", "Downloads", "report.txt")); !os.IsNotExist(err) {
		t.Fatalf("artifact was incorrectly resolved relative to the command working directory: %v", err)
	}
}

// An agent in a project names a browser artifact the way its shell does, relative to the project folder. The workspace-relative
// reading is not writable there, so the folder-relative one is used (otherwise: 'not covered by this session's write paths').
func TestExecuteShellBrowserArtifactDestinationMayBeRelativeToTheProjectFolder(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "proj"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(security.BrowserArtifactStagingDir(), 0700); err != nil {
		t.Fatal(err)
	}
	oldDocsDir := viper.GetString("docs-dir")
	viper.Set("docs-dir", base)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocsDir) })
	gin.SetMode(gin.ReleaseMode)
	finalize := func(destination string) int {
		staged, err := os.CreateTemp(security.BrowserArtifactStagingDir(), "handler-*.download")
		if err != nil {
			t.Fatal(err)
		}
		stagedPath := staged.Name()
		_, _ = staged.WriteString("body")
		_ = staged.Close()
		t.Cleanup(func() { _ = os.Remove(stagedPath) })
		body, err := json.Marshal(models.ExecuteShellRequest{
			Command:          "true",
			WorkingDirectory: "proj",
			FolderGuard:      &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"proj"}, WritePaths: []string{"proj"}},
			ArtifactTransfer: &models.BrowserArtifactTransfer{SourcePath: stagedPath, DestinationPath: destination, Kind: "download", Finalize: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/execute", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ExecuteShellCommand(ctx)
		return recorder.Code
	}
	if code := finalize("shot.txt"); code != http.StatusOK {
		t.Fatalf("a project-folder-relative destination was refused: %d", code)
	}
	if got, err := os.ReadFile(filepath.Join(base, "proj", "shot.txt")); err != nil || string(got) != "body" {
		t.Fatalf("folder-relative artifact = %q, err=%v", got, err)
	}
	// The fallback widens nothing: a destination that leaves the project folder stays refused.
	for _, escape := range []string{"../escape.txt", "../../escape.txt", "sub/../../escape.txt"} {
		if code := finalize(escape); code == http.StatusOK {
			t.Fatalf("%q was accepted", escape)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("an artifact escaped the project folder: %v", err)
	}
}
