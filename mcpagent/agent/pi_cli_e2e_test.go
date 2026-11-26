package mcpagent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestPiCLIMcpAgentGeminiE2E(t *testing.T) {
	if os.Getenv("RUN_PI_CLI_MCPAGENT_E2E") != "1" {
		t.Skip("set RUN_PI_CLI_MCPAGENT_E2E=1 to run the real Pi CLI mcpagent smoke test")
	}

	for _, envPath := range []string{"../.env", "../../multi-llm-provider-go/.env"} {
		_ = godotenv.Load(envPath)
	}
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("PI_API_KEY"))
	}
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY or PI_API_KEY is required for real Pi CLI smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sessionID := "pi-mcpagent-e2e-" + strings.ReplaceAll(t.Name(), "/", "-")
	t.Cleanup(func() {
		llm.ClosePiCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	})

	agent := &Agent{
		provider:                       llm.ProviderPiCLI,
		modelID:                        "google/gemini-3.8-flash",
		sessionID:                      sessionID,
		codingAgentWorkingDir:          t.TempDir(),
		piPersistentInteractiveSession: false,
		logger:                         loggerv2.NewDefault(),
		apiKeys:                        &llm.ProviderAPIKeys{PiCLI: &apiKey},
		enableStreaming:                true,
	}

	model := LLMModel{
		Provider: string(llm.ProviderPiCLI),
		ModelID:  "google/gemini-3.8-flash",
		APIKey:   &apiKey,
		Options: map[string]interface{}{
			"pi_provider": "google",
		},
	}
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly MCPAGENT_PI_OK and no other words."),
	}

	resp, err := agent.executeLLMInner(ctx, model, messages, nil, false)
	if err != nil {
		t.Fatalf("executeLLMInner(pi-cli) error: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("empty Pi CLI response: %#v", resp)
	}
	if !strings.Contains(resp.Choices[0].Content, "MCPAGENT_PI_OK") {
		t.Fatalf("Pi CLI content = %q, want MCPAGENT_PI_OK", resp.Choices[0].Content)
	}

	extractCodingAgentSessionIDs(agent, resp)
	handle := agent.codingProviderSessionHandle
	if handle.Provider != string(llm.ProviderPiCLI) {
		t.Fatalf("handle provider = %q, want %q", handle.Provider, llm.ProviderPiCLI)
	}
	if handle.Transport != llmtypes.CodingProviderTransportTmux {
		t.Fatalf("handle transport = %q, want %q", handle.Transport, llmtypes.CodingProviderTransportTmux)
	}
	if strings.TrimSpace(handle.TmuxSession) == "" {
		t.Fatalf("expected Pi CLI tmux session handle, got %#v", handle)
	}
}
