package zai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	openaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/openai"
)

// zaiMockLogger is a stdout-silent logger for Z.AI real tests.
type zaiMockLogger struct{}

func (l *zaiMockLogger) Infof(format string, args ...any)          {}
func (l *zaiMockLogger) Errorf(format string, args ...any)         {}
func (l *zaiMockLogger) Debugf(format string, args ...interface{}) {}

func requireZAIRealE2E(t *testing.T) (apiKey, model string) {
	t.Helper()
	if os.Getenv("RUN_ZAI_REAL_E2E") == "" {
		t.Skip("set RUN_ZAI_REAL_E2E=1 to run real Z.AI API tests")
	}
	apiKey = strings.TrimSpace(os.Getenv("ZAI_API_KEY"))
	if apiKey == "" {
		t.Skip("set ZAI_API_KEY to run real Z.AI tests")
	}
	model = strings.TrimSpace(os.Getenv("ZAI_REAL_E2E_MODEL"))
	if model == "" {
		model = ModelGLM46
	}
	return apiKey, model
}

func newRealZAIAdapter(t *testing.T) (llmtypes.Model, string) {
	t.Helper()
	apiKey, model := requireZAIRealE2E(t)

	baseURL := strings.TrimSpace(os.Getenv("ZAI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.z.ai/api/coding/paas/v4"
	}
	client := openaisdk.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)
	adapter := openaiadapter.NewCompatibleOpenAIAdapter(&client, model, &zaiMockLogger{}, openaiadapter.OpenAICompatibilityConfig{
		ProviderName:   "z-ai",
		MetadataLookup: GetZAIModelMetadata,
	})
	return adapter, model
}

// TestZAIRealPlainText is the P0 smoke proving the ZAI_API_KEY +
// base URL + OpenAI-compatible adapter actually round-trips against
// api.z.ai. Mirrors api_provider_test_contract.md §"Plain-text generation".
func TestZAIRealPlainText(t *testing.T) {
	adapter, model := newRealZAIAdapter(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	token := "ZAI_REAL_OK"
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly this token and nothing else: " + token},
			}},
		},
		llmtypes.WithMaxTokens(64),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatalf("no choices in response")
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, token) {
		t.Fatalf("content = %q, want token %q (model=%q)", content, token, model)
	}
}

// TestZAIRealTokenUsage proves that token usage fields are populated
// on the response, so cost tracking and rate-limit accounting can rely
// on them. Per api_provider_test_contract.md §"Token usage in response".
func TestZAIRealTokenUsage(t *testing.T) {
	adapter, model := newRealZAIAdapter(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(32),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		t.Fatal("response missing GenerationInfo")
	}
	gi := resp.Choices[0].GenerationInfo
	if gi.InputTokens == nil || *gi.InputTokens <= 0 {
		t.Fatalf("InputTokens not populated: %+v (model=%q)", gi.InputTokens, model)
	}
	if gi.OutputTokens == nil || *gi.OutputTokens <= 0 {
		t.Fatalf("OutputTokens not populated: %+v (model=%q)", gi.OutputTokens, model)
	}
	if gi.TotalTokens == nil || *gi.TotalTokens <= 0 {
		t.Fatalf("TotalTokens not populated: %+v (model=%q)", gi.TotalTokens, model)
	}
}

// TestZAIRealCostEstimateOnPlainText proves the Z.AI path (OpenAI
// adapter + GetZAIModelMetadata lookup) emits cost_usd_estimated +
// cost_model_id on GenerationInfo.Additional after a real call. Z.AI
// rates come from GetZAIModelMetadata (not the OpenAI registry), so
// this is the regression test that the MetadataLookup-pluggable cost
// path works for a non-OpenAI provider hosted on the OpenAI-compatible
// API. Mirrors TestKimiRealCostEstimateOnPlainText.
func TestZAIRealCostEstimateOnPlainText(t *testing.T) {
	adapter, model := newRealZAIAdapter(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(32),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		t.Fatal("response missing GenerationInfo")
	}
	add := resp.Choices[0].GenerationInfo.Additional
	cost, ok := add["cost_usd_estimated"].(float64)
	if !ok || cost <= 0 {
		t.Fatalf("expected cost_usd_estimated > 0; got %v (type %T). model=%q", add["cost_usd_estimated"], add["cost_usd_estimated"], model)
	}
	if got := add["cost_model_id"]; got != model {
		t.Fatalf("cost_model_id = %v, want %q", got, model)
	}
	t.Logf("✅ cost_usd_estimated=$%.6f model=%q", cost, model)
}

// TestZAIRealInspectorContract is the per-adapter regression for the
// inspector emission contract on Z.AI. It mirrors the Anthropic
// inspector test — see pkg/adapters/anthropic/anthropic_inspector_real_test.go
// for the canonical shape. Failures here usually mean the underlying
// OpenAI-compatible adapter stopped wiring inspector events through
// for the z-ai ProviderName, which would also break the matrix test
// at the repo root.
func TestZAIRealInspectorContract(t *testing.T) {
	adapter, model := newRealZAIAdapter(t)

	rec := &llmtypes.InspectorRecorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(32),
		llmtypes.WithInspectorSink(rec),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices in response")
	}

	events := rec.Events()
	if len(events) == 0 {
		t.Fatal("no inspector events emitted; z-ai adapter not wired into inspector sink")
	}
	if events[0].Phase != llmtypes.InspectorPhaseRequest {
		t.Fatalf("first event phase = %q, want request", events[0].Phase)
	}
	last := events[len(events)-1]
	if last.Phase != llmtypes.InspectorPhaseCompletion {
		t.Fatalf("last event phase = %q, want completion", last.Phase)
	}
	for i, ev := range events {
		if ev.Phase == llmtypes.InspectorPhaseError {
			t.Fatalf("unexpected error event at index %d: %+v", i, ev)
		}
		if ev.Provider != "z-ai" {
			t.Fatalf("events[%d].Provider = %q, want z-ai", i, ev.Provider)
		}
		if ev.Model != model {
			t.Fatalf("events[%d].Model = %q, want %q", i, ev.Model, model)
		}
		if ev.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
	for _, key := range []string{"prompt_tokens", "completion_tokens", "stop_reason", "duration_ms"} {
		if last.Metadata[key] == nil {
			t.Fatalf("completion event missing required key %q", key)
		}
	}
	t.Logf("✅ z-ai inspector contract: %d events", len(events))
}

// TestZAIRealSystemPrompt proves system messages are honored rather
// than flattened into the user role. Per api_provider_test_contract.md
// §"System prompt".
func TestZAIRealSystemPrompt(t *testing.T) {
	adapter, _ := newRealZAIAdapter(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply only with the single word OK. No punctuation."},
			}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Hello, are you there?"},
			}},
		},
		llmtypes.WithMaxTokens(32),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatalf("no choices in response")
	}
	content := strings.ToUpper(strings.TrimSpace(resp.Choices[0].Content))
	content = strings.Trim(content, ".!?,;:")
	if content != "OK" {
		t.Fatalf("system prompt not honored — content = %q, want OK", content)
	}
}
