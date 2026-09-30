package orchestrator

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodingAgentOutputRuntimeUsesDedicatedStepGuard(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	for _, outputRel := range []string{
		"Workflow/demo/runs/iteration-0/group-a/execution/step-1",
		"Workflow/demo/runs/iteration-0/group-b/execution/step-1/agents/child",
		"Workflow/demo/runs/iteration-1/group-a/execution/step-1/messages/item-2",
	} {
		t.Run(outputRel, func(t *testing.T) {
			session := t.Name()
			t.Cleanup(func() { common.ClearSessionShellConfig(session) })
			output := filepath.Join(docs, outputRel)
			read := "Workflow/demo/runs/iteration-0"
			common.SetSessionFolderGuard(session, []string{read}, []string{outputRel})
			common.SetSessionShellEnv(session, map[string]string{"STEP_OUTPUT_DIR": output})
			parent := llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, LandlockRunner: "/launcher", PrivateHome: "/parent-home", WorkspaceWritePaths: []string{docs}}
			config := agents.NewOrchestratorAgentConfig("step")
			config.MCPSessionID = session
			config.LLMConfig.Primary.Provider = "agy-cli"
			config.IsolateCodingAgentWorkspace = true
			config.CodingAgentOutputDir = output
			config.CLISecurityPolicy = &parent
			if err := prepareCodingAgentOutputRuntime(config); err != nil {
				t.Fatal(err)
			}
			canonical, err := filepath.EvalSymlinks(output)
			if err != nil {
				t.Fatal(err)
			}
			if config.CodingAgentOutputDir != canonical {
				t.Fatalf("wrong output: %q", config.CodingAgentOutputDir)
			}
			if !reflect.DeepEqual(config.CLISecurityPolicy.WorkspaceWritePaths, []string{canonical}) {
				t.Fatalf("parent write leaked: %#v", config.CLISecurityPolicy)
			}
			if config.CLISecurityPolicy.LandlockRunner != "/launcher" || config.CLISecurityPolicy.Mode != llmtypes.CLISecurityModeIsolated {
				t.Fatal("lost confinement")
			}
			if !reflect.DeepEqual(parent.WorkspaceWritePaths, []string{docs}) {
				t.Fatal("mutated parent policy")
			}
		})
	}
}

func TestCodingAgentOutputRuntimeRejectsUnauthorizedAndEscapedTargets(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	write := filepath.Join(docs, "own-step")
	outside := t.TempDir()
	if err := os.Mkdir(write, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(write, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{filepath.Join(docs, "sibling-step"), filepath.Join(write, "escape", "new-output")} {
		session := t.Name() + output
		common.SetSessionFolderGuard(session, nil, []string{write})
		common.SetSessionShellEnv(session, map[string]string{"STEP_OUTPUT_DIR": output})
		t.Cleanup(func() { common.ClearSessionShellConfig(session) })
		config := agents.NewOrchestratorAgentConfig("step")
		config.MCPSessionID = session
		config.LLMConfig.Primary.Provider = "codex-cli"
		config.IsolateCodingAgentWorkspace = true
		config.CodingAgentOutputDir = output
		if err := prepareCodingAgentOutputRuntime(config); err == nil {
			t.Fatalf("accepted unauthorized output %q", output)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatalf("created unauthorized output: %v", err)
		}
	}
}

func TestCodingAgentOutputRuntimeRequiresExplicitMatchingOutput(t *testing.T) {
	session := t.Name()
	output := t.TempDir()
	common.SetSessionFolderGuard(session, nil, []string{output})
	common.SetSessionShellEnv(session, map[string]string{"STEP_OUTPUT_DIR": output})
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	config := agents.NewOrchestratorAgentConfig("review")
	config.MCPSessionID = session
	config.LLMConfig.Primary.Provider = "claude-code"
	config.IsolateCodingAgentWorkspace = true
	if err := prepareCodingAgentOutputRuntime(config); err != nil {
		t.Fatal(err)
	}
	if config.CodingAgentOutputDir != "" {
		t.Fatal("review inherited output link")
	}
	config.CodingAgentOutputDir = t.TempDir()
	if err := prepareCodingAgentOutputRuntime(config); err == nil {
		t.Fatal("accepted output different from dedicated session")
	}
}
