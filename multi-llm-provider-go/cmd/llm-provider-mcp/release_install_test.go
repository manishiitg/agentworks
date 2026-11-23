package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerPreparesMacOSBinaryBeforeSetup(t *testing.T) {
	script := readReleaseFile(t, "scripts", "install-mcp.sh")

	for _, required := range []string{
		"prepare_macos_binary()",
		"code object is not signed at all",
		"codesign --force --sign - \"$BINARY_PATH\"",
		"codesign --verify --strict \"$BINARY_PATH\"",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("installer is missing %q", required)
		}
	}

	prepareCall := strings.LastIndex(script, "\nprepare_macos_binary\n")
	setupArgs := strings.Index(script, "\nset -- setup --binary")
	if prepareCall < 0 || setupArgs < 0 || prepareCall > setupArgs {
		t.Fatal("installer must prepare the macOS signature before launching setup")
	}
}

func TestReleaseWorkflowSignsDarwinArtifactsOnMacOS(t *testing.T) {
	workflow := readReleaseFile(t, ".github", "workflows", "release-mcp.yml")

	for _, required := range []string{
		"runs-on: ${{ matrix.runner }}",
		"runner: macos-latest",
		"codesign --force --sign - \"dist/${asset}/llm-provider-mcp\"",
		"codesign --verify --strict --verbose=2 \"dist/${asset}/llm-provider-mcp\"",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("release workflow is missing %q", required)
		}
	}
}

func readReleaseFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
