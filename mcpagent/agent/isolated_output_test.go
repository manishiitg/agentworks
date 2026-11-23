package mcpagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func newLinkedOutputTestAgent(t *testing.T, output string) *Agent {
	t.Helper()
	a := &Agent{sessionID: t.Name() + t.TempDir(), isolatedSessionWorkspace: true, codingAgentOutputDir: output}
	t.Cleanup(func() { CloseSession(a.sessionID) })
	return a
}

func TestIsolatedOutputPersistsAcrossResumeAndSessionCleanup(t *testing.T) {
	output := t.TempDir()
	a := newLinkedOutputTestAgent(t, output)
	if err := a.prepareIsolatedOutputLink(); err != nil {
		t.Fatal(err)
	}
	runtime := a.isolatedWorkspacePath
	file := filepath.Join(runtime, "output", "draft.json")
	if err := os.WriteFile(file, []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, filepath.Join(runtime, "output", "result.json")); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	resumed := &Agent{sessionID: a.sessionID, isolatedSessionWorkspace: true, codingAgentOutputDir: output}
	if err := resumed.prepareIsolatedOutputLink(); err != nil {
		t.Fatal(err)
	}
	if resumed.isolatedWorkspacePath != runtime {
		t.Fatal("resume moved the private runtime")
	}
	CloseSession(a.sessionID)
	if _, err := os.Stat(runtime); !os.IsNotExist(err) {
		t.Fatalf("runtime was not reclaimed: %v", err)
	}
	// #nosec G304 -- controlled output in a test-owned temporary directory
	if got, err := os.ReadFile(filepath.Join(output, "result.json")); err != nil || string(got) != "result" {
		t.Fatalf("cleanup lost real artifact: %q, %v", got, err)
	}
}

func TestIsolatedOutputProviderLaunchPolicies(t *testing.T) {
	for _, provider := range []llm.Provider{llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderPiCLI, llm.ProviderMuseCLI, llm.ProviderAgyCLI} {
		t.Run(string(provider), func(t *testing.T) {
			output := t.TempDir()
			a := newLinkedOutputTestAgent(t, output)
			if err := a.prepareIsolatedOutputLink(); err != nil {
				t.Fatal(err)
			}
			withCLISecurityPolicy(llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, WorkspaceWritePaths: []string{output}, PrivateHome: "/old-chat-home"})(a)
			opts := a.appendCodingAgentInteractiveOptionsForProvider(nil, provider, "test-model")
			resolved := &llmtypes.CallOptions{}
			for _, option := range opts {
				option(resolved)
			}
			if resolved.CLISecurity == nil || resolved.CLISecurity.Provider != string(provider) {
				t.Fatal("missing provider launch policy")
			}
			if keys := codingAgentResumeOptionKeys[provider]; resolved.Metadata == nil || resolved.Metadata.Custom[keys.directory] != output {
				t.Fatal("provider did not receive its admitted output cwd")
			}
			if resolved.CLIRuntimeDir != a.isolatedWorkspacePath || a.resolveContinuationWorkingDir("") != output {
				t.Fatal("working directory and private generated files were not kept separate on resume")
			}
			if len(resolved.CLISecurity.WorkspaceWritePaths) != 2 || resolved.CLISecurity.WorkspaceWritePaths[0] != output || resolved.CLISecurity.WorkspaceWritePaths[1] != a.isolatedWorkspacePath {
				t.Fatalf("wrong writes: %#v", resolved.CLISecurity)
			}
			if !strings.HasPrefix(resolved.CLISecurity.PrivateHome, a.isolatedWorkspacePath+string(filepath.Separator)) {
				t.Fatal("step reused parent home")
			}
			if a.cliSecurityPolicy.PrivateHome != "/old-chat-home" || len(a.cliSecurityPolicy.WorkspaceWritePaths) != 1 {
				t.Fatal("mutated admitted policy")
			}
			if _, err := os.Stat(filepath.Join(a.isolatedWorkspacePath, "output")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIsolatedOutputRejectsObstructionAndRebinding(t *testing.T) {
	for _, obstruction := range []string{"file", "directory", "wrong-link"} {
		t.Run(obstruction, func(t *testing.T) {
			a := newLinkedOutputTestAgent(t, t.TempDir())
			link := filepath.Join(a.ensureIsolatedWorkspaceDir(), "output")
			var err error
			switch obstruction {
			case "file":
				err = os.WriteFile(link, []byte("keep"), 0600)
			case "directory":
				err = os.Mkdir(link, 0700)
			case "wrong-link":
				err = os.Symlink(t.TempDir(), link)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := a.prepareIsolatedOutputLink(); err == nil {
				t.Fatal("accepted output obstruction")
			}
			if _, err := os.Lstat(link); err != nil {
				t.Fatal("removed obstruction")
			}
		})
	}
	a := newLinkedOutputTestAgent(t, t.TempDir())
	if err := a.prepareIsolatedOutputLink(); err != nil {
		t.Fatal(err)
	}
	a.codingAgentOutputDir = t.TempDir()
	if err := a.prepareIsolatedOutputLink(); err == nil {
		t.Fatal("same session rebound to another iteration")
	}
}

func TestIsolatedOutputIndependentStepsAndRandomCleanup(t *testing.T) {
	root := t.TempDir()
	for _, step := range []string{"iteration-0/group-a/step-1", "iteration-0/group-a/step-2", "iteration-1/group-a/step-1"} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			output := filepath.Join(root, step)
			if err := os.MkdirAll(output, 0700); err != nil {
				t.Fatal(err)
			}
			a := newLinkedOutputTestAgent(t, output)
			if err := a.prepareIsolatedOutputLink(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(a.isolatedWorkspacePath, "output", "result"), []byte(step), 0600); err != nil {
				t.Fatal(err)
			}
			CloseSession(a.sessionID)
			// #nosec G304 -- controlled output in a test-owned temporary directory
			if got, err := os.ReadFile(filepath.Join(output, "result")); err != nil || string(got) != step {
				t.Fatalf("step collision: %q %v", got, err)
			}
		})
	}
	a := &Agent{isolatedSessionWorkspace: true, codingAgentOutputDir: t.TempDir()}
	if err := a.prepareIsolatedOutputLink(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.isolatedWorkspacePath, "output", "result"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.codingAgentOutputDir, "result")); err != nil {
		t.Fatal("random-runtime cleanup followed output link")
	}
}

func TestIsolatedOutputFailsClosedWhenRuntimeCannotBeCreated(t *testing.T) {
	a := newLinkedOutputTestAgent(t, t.TempDir())
	runtime := isolatedWorkspaceDirForSession(a.sessionID)
	if err := os.WriteFile(runtime, []byte("obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareIsolatedOutputLink(); err == nil {
		t.Fatal("accepted unavailable stable runtime")
	}
	if a.isolatedWorkspacePath != "" {
		t.Fatal("fell back to another runtime")
	}
	// #nosec G304 -- controlled output in a test-owned temporary directory
	if got, err := os.ReadFile(runtime); err != nil || string(got) != "obstruction" {
		t.Fatal("overwrote runtime obstruction")
	}
}

func TestIsolatedOutputCloseDoesNotCleanTheBridgeWorkingDirectory(t *testing.T) {
	bridge := t.TempDir()
	marker := filepath.Join(bridge, "AGENTS.md")
	if err := os.WriteFile(marker, []byte("<!-- mlp-session-instructions: orchestrator-generated -->\nkeep"), 0600); err != nil {
		t.Fatal(err)
	}
	a := newLinkedOutputTestAgent(t, t.TempDir())
	a.provider = llm.ProviderCodexCLI
	a.codingAgentWorkingDir = bridge
	if err := a.prepareIsolatedOutputLink(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("isolated cleanup touched the real bridge directory")
	}
}

type linkedOutputFakeModel struct{ llmtypes.Model }

func (linkedOutputFakeModel) GetModelID() string { return "test-model" }

func TestNewAgentLinkedOutputInstructionsAndFailures(t *testing.T) {
	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	session := t.Name() + t.TempDir()
	t.Cleanup(func() { CloseSession(session) })
	a, err := NewAgentFromDefinition(context.Background(), AgentDefinition{Instructions: "execute step"}, RuntimeConfig{Model: linkedOutputFakeModel{}, MCPConfigPath: config, MCP: MCPRuntimeConfig{SessionID: session}, Workspace: WorkspaceRuntimeConfig{IsolatedSession: true, OutputDir: output}, Coding: CodingRuntimeConfig{AgentToolsMode: "full"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !strings.Contains(a.systemPrompt, "STEP_OUTPUT_DIR") || !strings.Contains(a.systemPrompt, "bridge paths") {
		t.Fatal("missing output path contract")
	}
	// mcp_only: native file tools and shell are disabled, so a paragraph about "native file tools" would
	// contradict the tool list and push models to report a false read-only failure (PLAT-496).
	bridgeOnly, err := NewAgentFromDefinition(context.Background(), AgentDefinition{Instructions: "execute step"}, RuntimeConfig{Model: linkedOutputFakeModel{}, MCPConfigPath: config, MCP: MCPRuntimeConfig{SessionID: session + "-bridge"}, Workspace: WorkspaceRuntimeConfig{IsolatedSession: true, OutputDir: output}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridgeOnly.Close()
	if strings.Contains(bridgeOnly.systemPrompt, "native file") || strings.Contains(bridgeOnly.systemPrompt, "Step output directory") {
		t.Fatal("bridge-only step agent was told to use native file tools")
	}
	_, err = NewAgentFromDefinition(context.Background(), AgentDefinition{Instructions: "execute step"}, RuntimeConfig{Model: linkedOutputFakeModel{}, MCPConfigPath: config, Workspace: WorkspaceRuntimeConfig{OutputDir: output}})
	if err == nil {
		t.Fatal("non-isolated output configuration accepted")
	}
}
