package browserconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxSocketDirIsTheSharedTmpCopyOfTheManagedFolder(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	session := "project-1f505da0c06ad9bd--browser"
	want := filepath.Join(docs, "tmp", ".agent-browser", "o", "p1f505da0c06ad9bd")
	if got := SandboxSocketDir(session); got != want {
		t.Fatalf("SandboxSocketDir = %q, want %q", got, want)
	}
	if err := os.MkdirAll(want, 0o700); err != nil {
		t.Fatal(err)
	}
	if dirs := SandboxSocketDirs(); len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("SandboxSocketDirs = %v", dirs)
	}
	if SandboxSocketDir("shared-browser") != "" {
		t.Fatal("an unmanaged session has no per-owner folder")
	}
	t.Setenv("WORKSPACE_DOCS_PATH", "")
	t.Setenv("DOCS_DIR", "")
	if SandboxSocketDir(session) != "" || SandboxSocketDirs() != nil {
		t.Fatal("without a docs folder there is nothing to look in")
	}
}

func TestManagedSocketParentsAreDeploymentScoped(t *testing.T) {
	const session = "example--workflow-0123456789abcdef--browser"
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", "deployment-a")
	a := SocketDirForSession(session)
	if len(filepath.Join(a, session+".sock")) > 103 {
		t.Fatalf("managed socket path is too long: %s", a)
	}
	if got := ArtifactDirForSession(session); got != filepath.Join(a, "artifacts") {
		t.Fatalf("artifact root %s does not match socket scope %s", got, a)
	}
	if err := os.MkdirAll(SandboxSocketDir(session), 0o700); err != nil {
		t.Fatal(err)
	}
	if dirs := SandboxSocketDirs(); len(dirs) != 1 || dirs[0] != SandboxSocketDir(session) {
		t.Fatalf("scoped sandbox discovery: %v", dirs)
	}
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", "deployment-b")
	if b := SocketDirForSession(session); filepath.Dir(a) == filepath.Dir(b) {
		t.Fatalf("two deployments share a private parent: %s", b)
	}
	if dirs := SandboxSocketDirs(); len(dirs) != 0 {
		t.Fatalf("another deployment's sandbox folders were discovered: %v", dirs)
	}
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", "")
	if got := SocketDirForSession(session); got != filepath.Join(SocketRoot, "o", "w0123456789abcdef") {
		t.Fatalf("unconfigured local socket path changed: %s", got)
	}
}
