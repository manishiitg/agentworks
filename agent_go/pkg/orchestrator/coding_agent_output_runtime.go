package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Prepare only explicit execution outputs, never an inherited parent-session
// STEP_OUTPUT_DIR on a review/learning agent. The final dedicated session guard
// is the authority, including nested/message-sequence output overrides.
func prepareCodingAgentOutputRuntime(config *agents.OrchestratorAgentConfig) error {
	if config == nil || strings.TrimSpace(config.CodingAgentOutputDir) == "" {
		return nil
	}
	if !config.IsolateCodingAgentWorkspace || !common.IsCLIProvider(config.LLMConfig.Primary.Provider) {
		return fmt.Errorf("step output link requires an isolated coding-agent runtime")
	}
	cfg := common.GetSessionShellConfig(config.MCPSessionID)
	if cfg == nil || cfg.Env["STEP_OUTPUT_DIR"] != config.CodingAgentOutputDir {
		return fmt.Errorf("step output link does not match its dedicated session output")
	}
	output, err := canonicalRuntimePath(config.CodingAgentOutputDir)
	if err != nil {
		return err
	}
	policy := llmtypes.CLISecurityPolicy{}
	if config.CLISecurityPolicy != nil {
		policy = config.CLISecurityPolicy.Clone()
	}
	// Never inherit the chat's workflow-wide write grant. Keep independently
	// admitted host capabilities and enforcement mode, and use this step's
	// existing workspace grants (DB/cache/KB/owning subtree included).
	policy.WorkspaceReadPaths = nil
	policy.WorkspaceWritePaths = nil
	for _, paths := range []struct {
		source []string
		target *[]string
	}{{cfg.ReadPaths, &policy.WorkspaceReadPaths}, {cfg.WritePaths, &policy.WorkspaceWritePaths}} {
		for _, path := range paths.source {
			resolved, err := canonicalRuntimePath(resolveCodingAgentWorkingDir(path))
			if err != nil {
				return fmt.Errorf("resolve step CLI grant %q: %w", path, err)
			}
			*paths.target = append(*paths.target, resolved)
		}
	}
	allowed := false
	for _, root := range policy.WorkspaceWritePaths {
		rel, err := filepath.Rel(root, output)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("step output link target %q is outside the step's write grants", output)
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		return fmt.Errorf("create step output directory: %w", err)
	}
	config.CodingAgentOutputDir = output
	config.CLISecurityPolicy = &policy
	return nil
}

// Resolve existing ancestors too: a missing nested output below a symlink must
// be checked against the real write root before creating the directory.
func canonicalRuntimePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("runtime path must be absolute: %q", path)
	}
	path = filepath.Clean(path)
	ancestor := path
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			suffix, _ := filepath.Rel(ancestor, path)
			return filepath.Join(resolved, suffix), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("no existing ancestor for runtime path %q", path)
		}
		ancestor = parent
	}
}
