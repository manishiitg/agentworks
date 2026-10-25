package agent

import (
	"context"
	"testing"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type wrapperSessionTestModel struct{}

func (wrapperSessionTestModel) GenerateContent(context.Context, []llmtypes.MessageContent, ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: "done"}}}, nil
}

func (wrapperSessionTestModel) GetModelID() string { return "wrapper-session-test" }

func (wrapperSessionTestModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{ModelID: "wrapper-session-test"}, nil
}

func TestWrapperKeepsSessionAfterTurnReturns(t *testing.T) {
	const sessionID = "wrapper-retained-after-turn"
	mcpagent.CloseSession(sessionID)
	runtime := mcpagent.RuntimeConfig{
		Model:         wrapperSessionTestModel{},
		Generation:    mcpagent.GenerationRuntimeConfig{MaxTurns: 1},
		MCP:           mcpagent.MCPRuntimeConfig{SessionID: sessionID},
		Observability: mcpagent.ObservabilityRuntimeConfig{Logger: loggerv2.NewNoop()},
	}
	draft, err := mcpagent.NewAgentFromDefinition(context.Background(), mcpagent.AgentDefinition{}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &LLMAgentWrapper{
		agent:      draft,
		config:     LLMAgentConfig{},
		metrics:    &agentMetricsImpl{MinLatency: time.Duration(^uint64(0) >> 1)},
		logger:     loggerv2.NewNoop(),
		runtime:    runtime,
		definition: mcpagent.AgentDefinition{},
	}
	// An empty turn returns before provider I/O. The lifecycle assertion is that
	// returning from the wrapper's real turn method does not invoke the
	// one-shot Agent.Run cleanup path and unregister the conversation Session.
	if _, err := wrapper.InvokeWithHistory(context.Background(), nil); err == nil {
		t.Fatal("empty turn unexpectedly succeeded")
	}
	if _, ok := mcpagent.LookupSession(sessionID); !ok {
		t.Fatal("wrapper session disappeared when the completed turn returned")
	}
	if err := wrapper.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := mcpagent.LookupSession(sessionID); ok {
		t.Fatal("wrapper Close left its durable session registered")
	}
}

func TestWrapperBuildsSessionTurnWithExplicitContinuationInput(t *testing.T) {
	history := []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}}}}
	turn := buildSessionTurn("[AUTO-NOTIFICATION] child completed", history, mcpagent.ToolPolicy{}, nil)
	if turn.ID == "" {
		t.Fatal("turn has no stable platform identity")
	}
	if turn.Input != "[AUTO-NOTIFICATION] child completed" {
		t.Fatalf("turn input = %q, want current continuation", turn.Input)
	}
	if len(turn.History) != 1 {
		t.Fatalf("turn history = %d messages, want prior history only", len(turn.History))
	}
}

func TestWrapperRetainsInstalledSkillResolverForFinalizedAgent(t *testing.T) {
	wrapper := &LLMAgentWrapper{}
	resolver := func(name, path string) (mcpagent.InstalledSkillFile, error) {
		return mcpagent.InstalledSkillFile{Content: name + ":" + path}, nil
	}
	if err := wrapper.SetInstalledSkillResolver("Crew/example", resolver); err != nil {
		t.Fatalf("SetInstalledSkillResolver: %v", err)
	}
	if wrapper.installedSkillResolver == nil {
		t.Fatal("resolver was not retained on the immutable wrapper definition")
	}
	wrapper.finalized = true
	if err := wrapper.SetInstalledSkillResolver("Crew/example", resolver); err == nil {
		t.Fatal("resolver changed after immutable definition was finalized")
	}
}
