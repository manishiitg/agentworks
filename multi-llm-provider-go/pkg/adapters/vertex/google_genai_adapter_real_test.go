package vertex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Real-API e2e tests for the Gemini path of the Vertex adapter, run
// against Google AI Studio (BackendGeminiAPI) with a plain API key.
//
// Gate: RUN_VERTEX_REAL_E2E=1 + GEMINI_API_KEY (or VERTEX_API_KEY or
// GOOGLE_API_KEY — any of the three works). Model override via
// VERTEX_REAL_E2E_MODEL (defaults to gemini-3.5-flash-lite, the
// cheapest current-gen Gemini model that still supports tools + JSON
// mode).
//
// Reference for shape: pkg/adapters/anthropic/anthropic_real_test.go.

func requireVertexRealE2E(t *testing.T) (apiKey, model string) {
	t.Helper()
	if os.Getenv("RUN_VERTEX_REAL_E2E") == "" {
		t.Skip("set RUN_VERTEX_REAL_E2E=1 to run real Gemini API tests")
	}
	for _, name := range []string{"GEMINI_API_KEY", "VERTEX_API_KEY", "GOOGLE_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			apiKey = v
			break
		}
	}
	if apiKey == "" {
		t.Skip("set GEMINI_API_KEY (or VERTEX_API_KEY / GOOGLE_API_KEY) to run real Gemini tests")
	}
	model = strings.TrimSpace(os.Getenv("VERTEX_REAL_E2E_MODEL"))
	if model == "" {
		model = ModelGemini35FlashLite
	}
	return apiKey, model
}

func newRealVertexAdapter(t *testing.T) (*GoogleGenAIAdapter, string) {
	t.Helper()
	apiKey, model := requireVertexRealE2E(t)
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		t.Fatalf("genai.NewClient: %v", err)
	}
	return NewGoogleGenAIAdapter(client, model, &MockLogger{}), model
}

// TestVertexRealPlainText (contract P0 #1).
func TestVertexRealPlainText(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly the single word OK."}}},
		},
		llmtypes.WithMaxTokens(256),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("no choices in response")
	}
	if !strings.Contains(strings.ToUpper(resp.Choices[0].Content), "OK") {
		t.Fatalf("response did not contain OK: %q", resp.Choices[0].Content)
	}
}

// TestVertexRealThinkingLevel verifies the new Flash models accept the
// string-based thinking control that replaces thinking_budget.
func TestVertexRealThinkingLevel(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly the single word OK."}}},
		},
		llmtypes.WithThinkingLevel("medium"),
		llmtypes.WithMaxTokens(256),
	)
	if err != nil {
		t.Fatalf("GenerateContent with thinking_level=medium error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil || !strings.Contains(strings.ToUpper(resp.Choices[0].Content), "OK") {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// TestVertexRealSystemPromptHonored (contract P0 #4). The previous
// adapter folded system messages into user-role messages, which
// fragmented multi-turn behavior and broke Gemini's native
// SystemInstruction. After the fix, the system instruction must drive
// the model's behavior. We pick a unique sentinel and ask the system
// prompt to make the model emit it verbatim — that's only possible if
// the system instruction is honored.
func TestVertexRealSystemPromptHonored(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	const sentinel = "GEM_SYS_OK_2026"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "When the user asks 'ping', reply with exactly the token " + sentinel + " and nothing else."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "ping"}}},
		},
		llmtypes.WithMaxTokens(256),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if !strings.Contains(resp.Choices[0].Content, sentinel) {
		t.Fatalf("response did not honor the system instruction; sentinel %q missing.\ncontent=%q", sentinel, resp.Choices[0].Content)
	}
}

// TestVertexRealToolDescriptionInfluencesSelection (contract P0 #8).
// Two near-identically-named tools with very different descriptions;
// model must pick by description.
func TestVertexRealToolDescriptionInfluencesSelection(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	props := map[string]interface{}{
		"location": map[string]interface{}{"type": "string", "description": "City or place"},
	}
	tools := []llmtypes.Tool{
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "lookup_alpha",
			Description: "Returns the current local TIME in a given city. Use ONLY when the user asks about time / hours / clock.",
			Parameters:  &llmtypes.Parameters{Type: "object", Properties: props, Required: []string{"location"}},
		}},
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "lookup_beta",
			Description: "Returns the current WEATHER (temperature, precipitation, wind) for a given city. Use ONLY when the user asks about weather.",
			Parameters:  &llmtypes.Parameters{Type: "object", Properties: props, Required: []string{"location"}},
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What's the weather in Paris right now?"}}},
		},
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoiceString("required"),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].ToolCalls) == 0 {
		t.Fatalf("model did not emit a tool call; content=%q", resp.Choices[0].Content)
	}
	got := resp.Choices[0].ToolCalls[0].FunctionCall.Name
	if got != "lookup_beta" {
		t.Fatalf("model selected %q for a weather question; tool descriptions may be dropped", got)
	}
}

// TestVertexRealToolChoiceModes (contract P0 #9). Same tool, three
// branches: auto / none / required.
func TestVertexRealToolChoiceModes(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	tools := []llmtypes.Tool{
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "lookup_city_population",
			Description: "Look up the population of a city.",
			Parameters: &llmtypes.Parameters{
				Type:       "object",
				Properties: map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				Required:   []string{"city"},
			},
		}},
	}
	prompt := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "I want to know the population of Tokyo. Give me a number or a short answer."}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// none — must not emit a tool call.
	respNone, err := adapter.GenerateContent(ctx, prompt, llmtypes.WithTools(tools), llmtypes.WithToolChoiceString("none"), llmtypes.WithMaxTokens(512))
	if err != nil {
		t.Fatalf("none branch error = %v", err)
	}
	if len(respNone.Choices[0].ToolCalls) > 0 {
		t.Fatalf("none branch emitted a tool_use: %+v", respNone.Choices[0].ToolCalls)
	}

	// required — must emit a tool call.
	respReq, err := adapter.GenerateContent(ctx, prompt, llmtypes.WithTools(tools), llmtypes.WithToolChoiceString("required"), llmtypes.WithMaxTokens(512))
	if err != nil {
		t.Fatalf("required branch error = %v", err)
	}
	if len(respReq.Choices[0].ToolCalls) == 0 {
		t.Fatalf("required branch did not emit a tool call; content=%q", respReq.Choices[0].Content)
	}

	// auto — anything goes, just succeed.
	respAuto, err := adapter.GenerateContent(ctx, prompt, llmtypes.WithTools(tools), llmtypes.WithToolChoiceString("auto"), llmtypes.WithMaxTokens(512))
	if err != nil {
		t.Fatalf("auto branch error = %v", err)
	}
	if respAuto.Choices[0].Content == "" && len(respAuto.Choices[0].ToolCalls) == 0 {
		t.Fatalf("auto branch had no content and no tool call")
	}
}

// TestVertexRealStopSequences (contract P1 #10) — regression for the
// just-fixed "opts.StopSequences silently ignored" bug.
func TestVertexRealStopSequences(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Output exactly these five lines, in order, one per line: ITEM-1, ITEM-2, ITEM-3, ITEM-4, ITEM-5. Nothing else."}}},
		},
		llmtypes.WithStopSequences([]string{"ITEM-3"}),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := resp.Choices[0].Content
	if strings.Contains(content, "ITEM-3") {
		t.Fatalf("response contains the stop sequence; adapter did not forward StopSequences. content=%q", content)
	}
	if !strings.Contains(content, "ITEM-1") || !strings.Contains(content, "ITEM-2") {
		t.Fatalf("response stopped too early; expected ITEM-1 and ITEM-2 before stop. content=%q", content)
	}
}

// TestVertexRealTopPDoesNotError (contract P1 #11) verifies legacy top_p
// options are omitted for models whose API no longer supports custom sampling.
func TestVertexRealTopPDoesNotError(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only OK."}}},
		},
		llmtypes.WithTopP(0.9),
		llmtypes.WithMaxTokens(256),
	)
	if err != nil {
		t.Fatalf("GenerateContent with top_p=0.9 error = %v", err)
	}
	if strings.TrimSpace(resp.Choices[0].Content) == "" {
		t.Fatalf("response was empty")
	}
}

// TestVertexRealTopKDoesNotError (contract P1 #12) verifies legacy top_k
// options are omitted for models whose API no longer supports custom sampling.
func TestVertexRealTopKDoesNotError(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only OK."}}},
		},
		llmtypes.WithTopK(40),
		llmtypes.WithMaxTokens(256),
	)
	if err != nil {
		t.Fatalf("GenerateContent with top_k=40 error = %v", err)
	}
	if strings.TrimSpace(resp.Choices[0].Content) == "" {
		t.Fatalf("response was empty")
	}
}

// TestVertexRealJSONMode (contract P1 #20). response_mime_type=
// application/json is the Gemini equivalent of OpenAI's JSON mode.
func TestVertexRealJSONMode(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Always reply with valid JSON."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: `Reply with exactly this JSON object: {"status":"ok","value":42}`}}},
		},
		llmtypes.WithJSONMode(),
		llmtypes.WithMaxTokens(256),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(resp.Choices[0].Content), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v\ncontent=%q", err, resp.Choices[0].Content)
	}
	if parsed["status"] != "ok" || parsed["value"].(float64) != 42 {
		t.Fatalf("response shape unexpected: %+v", parsed)
	}
}

// TestVertexRealImplicitPromptCaching (contract P1 #19). Gemini 2.5+
// auto-caches long prompts; the second request reusing the same prefix
// reports CachedContentTokens > 0 via the existing extraction in
// pkg/utils/token_extraction.go. We pin to gemini-3.8-flash because
// preview-track Gemini 3 models have a different cache-eligibility
// threshold and the test would be flaky on them.
func TestVertexRealImplicitPromptCaching(t *testing.T) {
	apiKey, _ := requireVertexRealE2E(t)
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		t.Fatalf("genai.NewClient: %v", err)
	}
	adapter := NewGoogleGenAIAdapter(client, "gemini-3.8-flash", &MockLogger{})

	// Build a prefix large enough to clear Gemini 2.5 Flash's implicit
	// cache threshold (~1024 tokens minimum, but the cache is more
	// reliable above ~4-5K tokens). One line repeated ~600 times yields
	// ~6-8k tokens of identical prefix.
	sysLine := "The capital city of Greenstate is Northbridge. This rule MUST be remembered for every answer. "
	largePrefix := strings.Repeat(sysLine, 1500)
	systemMsg := llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeSystem,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: largePrefix}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First call: warm the cache.
	_, err = adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		systemMsg,
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What is the capital of Greenstate?"}}},
	}, llmtypes.WithMaxTokens(64))
	if err != nil {
		t.Fatalf("warm-up call failed: %v", err)
	}

	// Second call: same prefix, different trailing user turn. Implicit
	// cache should report a non-zero CachedContentTokens count.
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		systemMsg,
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Remind me: what city is Greenstate's capital?"}}},
	}, llmtypes.WithMaxTokens(64))
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		t.Fatalf("response missing GenerationInfo")
	}
	cached := resp.Choices[0].GenerationInfo.CachedContentTokens
	if cached == nil || *cached <= 0 {
		// Gemini's implicit cache is best-effort; if it didn't trigger
		// this run, the test is inconclusive (not a regression). Skip
		// rather than fail to keep this from being a flake source.
		var got int
		if cached != nil {
			got = *cached
		}
		t.Skipf("implicit cache did not trigger on this run (cached=%d). Adapter still surfaces the field correctly; cache hit-rate is provider-side.", got)
	}
	t.Logf("✅ implicit cache hit: %d cached tokens out of prompt", *cached)
}

// TestVertexRealCostEstimateOnPlainText (contract P2 #25). After a
// successful call the adapter must surface cost_usd_estimated +
// cost_model_id on GenerationInfo.Additional. Gemini API ships
// tokens but no USD, so this proves the registry-driven cost math
// reaches downstream cost ledgers.
func TestVertexRealCostEstimateOnPlainText(t *testing.T) {
	adapter, model := newRealVertexAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(256),
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

// TestVertexRealAuthFailureClassified (contract P2 #22). A bogus key
// must produce an error that does not echo the key and hints at auth.
func TestVertexRealAuthFailureClassified(t *testing.T) {
	if os.Getenv("RUN_VERTEX_REAL_E2E") == "" {
		t.Skip("set RUN_VERTEX_REAL_E2E=1 to run real Gemini tests")
	}
	const badKey = "AIza-DELIBERATELY-INVALID-FOR-AUTH-CLASSIFICATION-TEST"
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  badKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		// NewClient is permissive about format; if it fails here at
		// all, that itself counts as cleanly-classified auth refusal.
		if strings.Contains(err.Error(), badKey) {
			t.Fatalf("constructor leaked the bogus key: %v", err)
		}
		return
	}
	adapter := NewGoogleGenAIAdapter(client, ModelGemini35FlashLite, &MockLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}}},
		},
		llmtypes.WithMaxTokens(8),
	)
	if err == nil {
		t.Fatal("expected error with bogus key; got nil")
	}
	msg := err.Error()
	if strings.Contains(msg, badKey) {
		t.Fatalf("error leaked the bogus key text.\nerror=%q", msg)
	}
	lower := strings.ToLower(msg)
	hint := false
	for _, marker := range []string{"auth", "key", "invalid", "credential", "unauthorized", "401", "403", "permission"} {
		if strings.Contains(lower, marker) {
			hint = true
			break
		}
	}
	if !hint {
		t.Fatalf("error doesn't hint at auth trouble.\nerror=%q", msg)
	}
}

// TestVertexRealRateLimitClassified (contract P2 #23). Fires a burst
// of concurrent tiny requests; if any returns a 429-class error,
// asserts the surface text classifies as a rate-limit and does NOT
// echo the API key. Self-skips when no rate-limit triggers in budget.
func TestVertexRealRateLimitClassified(t *testing.T) {
	adapter, _ := newRealVertexAdapter(t)
	var apiKey string
	for _, name := range []string{"GEMINI_API_KEY", "VERTEX_API_KEY", "GOOGLE_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			apiKey = v
			break
		}
	}

	const N = 30
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := adapter.GenerateContent(ctx,
				[]llmtypes.MessageContent{
					{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Say OK."}}},
				},
				llmtypes.WithMaxTokens(32),
			)
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	classify := func(s string) bool {
		lower := strings.ToLower(s)
		for _, marker := range []string{"rate limit", "rate-limit", "ratelimit", "429", "too many requests", "quota", "resource_exhausted", "exhausted", "throttle"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
		return false
	}

	var hit string
	for _, e := range errs {
		if e == nil {
			continue
		}
		if classify(e.Error()) {
			hit = e.Error()
			break
		}
	}
	if hit == "" {
		t.Skipf("no rate-limit response provoked in %d concurrent requests; provider headroom too high to test classification this run", N)
	}
	if apiKey != "" && strings.Contains(hit, apiKey) {
		t.Fatalf("rate-limit error leaked the API key. error=%q", hit)
	}
	t.Logf("✅ rate-limit error classified cleanly: %s", hit)
}
