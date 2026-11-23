package llmproviders_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	openaisdk "github.com/openai/openai-go/v3"
	openaisdkoption "github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	anthropicadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"
	bedrockadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/bedrock"
	openaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/openai"
	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
	zaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/zai"
)

// adapterFactory builds a Model and reports its model ID. Returns
// nils + skip=true when the adapter cannot run (missing env, missing
// dependency, etc.). The matrix test honors skip without failing.
type adapterFactory func(t *testing.T) (model llmtypes.Model, modelID string, skip bool)

// inspectorContractFactories is the registry of adapters that
// participate in the inspector contract. Adding a new provider:
// implement the factory below, register it here, the matrix test
// asserts the SAME contract for the new entry.
//
// This is the "unified interface" enforcement point — adapters honor
// the contract by passing the matrix; the contract stays stable
// because the assertion is centralised.
var inspectorContractFactories = map[string]adapterFactory{
	"anthropic": newRealAnthropicForInspectorMatrix,
	"bedrock":   newRealBedrockForInspectorMatrix,
	"openai":    newRealOpenAIForInspectorMatrix,
	"vertex":    newRealVertexForInspectorMatrix,
	"z-ai":      newRealZAIForInspectorMatrix,
	// TODO: claudecode (structured), codex (structured),
	// cursor-cli (structured). Each registers
	// here as it's wired.
}

// TestInspectorContractMatrix runs the shared assertion against every
// registered adapter. Each provider gets its own subtest; missing
// API keys cause clean Skip rather than failure.
//
// What this enforces:
//   - Same phase ordering (request → events → completion) regardless
//     of provider transport.
//   - Same required metadata keys on the request + completion phases.
//   - Same Seq/Provider/Model envelope on every event.
//
// When this passes for N adapters, those N adapters can be swapped
// behind a UI inspector pane interchangeably.
func TestInspectorContractMatrix(t *testing.T) {
	for name, factory := range inspectorContractFactories {
		t.Run(name, func(t *testing.T) {
			model, modelID, skip := factory(t)
			if skip {
				t.Skipf("%s factory could not build adapter (likely missing API key)", name)
			}

			rec := &llmtypes.InspectorRecorder{}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			// MaxTokens=512 is sized for thinking-on-by-default models
			// (e.g. gemini-3.8-flash regularly uses 300+ tokens for
			// internal thinking before emitting any output). A lower
			// cap caused the gemini factory to return zero candidates.
			// Cost is trivial per matrix run.
			_, err := model.GenerateContent(ctx,
				[]llmtypes.MessageContent{
					{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
				},
				llmtypes.WithMaxTokens(512),
				llmtypes.WithInspectorSink(rec),
			)
			if err != nil {
				t.Fatalf("%s GenerateContent: %v", name, err)
			}

			assertInspectorContract(t, rec.Events(), name, modelID)
		})
	}
}

// --- shared contract assertion (mirror of the per-adapter test) ---

func assertInspectorContract(t *testing.T, events []llmtypes.InspectorEvent, wantProvider, wantModel string) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no inspector events emitted; adapter is not wired up")
	}

	if events[0].Phase != llmtypes.InspectorPhaseRequest {
		t.Fatalf("first event phase = %q, want %q", events[0].Phase, llmtypes.InspectorPhaseRequest)
	}
	last := events[len(events)-1]
	if last.Phase != llmtypes.InspectorPhaseCompletion {
		t.Fatalf("last event phase = %q, want %q", last.Phase, llmtypes.InspectorPhaseCompletion)
	}
	for _, ev := range events {
		if ev.Phase == llmtypes.InspectorPhaseError {
			t.Fatalf("unexpected error event: %+v", ev)
		}
	}

	// Mid-stream event phase is RECOMMENDED but not required. Anthropic
	// emits per SSE event; openai/vertex currently use only request +
	// completion bookends (per-event emission is a per-adapter follow-up).
	// The contract here pins down the boundary events and lets richer
	// streams be additive.
	sawEvent := false
	for _, ev := range events {
		if ev.Phase == llmtypes.InspectorPhaseEvent {
			sawEvent = true
			break
		}
	}
	if !sawEvent {
		t.Logf("note: %s emitted no mid-stream event-phase entries (recommended but not required)", wantProvider)
	}

	for i, ev := range events {
		if ev.Provider != wantProvider {
			t.Fatalf("events[%d].Provider = %q, want %q", i, ev.Provider, wantProvider)
		}
		if ev.Model != wantModel {
			t.Fatalf("events[%d].Model = %q, want %q", i, ev.Model, wantModel)
		}
		if ev.Timestamp.IsZero() {
			t.Fatalf("events[%d].Timestamp is zero", i)
		}
		if ev.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}

	req := events[0]
	for _, key := range []string{"message_count"} {
		if req.Metadata[key] == nil {
			t.Fatalf("request event missing required key %q", key)
		}
	}
	for _, key := range []string{"prompt_tokens", "completion_tokens", "stop_reason", "duration_ms"} {
		if last.Metadata[key] == nil {
			t.Fatalf("completion event missing required key %q", key)
		}
	}

	t.Logf("✅ %s: %d events", wantProvider, len(events))
}

// --- factories ---

func newRealAnthropicForInspectorMatrix(t *testing.T) (llmtypes.Model, string, bool) {
	t.Helper()
	if os.Getenv("RUN_ANTHROPIC_REAL_E2E") == "" {
		return nil, "", true
	}
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		return nil, "", true
	}
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_REAL_E2E_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5"
	}
	client := anthropic.NewClient(anthropicoption.WithAPIKey(apiKey))
	return anthropicadapter.NewAnthropicAdapter(client, model, &matrixMockLogger{}), model, false
}

func newRealOpenAIForInspectorMatrix(t *testing.T) (llmtypes.Model, string, bool) {
	t.Helper()
	if os.Getenv("RUN_OPENAI_REAL_E2E") == "" {
		return nil, "", true
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, "", true
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_REAL_E2E_MODEL"))
	if model == "" {
		model = "gpt-5.4-mini"
	}
	client := openaisdk.NewClient(openaisdkoption.WithAPIKey(apiKey))
	return openaiadapter.NewOpenAIAdapter(&client, model, &matrixMockLogger{}), model, false
}

func newRealBedrockForInspectorMatrix(t *testing.T) (llmtypes.Model, string, bool) {
	t.Helper()
	if os.Getenv("RUN_BEDROCK_REAL_E2E") == "" {
		return nil, "", true
	}
	model := strings.TrimSpace(os.Getenv("BEDROCK_REAL_E2E_MODEL"))
	if model == "" {
		model = "global.anthropic.claude-sonnet-4-5-20250929-v1:0"
	}
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		t.Fatalf("aws LoadDefaultConfig: %v", err)
	}
	client := bedrockruntime.NewFromConfig(cfg)
	return bedrockadapter.NewBedrockAdapter(client, model, &matrixMockLogger{}), model, false
}

func newRealVertexForInspectorMatrix(t *testing.T) (llmtypes.Model, string, bool) {
	t.Helper()
	if os.Getenv("RUN_VERTEX_REAL_E2E") == "" {
		return nil, "", true
	}
	var apiKey string
	for _, name := range []string{"GEMINI_API_KEY", "VERTEX_API_KEY", "GOOGLE_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			apiKey = v
			break
		}
	}
	if apiKey == "" {
		return nil, "", true
	}
	model := strings.TrimSpace(os.Getenv("VERTEX_REAL_E2E_MODEL"))
	if model == "" {
		model = "gemini-3.8-flash"
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		t.Fatalf("genai.NewClient: %v", err)
	}
	return vertexadapter.NewGoogleGenAIAdapter(client, model, &matrixMockLogger{}), model, false
}

func newRealZAIForInspectorMatrix(t *testing.T) (llmtypes.Model, string, bool) {
	t.Helper()
	if os.Getenv("RUN_ZAI_REAL_E2E") == "" {
		return nil, "", true
	}
	apiKey := strings.TrimSpace(os.Getenv("ZAI_API_KEY"))
	if apiKey == "" {
		return nil, "", true
	}
	model := strings.TrimSpace(os.Getenv("ZAI_REAL_E2E_MODEL"))
	if model == "" {
		model = zaiadapter.ModelGLM46
	}
	baseURL := strings.TrimSpace(os.Getenv("ZAI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.z.ai/api/coding/paas/v4"
	}
	client := openaisdk.NewClient(
		openaisdkoption.WithAPIKey(apiKey),
		openaisdkoption.WithBaseURL(baseURL),
	)
	adapter := openaiadapter.NewCompatibleOpenAIAdapter(&client, model, &matrixMockLogger{}, openaiadapter.OpenAICompatibilityConfig{
		ProviderName:   "z-ai",
		MetadataLookup: zaiadapter.GetZAIModelMetadata,
	})
	return adapter, model, false
}

// matrixMockLogger is a silent logger for the matrix test.
type matrixMockLogger struct{}

func (l *matrixMockLogger) Infof(format string, args ...any)  {}
func (l *matrixMockLogger) Errorf(format string, args ...any) {}
func (l *matrixMockLogger) Debugf(format string, args ...any) {}
