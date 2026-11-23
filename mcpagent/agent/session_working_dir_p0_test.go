package mcpagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

func TestCodingAgentWorkingDirPersistenceAllProvidersP0(t *testing.T) {
	contracts := llmproviders.CodingAgentProviderContracts()
	if len(contracts) == 0 {
		t.Fatal("coding-agent registry is empty")
	}
	for _, contract := range contracts {
		provider := contract.Provider
		keys, ok := codingAgentResumeOptionKeys[provider]
		if !ok {
			t.Fatalf("new provider %s has no P0 resume expectations", provider)
		}
		if !contract.SupportsNativeResume {
			t.Fatalf("provider %s does not support native resume", provider)
		}
		for _, variant := range []struct {
			name      string
			isolated  bool
			transport string
		}{
			{"builder/tmux", false, llmtypes.CodingProviderTransportTmux},
			{"isolated/tmux", true, llmtypes.CodingProviderTransportTmux},
			{"builder/structured", false, llmtypes.CodingProviderTransportStructured},
			{"isolated/structured", true, llmtypes.CodingProviderTransportStructured},
		} {
			isolated := variant.isolated
			t.Run(string(provider)+"/"+variant.name, func(t *testing.T) {
				expected := filepath.Join(t.TempDir(), "private-runtime")
				privateArtifacts := t.TempDir() // PLAT-856-145: cwd and private runtime are independent durable identities.
				agent := &Agent{sessionID: "chat-a", provider: provider, codingAgentWorkingDir: expected, codingAgentRuntimeDir: privateArtifacts, isolatedSessionWorkspace: isolated}
				if isolated {
					agent.codingAgentWorkingDir = "/workflow/shared"
					agent.isolatedWorkspaceOnce.Do(func() { agent.isolatedWorkspacePath = expected; agent.isolatedWorkspaceStable = true })
				}
				partial := llmtypes.CodingProviderSessionHandle{Provider: string(provider), NativeSessionID: "native-a", Transport: variant.transport}
				response := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: &llmtypes.GenerationInfo{CodingProviderSessionHandle: &partial}}}}
				agent.updateCodingProviderSessionHandleFromResponse(response)
				if agent.codingProviderSessionHandle.WorkingDir != expected {
					t.Fatal("provider response erased launch directory")
				}
				// Simulate a legacy partial typed handle already resident in memory.
				agent.codingProviderSessionHandle.WorkingDir = ""
				raw, err := json.Marshal(SnapshotAgentSession(agent))
				if err != nil {
					t.Fatal(err)
				}
				var saved AgentSessionHandle
				if err := json.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Provider.WorkingDir != expected || saved.Provider.RuntimeDir != privateArtifacts || saved.Provider.NativeSessionID != "native-a" {
					t.Fatalf("incomplete durable handle: %+v", saved.Provider)
				}
				restored := &Agent{provider: provider, sessionID: "chat-a"}
				restored.applyAgentSessionHandle(&saved)
				got := SnapshotAgentSession(restored)
				if got.Provider.WorkingDir != expected || got.Provider.RuntimeDir != privateArtifacts || got.Provider.NativeSessionID != "native-a" {
					t.Fatalf("restart lost identity: %+v", got.Provider)
				}
				continuation, ok := restored.codingProviderContinuationHandleForModel(provider, contract.ModelID)
				if !ok {
					t.Fatal("restored session was not resumable")
				}
				probe := &codingAgentResumeProbe{}
				if _, err := llm.ContinueCodingAgentSession(context.Background(), probe, continuation, "next message"); err != nil {
					t.Fatal(err)
				}
				if probe.options.CLIRuntimeDir != privateArtifacts {
					t.Fatal("continuation lost private runtime")
				}
				if probe.calls != 1 {
					t.Fatalf("adapter calls = %d, want exactly one", probe.calls)
				}
				if probe.options.Metadata == nil {
					t.Fatal("missing native resume options")
				}
				metadata := probe.options.Metadata.Custom
				if metadata[keys.resume] != "native-a" || metadata[keys.directory] != expected {
					t.Fatalf("adapter received wrong resume identity: resume=%v cwd=%v", metadata[keys.resume], metadata[keys.directory])
				}
				if len(probe.messages) != 1 || probe.messages[0].Role != llmtypes.ChatMessageTypeHuman {
					t.Fatal("native continuation replayed history instead of current message")
				}

			})
		}
	}
}

func TestCodingAgentWorkingDirDoesNotOverrideProviderIdentityP0(t *testing.T) {
	agent := &Agent{provider: llm.ProviderMuseCLI, codingAgentWorkingDir: "/current"}
	explicit := llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", WorkingDir: "/provider-reported", NativeSessionID: "native"}
	if got := agent.withContinuationWorkingDir(explicit); got.WorkingDir != "/provider-reported" {
		t.Fatal("overwrote provider's explicit directory")
	}
	different := llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", NativeSessionID: "native"}
	if got := agent.withContinuationWorkingDir(different); got.WorkingDir != "" {
		t.Fatal("assigned another provider's directory")
	}
}

// Independent adapter-key expectations: adding a provider to the production
// registry must also extend this matrix or the required P0 fails.
var codingAgentResumeOptionKeys = map[llm.Provider]struct{ resume, directory string }{
	llm.ProviderClaudeCode: {claudecode.MetadataKeyResumeSessionID, claudecode.MetadataKeyWorkingDir},
	llm.ProviderCodexCLI:   {codexcli.MetadataKeyResumeSessionID, codexcli.MetadataKeyProjectDirID},
	llm.ProviderCursorCLI:  {cursorcli.MetadataKeyResumeSessionID, cursorcli.MetadataKeyWorkingDir},
	llm.ProviderPiCLI:      {picli.MetadataKeyResumeSessionID, picli.MetadataKeyWorkingDir},
	llm.ProviderMuseCLI:    {musecli.MetadataKeyMuseResumeSessionID, musecli.MetadataKeyMuseWorkingDir},
	llm.ProviderAgyCLI:     {agycli.MetadataKeyResumeSessionID, agycli.MetadataKeyWorkingDir},
}

type codingAgentResumeProbe struct {
	calls    int
	options  llmtypes.CallOptions
	messages []llmtypes.MessageContent
}

func (p *codingAgentResumeProbe) GenerateContent(_ context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	p.calls++
	p.messages = messages
	for _, option := range options {
		option(&p.options)
	}
	return &llmtypes.ContentResponse{}, nil
}
func (*codingAgentResumeProbe) GetModelID() string { return "" }
func (*codingAgentResumeProbe) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return nil, nil
}
