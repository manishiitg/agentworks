package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeMCPToolsFileForTest stores a tool list the way the launcher does and
// returns the path for MCP_TOOLS_FILE.
func writeMCPToolsFileForTest(t *testing.T, toolsJSON []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, toolsJSON, 0o600); err != nil {
		t.Fatalf("write tool list file: %v", err)
	}
	return path
}

func buildPulseTestMCPBridge(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	// Building a dependency directly from the module cache does not resolve its
	// local replace directives relative to this repository. Use the same
	// provider module selected by agent_go in a temporary workspace instead.
	resolveModule := func(name string) string {
		t.Helper()
		cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", name)
		cmd.Dir = moduleRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("resolve %s module: %v\n%s", name, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	moduleDir := resolveModule("github.com/manishiitg/mcpagent")
	providerDir := resolveModule("github.com/manishiitg/multi-llm-provider-go")
	workDir := t.TempDir()
	initWork := exec.Command("go", "work", "init", moduleDir)
	initWork.Dir = workDir
	if output, err := initWork.CombinedOutput(); err != nil {
		t.Fatalf("init mcpbridge workspace: %v\n%s", err, output)
	}
	workFile := filepath.Join(workDir, "go.work")
	workData, err := os.ReadFile(workFile)
	if err != nil {
		t.Fatalf("read mcpbridge workspace: %v", err)
	}
	// go work edit -replace parses @ in a module-cache directory as a version
	// separator. A quoted directory in the workspace file preserves the path.
	workData = append(workData, []byte(fmt.Sprintf("\nreplace github.com/manishiitg/multi-llm-provider-go => %q\n", providerDir))...)
	if err := os.WriteFile(workFile, workData, 0600); err != nil {
		t.Fatalf("replace mcpbridge provider module: %v", err)
	}
	source := filepath.Join(moduleDir, "cmd", "mcpbridge")
	binary := filepath.Join(workDir, "mcpbridge")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = source
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(workDir, "go.work"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mcpbridge: %v\n%s", err, output)
	}
	return binary
}
