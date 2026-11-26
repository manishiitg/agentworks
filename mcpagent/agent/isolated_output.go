package mcpagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const isolatedOutputInstructions = `

## Step output directory
Native file tools and shell commands start in this invocation's STEP_OUTPUT_DIR.
Save deliverable files there directly; no copy or sync is needed.
Keep generated instructions, skills, CLI configuration and scratch files in the
private runtime.
AgentWorks bridge paths and STEP_OUTPUT_DIR remain unchanged. Read inputs through
the existing admitted paths/tools.
`

func withCodingAgentOutputDir(dir string) agentOption {
	return func(a *Agent) { a.codingAgentOutputDir = strings.TrimSpace(dir) }
}

// Revalidate before every launch/continuation. A wrong target or an obstructing
// file is an error, never a reason to overwrite content or use the real cwd.
func (a *Agent) prepareIsolatedOutputLink() error {
	if a.codingAgentOutputDir == "" {
		return nil
	}
	if !a.isolatedSessionWorkspace || !filepath.IsAbs(a.codingAgentOutputDir) {
		return fmt.Errorf("output link requires an isolated runtime and an absolute output directory")
	}
	target, err := filepath.EvalSymlinks(a.codingAgentOutputDir)
	if err != nil {
		return fmt.Errorf("resolve step output link target: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("step output link target is not a directory: %q", target)
	}
	// Linked runtimes require the stable session directory. Do not accept the
	// old isolation helper's random-dir/real-cwd fallback after a state error.
	if stable := isolatedWorkspaceDirForSession(a.sessionID); stable != "" {
		if err := os.MkdirAll(stable, 0o700); err != nil {
			return fmt.Errorf("create linked step runtime: %w", err)
		}
	}
	runtime := a.ensureIsolatedWorkspaceDir()
	if runtime == "" {
		return fmt.Errorf("create linked step runtime: isolation unavailable")
	}
	if stable := isolatedWorkspaceDirForSession(a.sessionID); stable != "" && runtime != stable {
		return fmt.Errorf("linked step runtime does not match its session directory")
	}
	if info, err := os.Lstat(runtime); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("linked step runtime is not a private directory: %q", runtime)
	}
	if a.codingAgentRuntimeDir == "" {
		a.codingAgentRuntimeDir = runtime
	}
	if rel, err := filepath.Rel(runtime, target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("step output must persist outside its private runtime")
	}
	link := filepath.Join(runtime, "output")
	if err := os.Symlink(target, link); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create step output link: %w", err)
	}
	actual, err := os.Readlink(link)
	if err != nil || actual != target {
		return fmt.Errorf("step output link %q does not point to its admitted directory %q", link, target)
	}
	return nil
}
