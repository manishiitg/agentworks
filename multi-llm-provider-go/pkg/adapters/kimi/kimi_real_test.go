package kimi

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

// kimiMockLogger is a stdout-silent logger for Kimi tests.
type kimiMockLogger struct{}

func (l *kimiMockLogger) Infof(format string, args ...any)         {}
func (l *kimiMockLogger) Errorf(format string, args ...any)        {}
func (l *kimiMockLogger) Debugf(format string, args ...interface{}) {}

func requireKimiRealE2E(t *testing.T) (apiKey, model string) {
	t.Helper()
	if os.Getenv("RUN_KIMI_REAL_E2E") == "" {
		t.Skip("set RUN_KIMI_REAL_E2E=1 to run real Kimi API tests")
	}
	apiKey = strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MOONSHOT_API_KEY"))
	}
	if apiKey == "" {
		t.Skip("set KIMI_API_KEY (or MOONSHOT_API_KEY) to run real Kimi tests")
	}
	model = strings.TrimSpace(os.Getenv("KIMI_REAL_E2E_MODEL"))
	if model == "" {
		model = ModelKimiK26
	}
	return apiKey, model
}

// TestKimiRealCostEstimateOnPlainText proves the Kimi path (OpenAI
// adapter + KimiModelMetadata lookup) emits cost_usd_estimated +
// cost_model_id on GenerationInfo.Additional after a real call.
// Kimi rates come from kimi.GetKimiModelMetadata, not the OpenAI
// registry, so this is a regression test that the
// MetadataLookup-pluggable cost path works for non-OpenAI providers
// hosted on the OpenAI-compatible API.
func TestKimiRealCostEstimateOnPlainText(t *testing.T) {
	apiKey, model := requireKimiRealE2E(t)

	baseURL := strings.TrimSpace(os.Getenv("KIMI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.moonshot.ai/v1"
	}
	client := openaisdk.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)
	adapter := openaiadapter.NewCompatibleOpenAIAdapter(&client, model, &kimiMockLogger{}, openaiadapter.OpenAICompatibilityConfig{
		ProviderName:   "kimi",
		MetadataLookup: GetKimiModelMetadata,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
