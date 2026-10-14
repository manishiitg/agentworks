package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An agent is "behind" only when it said its build and that build differs from the one the server offers.
func TestAgentUpdateNeededComparesTheAttachedBuildWithTheOfferedOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "version.json"), []byte(`{"version":"v","cli_build":"build-2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := cliDownloadsDirOverride
	cliDownloadsDirOverride = dir
	servedCLIBuild.at = time.Time{}
	t.Cleanup(func() { cliDownloadsDirOverride = old; servedCLIBuild.at = time.Time{} })
	with := func(build string) namedMessageAgent {
		return namedMessageAgent{Attachment: &namedAgentAttachment{ClientBuild: build}}
	}
	for build, want := range map[string]bool{"build-1": true, "build-2": false, "dev": false, "": false} {
		if got := agentUpdateNeeded(with(build)); got != want {
			t.Fatalf("build %q: update_needed=%v, want %v", build, got, want)
		}
	}
	if agentUpdateNeeded(namedMessageAgent{}) {
		t.Fatal("an agent with no attachment cannot be behind")
	}
	if cleanClientBuild("x y") != "" || cleanClientBuild("build-2") != "build-2" {
		t.Fatal("client build text is not validated")
	}
}
