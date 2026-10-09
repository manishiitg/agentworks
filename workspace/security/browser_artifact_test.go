package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalizeBrowserArtifactPublishesAuthorizedScreenshot(t *testing.T) {
	base := t.TempDir()
	writePath := filepath.Join(base, "Workflow", "demo", "evidence")
	source := stagedArtifactFile(t, ".png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-data")...))
	destination := filepath.Join(writePath, "login.png")

	err := FinalizeBrowserArtifact(source, destination, "screenshot", base, []string{writePath}, nil, nil)
	if err != nil {
		t.Fatalf("FinalizeBrowserArtifact() error = %v", err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("published artifact missing: %v", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("staged artifact was not removed, stat error = %v", err)
	}
}

func TestBrowserStagingDirectoriesAreDeploymentNamespaced(t *testing.T) {
	t.Setenv(browserStagingNamespaceEnv, "Confida Production")
	t.Cleanup(func() {
		_ = os.RemoveAll(BrowserArtifactStagingDir())
		_ = os.RemoveAll(BrowserUploadStagingDir())
	})
	if got := BrowserArtifactStagingDir(); got != "/tmp/agentworks-browser-artifacts-confida-production" {
		t.Fatalf("artifact staging directory = %q", got)
	}
	if got := BrowserUploadStagingDir(); got != "/tmp/agentworks-browser-uploads-confida-production" {
		t.Fatalf("upload staging directory = %q", got)
	}
}

func TestFinalizeBrowserArtifactRejectsUnauthorizedDestination(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "Workflow", "demo", "evidence")
	source := stagedArtifactFile(t, ".png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-data")...))
	destination := filepath.Join(base, "Workflow", "other", "stolen.png")

	err := FinalizeBrowserArtifact(source, destination, "screenshot", base, []string{allowed}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not covered") {
		t.Fatalf("expected unauthorized destination error, got %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("unauthorized finalization must retain the staged artifact for recovery, stat error = %v", err)
	}
}

func TestFinalizeBrowserArtifactRejectsBlockedWrite(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "Workflow", "demo")
	blocked := filepath.Join(allowed, "planning")
	source := stagedArtifactFile(t, ".png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-data")...))
	destination := filepath.Join(blocked, "login.png")

	err := FinalizeBrowserArtifact(source, destination, "screenshot", base, []string{allowed}, nil, []string{blocked})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked destination error, got %v", err)
	}
}

func TestFinalizeBrowserArtifactRejectsSourceOutsideManagedStaging(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "not-managed.png")
	if err := os.WriteFile(source, append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-data")...), 0600); err != nil {
		t.Fatal(err)
	}

	err := FinalizeBrowserArtifact(source, filepath.Join(base, "out.png"), "screenshot", base, []string{base}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "managed staging") {
		t.Fatalf("expected managed staging error, got %v", err)
	}
}

func TestFinalizeBrowserArtifactRejectsSymlinkEscapeFromWritePath(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "evidence")
	outside := t.TempDir()
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(allowed, "escape")); err != nil {
		t.Fatal(err)
	}
	source := stagedArtifactFile(t, ".png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-data")...))

	err := FinalizeBrowserArtifact(source, filepath.Join(allowed, "escape", "login.png"), "screenshot", base, []string{allowed}, nil, nil)
	if err == nil || (!strings.Contains(err.Error(), "inside workspace") && !strings.Contains(err.Error(), "not covered")) {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestFinalizeBrowserArtifactPublishesWebM(t *testing.T) {
	base := t.TempDir()
	source := stagedArtifactFile(t, ".webm", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("webm-data")...))
	destination := filepath.Join(base, "evidence", "run.webm")

	if err := FinalizeBrowserArtifact(source, destination, "video", base, []string{"evidence"}, nil, nil); err != nil {
		t.Fatalf("FinalizeBrowserArtifact() error = %v", err)
	}
}

func TestFinalizeBrowserArtifactPublishesGenericDownload(t *testing.T) {
	base := t.TempDir()
	source := stagedArtifactFile(t, ".download", []byte("downloaded-report-data"))
	destination := filepath.Join(base, "Downloads", "report.csv")

	if err := FinalizeBrowserArtifact(source, destination, "download", base, []string{"Downloads"}, nil, nil); err != nil {
		t.Fatalf("FinalizeBrowserArtifact() error = %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != "downloaded-report-data" {
		t.Fatalf("downloaded artifact = %q, err=%v", got, err)
	}
}

func stagedArtifactFile(t *testing.T, extension string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(BrowserArtifactStagingDir(), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(BrowserArtifactStagingDir(), "test-*"+extension)
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	t.Cleanup(func() { _ = os.Remove(name) })
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

// server A 2026-10-07 (PLAT-709): a recording saved into a slot user's project was
// 0600 for the server account, so the user's own chat could not read it.
func TestFinalizeBrowserArtifactKeepsTheProjectGroupsAccess(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "Chats", "Code", "projects", "p1")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(project, 0o2770); err != nil {
		t.Fatal(err)
	}
	source := stagedArtifactFile(t, ".png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-data")...))
	destination := filepath.Join(project, "recordings", "take.png")
	if err := FinalizeBrowserArtifact(source, destination, "screenshot", base, []string{project}, nil, nil); err != nil {
		t.Fatalf("FinalizeBrowserArtifact() error = %v", err)
	}
	dir, _ := os.Stat(filepath.Dir(destination))
	file, _ := os.Stat(destination)
	if got := dir.Mode().Perm(); got != 0o770 {
		t.Fatalf("new folder mode = %v, want the project's 0770", got)
	}
	if got := file.Mode().Perm(); got != 0o660 {
		t.Fatalf("artifact mode = %v, want 0660 so the project group can read it", got)
	}
}
