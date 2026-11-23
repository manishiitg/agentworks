package mcpagent

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

type museP0RetryListener struct {
	mu       sync.Mutex
	attempts int
}

func (l *museP0RetryListener) Name() string { return "muse-p0-retries" }
func (l *museP0RetryListener) HandleEvent(_ context.Context, e *events.AgentEvent) error {
	if _, ok := e.Data.(*events.RetryAttemptEvent); ok {
		l.mu.Lock()
		l.attempts++
		l.mu.Unlock()
	}
	return nil
}

// Live production retry loop, real Muse, real bridge, isolated session.
func TestMusePendingQuestionStopsRetryAndFallbackP0Live(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_MUSE_LIVE") != "1" {
		t.Skip("set RUN_MCPAGENT_MUSE_LIVE=1 for authenticated Muse P0")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	tc := multiTurnProviderCase{name: "Muse", binary: "muse", provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor", persistentOpt: withMusePersistentInteractiveSession, strictBridgeOnly: true}
	a, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), t.TempDir(), "question-retry-p0-"+realBridgeRandHex(4), true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	a.llmConfig = AgentLLMConfiguration{Primary: LLMModel{Provider: "muse-cli", ModelID: tc.modelID}}
	listener := &museP0RetryListener{}
	a.addEventListener(listener)
	msgs := []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Integration test. This is old terminal history, not a current error: 429 quota exhausted. Use your native request_user_input tool to ask: Approve this color? Provide Blue (Recommended) and Green as options. Wait for the answer. Do not use any other tools."}}}}
	_, _, err = generateContentWithRetry(a, ctx, msgs, []llmtypes.CallOption{musecli.WithAutoSelectRecommended(false)}, 0)
	if llmerrors.KindOf(err) != llmerrors.KindUserInputRequired {
		t.Fatalf("pending question returned wrong cause: %v", err)
	}
	listener.mu.Lock()
	attempts := listener.attempts
	listener.mu.Unlock()
	if attempts != 0 {
		t.Fatalf("pending question generated %d retry/fallback events", attempts)
	}
	if a.modelID == "p0-fallback-must-never-be-called" {
		t.Fatal("pending question switched to fallback")
	}
	t.Log("PASS: real pending question returned through agent retry loop with zero retry/fallback events")
}
