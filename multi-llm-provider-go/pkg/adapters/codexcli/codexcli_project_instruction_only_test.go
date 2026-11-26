package codexcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// codexArgsHaveModelInstructionsFile reports whether the args carry a
// `-c model_instructions_file="..."` override (the CLI-side system-prompt
// injection on the interactive path).
func codexArgsHaveModelInstructionsFile(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" && strings.HasPrefix(args[i+1], "model_instructions_file=") {
			return true
		}
	}
	return false
}

// TestCodexInteractiveProjectInstructionOnly covers WithProjectInstructionOnly
// on the interactive (tmux) path:
//   - default: the prompt is injected via -c model_instructions_file AND also
//     projected into <workingDir>/AGENTS.md;
//   - flag on with a working dir: the -c model_instructions_file injection is
//     dropped and AGENTS.md becomes the sole carrier;
//   - flag on but the AGENTS.md projection cannot run (write-project-instruction
//     disabled): the adapter falls back to -c model_instructions_file so the
//     prompt is never silently dropped.
//
// buildCodexInteractiveArgs builds args and performs the projection without
// spawning the real `codex` binary, so this is exercised directly.
func TestCodexInteractiveProjectInstructionOnly(t *testing.T) {
	const promptBody = "ORCHESTRATOR SYSTEM PROMPT BODY"

	t.Run("default: -c model_instructions_file present and AGENTS.md also written", func(t *testing.T) {
		adapter := NewCodexCLIAdapter("", "gpt-5.5", &MockLogger{})
		dir := t.TempDir()
		opts := &llmtypes.CallOptions{}
		WithProjectDirID(dir)(opts)

		args, systemPromptFile, cleanup, err := adapter.buildCodexInteractiveArgs(opts, promptBody)
		if err != nil {
			t.Fatalf("buildCodexInteractiveArgs error = %v", err)
		}
		if systemPromptFile != "" {
			defer os.Remove(systemPromptFile)
		}
		if cleanup != nil {
			defer cleanup()
		}

		if !codexArgsHaveModelInstructionsFile(args) {
			t.Fatalf("default mode must pass -c model_instructions_file, args=%v", args)
		}
		body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatalf("default mode should also write AGENTS.md: %v", err)
		}
		if !strings.Contains(string(body), promptBody) {
			t.Fatalf("AGENTS.md missing prompt body, got %q", string(body))
		}
	})

	t.Run("project-instruction-only: drops -c model_instructions_file, keeps AGENTS.md", func(t *testing.T) {
		adapter := NewCodexCLIAdapter("", "gpt-5.5", &MockLogger{})
		dir := t.TempDir()
		opts := &llmtypes.CallOptions{}
		WithProjectDirID(dir)(opts)
		WithProjectInstructionOnly(true)(opts)

		args, systemPromptFile, cleanup, err := adapter.buildCodexInteractiveArgs(opts, promptBody)
		if err != nil {
			t.Fatalf("buildCodexInteractiveArgs error = %v", err)
		}
		if systemPromptFile != "" {
			defer os.Remove(systemPromptFile)
		}
		if cleanup != nil {
			defer cleanup()
		}

		if codexArgsHaveModelInstructionsFile(args) {
			t.Fatalf("project-instruction-only must NOT pass -c model_instructions_file, args=%v", args)
		}
		if systemPromptFile != "" {
			t.Fatalf("project-instruction-only must not write a CLI system-prompt temp file, got %q", systemPromptFile)
		}
		body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatalf("project-instruction-only must still write AGENTS.md: %v", err)
		}
		if !strings.Contains(string(body), promptBody) {
			t.Fatalf("AGENTS.md missing prompt body, got %q", string(body))
		}
	})

	t.Run("project-instruction-only with projection disabled: falls back to -c model_instructions_file", func(t *testing.T) {
		adapter := NewCodexCLIAdapter("", "gpt-5.5", &MockLogger{})
		dir := t.TempDir()
		opts := &llmtypes.CallOptions{}
		WithProjectDirID(dir)(opts)
		WithProjectInstructionOnly(true)(opts)
		// Disable the AGENTS.md projection so projectedToInstructionFile stays
		// false; the flag must not strand the session without a prompt.
		WithWriteProjectInstructionFile(false)(opts)

		args, systemPromptFile, cleanup, err := adapter.buildCodexInteractiveArgs(opts, promptBody)
		if err != nil {
			t.Fatalf("buildCodexInteractiveArgs error = %v", err)
		}
		if systemPromptFile != "" {
			defer os.Remove(systemPromptFile)
		}
		if cleanup != nil {
			defer cleanup()
		}

		if !codexArgsHaveModelInstructionsFile(args) {
			t.Fatalf("must fall back to -c model_instructions_file when AGENTS.md projection is skipped, args=%v", args)
		}
		if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
			t.Fatalf("AGENTS.md must NOT be written when write-project-instruction is disabled (err=%v)", err)
		}
	})

	t.Run("project-instruction-only off explicitly behaves like default", func(t *testing.T) {
		adapter := NewCodexCLIAdapter("", "gpt-5.5", &MockLogger{})
		dir := t.TempDir()
		opts := &llmtypes.CallOptions{}
		WithProjectDirID(dir)(opts)
		WithProjectInstructionOnly(false)(opts)

		args, systemPromptFile, cleanup, err := adapter.buildCodexInteractiveArgs(opts, promptBody)
		if err != nil {
			t.Fatalf("buildCodexInteractiveArgs error = %v", err)
		}
		if systemPromptFile != "" {
			defer os.Remove(systemPromptFile)
		}
		if cleanup != nil {
			defer cleanup()
		}

		if !codexArgsHaveModelInstructionsFile(args) {
			t.Fatalf("explicit false must keep -c model_instructions_file, args=%v", args)
		}
	})
}
