package mcpagent

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Layer 2 coverage for a DIRECT API provider.
//
// mcpagent's job is orchestration: run the agent loop, hand the model its
// tools, feed results back, produce a final answer. Every live test in this
// package exercises that through a coding-agent CLI (Pi, Claude, Codex,
// Cursor) — see layer2_certification.go, whose transports are exactly "tmux"
// and "json". Nothing ran the loop against a plain API provider, even though
// AgentWorks exposes OpenAI / Anthropic / "Gemini / Vertex" / Bedrock / Azure /
// MiniMax as api_model integrations with real API-key setup flows, so a user
// can enable a Gemini key and run workflow steps on this path today.
//
// The Layer 1 sibling (multi-llm-provider-go's vertex real-API tests) certifies
// a single adapter CALL. This certifies the layer above it: that a tool is
// actually offered to the model, its result is fed back, and the loop reaches a
// final answer.
//
// Gate: RUN_API_PROVIDER_AGENT_LOOP_E2E=1 plus GEMINI_API_KEY (or
// VERTEX_API_KEY / GOOGLE_API_KEY). Vertex is used because it is the cheapest
// current-gen provider already covered at Layer 1, so a failure here isolates
// to orchestration rather than to the adapter.
func requireAPIProviderAgentLoopE2E(t *testing.T) string {
	t.Helper()
	if os.Getenv("RUN_API_PROVIDER_AGENT_LOOP_E2E") != "1" {
		t.Skip("set RUN_API_PROVIDER_AGENT_LOOP_E2E=1 to run the direct-API agent-loop e2e")
	}
	for _, envPath := range []string{"../.env", "../../multi-llm-provider-go/.env"} {
		_ = godotenv.Load(envPath)
	}
	for _, name := range []string{"GEMINI_API_KEY", "VERTEX_API_KEY", "GOOGLE_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	t.Skip("set GEMINI_API_KEY (or VERTEX_API_KEY / GOOGLE_API_KEY) to run the direct-API agent-loop e2e")
	return ""
}

func newVertexAgentForLoopTest(t *testing.T, apiKey string, tools ToolSet) *Agent {
	t.Helper()
	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderVertex,
		ModelID:  apiProviderLoopModel(),
		Logger:   loggerv2.NewDefault(),
		APIKeys:  &llm.ProviderAPIKeys{Vertex: &apiKey},
	})
	if err != nil {
		t.Fatalf("InitializeLLM(vertex): %v", err)
	}

	agent, err := NewAgentFromDefinition(context.Background(), AgentDefinition{
		Instructions: "You are a test agent. When a tool can answer the question, call it and use its result. Be terse.",
		Tools:        tools,
	}, RuntimeConfig{
		Model: model,
		Generation: GenerationRuntimeConfig{
			Provider: llm.ProviderVertex,
			MaxTurns: 6,
		},
	})
	if err != nil {
		t.Fatalf("NewAgentFromDefinition(vertex): %v", err)
	}
	return agent
}

func apiProviderLoopModel() string {
	if v := strings.TrimSpace(os.Getenv("API_PROVIDER_LOOP_E2E_MODEL")); v != "" {
		return v
	}
	return "gemini-3.5-flash-lite"
}

// The core Layer 2 contract: the model is actually offered the tool, mcpagent
// executes it, and the RESULT is fed back into the conversation so the final
// answer depends on it.
//
// The sentinel is deliberately a value the model cannot know or guess — if it
// appears in the answer, the tool ran and its output round-tripped. Asserting
// only "the tool was called" would pass even if the result were dropped, which
// is the failure that actually matters at this layer.
func TestAPIProviderAgentLoopExecutesToolAndUsesResult(t *testing.T) {
	apiKey := requireAPIProviderAgentLoopE2E(t)

	const sentinel = "ZQ7-VERTEX-LOOP-4417"
	var calls atomic.Int32

	agent := newVertexAgentForLoopTest(t, apiKey, ToolSet{
		Direct: []ToolDefinition{{
			Name:        "get_vault_code",
			Description: "Returns the vault access code. This is the ONLY way to learn the code.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Execute: func(_ context.Context, _ map[string]interface{}) (string, error) {
				calls.Add(1)
				return sentinel, nil
			},
		}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := agent.Run(ctx, Turn{
		Input: "What is the vault access code? Call the tool, then reply with the code and nothing else.",
	})
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("tool executed %d times, want exactly 1 — the model was not offered the tool, or the loop re-ran it", got)
	}
	if !strings.Contains(result.Text, sentinel) {
		t.Fatalf("final answer %q does not contain the tool's result %q — the tool ran but its output never reached the model", result.Text, sentinel)
	}
}

// A tool that fails must surface as a failure the model can react to, not as a
// silent empty result or a crashed turn. This is the tool_failure_recovery
// capability Layer 2 already certifies for every CLI provider
// (layer2_certification.go), applied to the direct-API path.
func TestAPIProviderAgentLoopSurvivesToolFailure(t *testing.T) {
	apiKey := requireAPIProviderAgentLoopE2E(t)

	var calls atomic.Int32
	agent := newVertexAgentForLoopTest(t, apiKey, ToolSet{
		Direct: []ToolDefinition{{
			Name:        "check_inventory",
			Description: "Checks warehouse inventory.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Execute: func(_ context.Context, _ map[string]interface{}) (string, error) {
				calls.Add(1)
				return "", context.DeadlineExceeded
			},
		}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := agent.Run(ctx, Turn{
		Input: "Check the inventory. If the tool fails, say FAILED and stop — do not retry more than once.",
	})
	// The turn itself must not error: a failing TOOL is data for the model, not
	// a broken run. That distinction is the whole point of this test.
	if err != nil {
		t.Fatalf("agent.Run returned an error for a failing tool; a tool failure must be reported to the model, not abort the turn: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("the failing tool was never executed")
	}
	if strings.TrimSpace(result.Text) == "" {
		t.Fatal("no final answer after a tool failure — the model was left with nothing to respond to")
	}
}

// Multi-turn continuity: history from turn one must reach turn two. On the CLI
// providers this is native --resume; on a direct API provider there is no
// session to resume, so mcpagent owns replaying history itself — a genuinely
// different code path, and the reason this cannot be assumed from the CLI
// coverage.
func TestAPIProviderAgentLoopKeepsHistoryAcrossTurns(t *testing.T) {
	apiKey := requireAPIProviderAgentLoopE2E(t)

	agent := newVertexAgentForLoopTest(t, apiKey, ToolSet{})
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatalf("agent.Start: %v", err)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const sentinel = "PLUM-8823"
	if _, err := session.Run(ctx, Turn{
		Input: "Remember this token exactly: " + sentinel + ". Reply ACK.",
	}); err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	second, err := session.Run(ctx, Turn{
		Input: "What token did I ask you to remember? Reply with only the token.",
	})
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if !strings.Contains(second.Text, sentinel) {
		t.Fatalf("turn 2 answer %q lost the token from turn 1 — history did not carry across turns", second.Text)
	}
}
