package security

import (
	"bytes"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
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
	t.Setenv(browserStagingNamespaceEnv, "customer Production")
	t.Cleanup(func() {
		_ = os.RemoveAll(BrowserArtifactStagingDir())
		_ = os.RemoveAll(BrowserUploadStagingDir())
	})
	if got := BrowserArtifactStagingDir(); got != "/tmp/agentworks-browser-artifacts-customer-production" {
		t.Fatalf("artifact staging directory = %q", got)
	}
	if got := BrowserUploadStagingDir(); got != "/tmp/agentworks-browser-uploads-customer-production" {
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

// PLAT-691: both advertised recording formats must pass the same scoped
// finalizer. A non-video file still cannot be published as a recording.
func TestFinalizeBrowserArtifactPublishesRecordingFormats(t *testing.T) {
	for _, tc := range []struct {
		name, ext string
		data      []byte
		allowed   bool
	}{
		{"webm", ".webm", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("webm-data")...), true},
		{"mp4", ".mp4", append([]byte{0, 0, 0, 24}, []byte("ftypisom0000mp41")...), true},
		{"non_video", ".webm", []byte("this is not video data"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			source := stagedArtifactFile(t, tc.ext, tc.data)
			destination := filepath.Join(base, "evidence", "run"+tc.ext)
			err := FinalizeBrowserArtifact(source, destination, "video", base, []string{"evidence"}, nil, nil)
			if (err == nil) != tc.allowed {
				t.Fatalf("format allowed=%t, error=%v", tc.allowed, err)
			}
		})
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
	if os.Getenv(browserStagingNamespaceEnv) == "" {
		t.Setenv(browserStagingNamespaceEnv, filepath.Base(t.TempDir()))
		root := BrowserArtifactStagingDir()
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
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

// PLAT-856: browser outputs and Files must resolve a selected local folder identically.
func TestBrowserArtifactsUseBoundCrewFolderAndRetainWriteGuards(t *testing.T) {
	base := t.TempDir()
	docs, project, state := filepath.Join(base, "docs"), filepath.Join(base, "project"), filepath.Join(base, "state")
	for _, dir := range []string{filepath.Join(docs, "Crew", "example"), filepath.Join(project, ".agentworks"), filepath.Join(docs, "_system", "crew-data", "id"), filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	docs, _ = filepath.EvalSymlinks(docs)
	project, _ = filepath.EvalSymlinks(project)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", Owner: "owner", ID: "id", LocalRoot: project}}})
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1}
	for _, tc := range []struct {
		name, kind string
		data       []byte
	}{{"shot.png", "screenshot", png}, {"video.webm", "video", []byte{0x1a, 0x45, 0xdf, 0xa3, 1}}} {
		virtual := "Crew/example/_files/evidence/" + tc.name
		for _, dest := range []string{virtual, filepath.Join(docs, virtual), filepath.Join(project, "evidence", tc.name)} {
			if !ArtifactDestinationWritable(dest, docs, []string{"Crew/example/_files"}) {
				t.Fatalf("not writable: %s", dest)
			}
			source := stagedArtifactFile(t, filepath.Ext(tc.name), tc.data)
			if err := FinalizeBrowserArtifact(source, dest, tc.kind, docs, []string{"Crew/example/_files"}, nil, nil); err != nil {
				t.Fatal(err)
			}
			readPath, err := utils.ResolveUserPath(docs, virtual, "owner")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(readPath)
			if err != nil || !bytes.Equal(data, tc.data) || readPath != filepath.Join(project, "evidence", tc.name) {
				t.Fatalf("Files read %s: %v", readPath, err)
			}
		}
		if _, err := os.Stat(filepath.Join(docs, virtual)); !os.IsNotExist(err) {
			t.Fatalf("shadow artifact exists: %v", err)
		}
	}
	for _, tc := range []struct {
		dest             string
		allowed, blocked []string
	}{
		{"Crew/example/_files/denied.png", []string{"Crew/other/_files"}, nil},
		{"Crew/example/_files/denied.png", []string{"Crew/example/_files"}, []string{"Crew/example/_files/denied.png"}},
		{filepath.Join(base, "outside.png"), []string{base}, nil},
	} {
		source := stagedArtifactFile(t, ".png", png)
		if err := FinalizeBrowserArtifact(source, tc.dest, "screenshot", docs, tc.allowed, nil, tc.blocked); err == nil {
			t.Fatalf("unauthorized destination accepted: %s", tc.dest)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	source := stagedArtifactFile(t, ".png", png)
	if err := FinalizeBrowserArtifact(source, "Crew/example/_files/escape/out.png", "screenshot", docs, []string{"Crew/example/_files"}, nil, nil); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

// PLAT-856-525: the host Downloads exception is local-only and needs the exact
// current grant; ordinary absolute paths and symlink escapes remain refused.
func TestBrowserArtifactMacDownloadsRequiresCurrentGrant(t *testing.T) {
	oldOS := browserArtifactHostOS
	browserArtifactHostOS = "darwin"
	t.Cleanup(func() { browserArtifactHostOS = oldOS })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	root := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	source := func() string {
		return stagedArtifactFile(t, ".png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image")...))
	}
	dest := filepath.Join(root, "page.png")
	if err := FinalizeBrowserArtifact(source(), dest, "screenshot", base, []string{root}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
	if ArtifactDestinationWritable(dest, base, []string{base}) {
		t.Fatal("missing Downloads grant accepted")
	}
	if err := FinalizeBrowserArtifact(source(), dest, "screenshot", base, []string{root}, nil, []string{root}); err == nil {
		t.Fatal("blocked Downloads accepted")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if ArtifactDestinationWritable(filepath.Join(root, "escape", "page.png"), base, []string{root}) {
		t.Fatal("symlink escape accepted")
	}
	t.Setenv("MULTI_USER_MODE", "true")
	if ArtifactDestinationWritable(dest, base, []string{root}) {
		t.Fatal("server Downloads accepted")
	}
	t.Setenv("MULTI_USER_MODE", "false")
	browserArtifactHostOS = "linux"
	if ArtifactDestinationWritable(dest, base, []string{root}) {
		t.Fatal("Linux Downloads accepted")
	}
}
