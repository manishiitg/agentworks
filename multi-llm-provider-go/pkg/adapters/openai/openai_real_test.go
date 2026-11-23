package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Real-API e2e tests for the OpenAI adapter.
//
// Gate: RUN_OPENAI_REAL_E2E=1 + OPENAI_API_KEY. Model override via
// OPENAI_REAL_E2E_MODEL (defaults to gpt-5.4-mini, the cheapest current
// "mini" SKU on the 5.4 line — supports tool calls, JSON mode, and
// reasoning effort).
//
// Reference for shape: pkg/adapters/anthropic/anthropic_real_test.go.

func requireOpenAIRealE2E(t *testing.T) (apiKey, model string) {
	t.Helper()
	if os.Getenv("RUN_OPENAI_REAL_E2E") == "" {
		t.Skip("set RUN_OPENAI_REAL_E2E=1 to run real OpenAI Chat Completions tests")
	}
	apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("set OPENAI_API_KEY to run real OpenAI tests")
	}
	model = strings.TrimSpace(os.Getenv("OPENAI_REAL_E2E_MODEL"))
	if model == "" {
		model = "gpt-5.4-mini"
	}
	return apiKey, model
}

func newRealOpenAIAdapter(t *testing.T) (*OpenAIAdapter, string) {
	t.Helper()
	apiKey, model := requireOpenAIRealE2E(t)
	client := openai.NewClient(openaioption.WithAPIKey(apiKey))
	return NewOpenAIAdapter(&client, model, &MockLogger{}), model
}

// newRealOpenAINonReasoningAdapter builds an adapter targeting a
// non-reasoning chat model. OpenAI's reasoning-class models (the
// gpt-5.x family) reject `stop`, `top_p`, and several tool_choice
// values with "Unsupported parameter" errors, so contract tests that
// exercise those params must run against a chat-class model.
//
// Override via OPENAI_NONREASONING_MODEL. Defaults to gpt-4.1-nano,
// the cheapest non-reasoning model in the public catalog that still
// supports tools, JSON mode, and the full sampling-control surface.
func newRealOpenAINonReasoningAdapter(t *testing.T) (*OpenAIAdapter, string) {
	t.Helper()
	apiKey, _ := requireOpenAIRealE2E(t)
	model := strings.TrimSpace(os.Getenv("OPENAI_NONREASONING_MODEL"))
	if model == "" {
		model = "gpt-4.1-nano"
	}
	client := openai.NewClient(openaioption.WithAPIKey(apiKey))
	return NewOpenAIAdapter(&client, model, &MockLogger{}), model
}

// TestOpenAIRealPlainText is the contract-P0-#1 smoke. Proves the API
// key, request shape, and minimal response parsing all work end-to-end.
func TestOpenAIRealPlainText(t *testing.T) {
	adapter, _ := newRealOpenAIAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly the single word OK."}}},
		},
		llmtypes.WithMaxTokens(16),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("no choices in response")
	}
	got := strings.ToUpper(strings.TrimSpace(resp.Choices[0].Content))
	if !strings.Contains(got, "OK") {
		t.Fatalf("response did not contain OK: %q", resp.Choices[0].Content)
	}
}

// TestOpenAIRealToolDescriptionInfluencesSelection is the contract-P0-#8
// regression test. Two confusable tool names + very different
// descriptions: the model must pick by description.
func TestOpenAIRealToolDescriptionInfluencesSelection(t *testing.T) {
	adapter, _ := newRealOpenAIAdapter(t)

	props := map[string]interface{}{
		"location": map[string]interface{}{"type": "string", "description": "City or place"},
	}
	tools := []llmtypes.Tool{
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "lookup_alpha",
				Description: "Returns the current local TIME in a given city. Use ONLY when the user asks about time/hours.",
				Parameters:  &llmtypes.Parameters{Type: "object", Properties: props, Required: []string{"location"}},
			},
		},
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "lookup_beta",
				Description: "Returns the current WEATHER (temperature, precipitation) for a given city. Use ONLY when the user asks about weather.",
				Parameters:  &llmtypes.Parameters{Type: "object", Properties: props, Required: []string{"location"}},
			},
		},
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

// TestOpenAIRealToolChoiceModes is the contract-P0-#9 test for
// auto / none / required. Same tool, three branches. We run against a
// non-reasoning model because OpenAI's reasoning-class models can
// override tool_choice=none and emit a tool anyway, and the contract
// is testing the wire-level translation rather than per-model
// behavior.
func TestOpenAIRealToolChoiceModes(t *testing.T) {
	adapter, _ := newRealOpenAINonReasoningAdapter(t)
	tools := []llmtypes.Tool{
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "lookup_city_population",
				Description: "Look up the population of a city.",
				Parameters: &llmtypes.Parameters{
					Type:       "object",
					Properties: map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
					Required:   []string{"city"},
				},
			},
		},
	}
	prompt := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What is the population of Tokyo? Use the tool if you have it."}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// none — no tool calls allowed.
	respNone, err := adapter.GenerateContent(ctx, prompt, llmtypes.WithTools(tools), llmtypes.WithToolChoiceString("none"), llmtypes.WithMaxTokens(120))
	if err != nil {
		t.Fatalf("none branch error = %v", err)
	}
	if len(respNone.Choices[0].ToolCalls) > 0 {
		t.Fatalf("none branch emitted a tool_use: %+v", respNone.Choices[0].ToolCalls)
	}

	// required — must emit a tool call.
	respReq, err := adapter.GenerateContent(ctx, prompt, llmtypes.WithTools(tools), llmtypes.WithToolChoiceString("required"), llmtypes.WithMaxTokens(120))
	if err != nil {
		t.Fatalf("required branch error = %v", err)
	}
	if len(respReq.Choices[0].ToolCalls) == 0 {
		t.Fatalf("required branch did not emit a tool call; content=%q", respReq.Choices[0].Content)
	}

	// auto — anything goes, just succeed.
	respAuto, err := adapter.GenerateContent(ctx, prompt, llmtypes.WithTools(tools), llmtypes.WithToolChoiceString("auto"), llmtypes.WithMaxTokens(120))
	if err != nil {
		t.Fatalf("auto branch error = %v", err)
	}
	if respAuto.Choices[0].Content == "" && len(respAuto.Choices[0].ToolCalls) == 0 {
		t.Fatalf("auto branch had no content and no tool call")
	}
}

// TestOpenAIRealStopSequences (contract P1 #10) is the regression test
// for the just-fixed bug where opts.StopSequences was defined on
// CallOptions but never reached params.Stop. Before the fix, this
// test would have produced "ITEM-3" in the output. After the fix,
// it must terminate before that sequence.
//
// Runs against a non-reasoning model because OpenAI's reasoning-class
// models reject `stop` outright ("Unsupported parameter: 'stop' is
// not supported with this model"). The adapter still forwards stop
// when the caller provides it — it's the model that refuses — so we
// pin a chat-class model here to exercise the wire path.
func TestOpenAIRealStopSequences(t *testing.T) {
	adapter, _ := newRealOpenAINonReasoningAdapter(t)
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

// TestOpenAIRealTopPDoesNotError (contract P1 #11) is the regression
// test for the second half of the same fix. top_p was defined on
// CallOptions but never assigned to params.TopP. We can't
// deterministically assert distribution from one call, so this test
// asserts the request shape is accepted and produces output.
func TestOpenAIRealTopPDoesNotError(t *testing.T) {
	adapter, _ := newRealOpenAIAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only OK."}}},
		},
		llmtypes.WithTopP(0.9),
		llmtypes.WithMaxTokens(16),
	)
	if err != nil {
		t.Fatalf("GenerateContent with top_p=0.9 error = %v", err)
	}
	if strings.TrimSpace(resp.Choices[0].Content) == "" {
		t.Fatalf("response was empty")
	}
}

// TestOpenAIRealReasoningEffort (contract P1 #17). gpt-5.x supports
// the reasoning_effort knob. Setting it explicitly must pass through
// to the API and produce a successful response. We can't directly
// observe a thinking block on the wire for Chat Completions, so we
// just verify the request is accepted.
func TestOpenAIRealReasoningEffort(t *testing.T) {
	adapter, _ := newRealOpenAIAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What is 17 * 23? Reply with only the integer."}}},
		},
		llmtypes.WithReasoningEffort("low"),
		llmtypes.WithMaxTokens(64),
	)
	if err != nil {
		t.Fatalf("reasoning_effort=low: GenerateContent error = %v", err)
	}
	if !strings.Contains(resp.Choices[0].Content, "391") {
		t.Fatalf("model didn't compute 17*23=391; content=%q", resp.Choices[0].Content)
	}
}

// TestOpenAIRealJSONMode (contract P1 #20). Setting JSONMode must
// produce a response whose content parses as valid JSON.
func TestOpenAIRealJSONMode(t *testing.T) {
	adapter, _ := newRealOpenAIAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Always reply with valid JSON."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: `Reply with exactly the JSON object: {"status":"ok","value":42}`}}},
		},
		llmtypes.WithJSONMode(),
		llmtypes.WithMaxTokens(64),
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

// TestOpenAIRealJSONSchemaStrict (contract P1 #21). With a strict JSON
// schema attached, the response MUST conform — required keys present,
// no extras.
func TestOpenAIRealJSONSchemaStrict(t *testing.T) {
	adapter, _ := newRealOpenAIAdapter(t)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"city":       map[string]interface{}{"type": "string"},
			"population": map[string]interface{}{"type": "integer"},
		},
		"required":             []interface{}{"city", "population"},
		"additionalProperties": false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Return the population of Tokyo as a structured object. Use any reasonable estimate."}}},
		},
		llmtypes.WithJSONSchema(schema, "city_population", "City with population", true),
		llmtypes.WithMaxTokens(80),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(resp.Choices[0].Content), &parsed); err != nil {
		t.Fatalf("strict JSON Schema response not valid JSON: %v\ncontent=%q", err, resp.Choices[0].Content)
	}
	if _, ok := parsed["city"]; !ok {
		t.Fatalf("strict JSON Schema response missing 'city': %+v", parsed)
	}
	if _, ok := parsed["population"]; !ok {
		t.Fatalf("strict JSON Schema response missing 'population': %+v", parsed)
	}
	// With additionalProperties:false + strict:true, the response
	// should have exactly these two keys.
	for k := range parsed {
		if k != "city" && k != "population" {
			t.Errorf("strict mode allowed extra key %q: %+v", k, parsed)
		}
	}
}

// TestOpenAIRealPromptCachingCacheRead (contract P1 #19). OpenAI auto-
// caches prefixes of ≥1024 tokens with no per-request markers — the
// caller just sends the same prefix twice and the second call sees a
// non-zero cached_tokens in usage. The adapter must surface that as
// CachedContentTokens or GenerationInfo.Additional.
func TestOpenAIRealPromptCachingCacheRead(t *testing.T) {
	adapter, _ := newRealOpenAIAdapter(t)

	// ~6.5k tokens of stable text to safely exceed OpenAI's 1024-token
	// minimum cache prefix. Content must be deterministic so the
	// cache key matches between calls.
	const sysLine = "This is a stable instructional paragraph used to fill an OpenAI cacheable prefix. " +
		"It establishes the operating constraints and persona for the assistant. " +
		"It must be long enough to comfortably exceed OpenAI's 1024-token minimum prefix for prompt caching. "
	systemPrompt := strings.Repeat(sysLine, 100)
	prompt := func() []llmtypes.MessageContent {
		return []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only OK."}}},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Warm the cache.
	if _, err := adapter.GenerateContent(ctx, prompt(), llmtypes.WithMaxTokens(16)); err != nil {
		t.Fatalf("first call error = %v", err)
	}

	// Second call must hit the cache. The OpenAI adapter exposes the
	// cached prompt tokens via usage.PromptTokensDetails.CachedTokens
	// → GenerationInfo.CachedContentTokens.
	resp, err := adapter.GenerateContent(ctx, prompt(), llmtypes.WithMaxTokens(16))
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}
	if resp.Choices[0].GenerationInfo == nil {
		t.Fatalf("response missing GenerationInfo")
	}
	if resp.Choices[0].GenerationInfo.CachedContentTokens == nil || *resp.Choices[0].GenerationInfo.CachedContentTokens <= 0 {
		t.Fatalf("second call did not see prompt-caching hit; CachedContentTokens=%v Additional=%v",
			resp.Choices[0].GenerationInfo.CachedContentTokens, resp.Choices[0].GenerationInfo.Additional)
	}
	t.Logf("cache_read=%d tokens", *resp.Choices[0].GenerationInfo.CachedContentTokens)
}

// TestOpenAIRealPDFInputDocumentReceived (contract P1 #15). Proves
// that DocumentContent → file content-part conversion in the adapter
// reaches OpenAI's Chat Completions API and the model actually reads
// from the inline PDF. We embed a unique marker inside a tiny
// hand-rolled PDF stream and require the model to echo it back. If
// the adapter ever drops DocumentContent parts again, the model will
// respond "I don't see a document attached" and the assertion fails.
// Uses gpt-4.1-mini (the cheapest current chat model that supports
// file input).
func TestOpenAIRealPDFInputDocumentReceived(t *testing.T) {
	apiKey, _ := requireOpenAIRealE2E(t)
	model := strings.TrimSpace(os.Getenv("OPENAI_PDF_MODEL"))
	if model == "" {
		model = "gpt-4.1-mini"
	}
	client := openai.NewClient(openaioption.WithAPIKey(apiKey))
	adapter := NewOpenAIAdapter(&client, model, &MockLogger{})

	marker := "OAI_PDF_MARKER_" + time.Now().Format("150405")
	pdfBytes := minimalPDFWithMarker(marker)
	encoded := base64.StdEncoding.EncodeToString(pdfBytes)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.DocumentContent{
						SourceType: "base64",
						MediaType:  "application/pdf",
						Data:       encoded,
						Title:      "marker-doc.pdf",
					},
					llmtypes.TextContent{Text: "The attached PDF contains a single marker token that begins 'OAI_PDF_MARKER_'. Reply with ONLY that full token — no other words."},
				},
			},
		},
		llmtypes.WithMaxTokens(64),
	)
	if err != nil {
		t.Fatalf("GenerateContent with PDF document error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("response had no choices")
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, marker) {
		t.Fatalf("response missing PDF marker %q (DocumentContent likely dropped). content=%q", marker, content)
	}
}

// minimalPDFWithMarker builds a tiny but syntactically valid PDF whose
// only printable text is a caller-supplied marker. Same shape as the
// helper in the Anthropic suite — kept package-local so each adapter
// test file compiles standalone.
func minimalPDFWithMarker(marker string) []byte {
	header := "%PDF-1.4\n%\xE2\xE3\xCF\xD3\n"
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Count 1/Kids[3 0 R]>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>",
		"",
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
	}
	stream := "BT /F1 24 Tf 72 720 Td (" + marker + ") Tj ET"
	objs[3] = "<</Length " + iToA(len(stream)) + ">>\nstream\n" + stream + "\nendstream"

	var b strings.Builder
	b.WriteString(header)
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = b.Len()
		b.WriteString(iToA(i+1) + " 0 obj\n" + body + "\nendobj\n")
	}
	xrefOff := b.Len()
	b.WriteString("xref\n0 ")
	b.WriteString(iToA(len(objs) + 1))
	b.WriteString("\n0000000000 65535 f \n")
	for _, off := range offsets[1:] {
		b.WriteString(pad10(off) + " 00000 n \n")
	}
	b.WriteString("trailer\n<</Size ")
	b.WriteString(iToA(len(objs) + 1))
	b.WriteString("/Root 1 0 R>>\nstartxref\n")
	b.WriteString(iToA(xrefOff))
	b.WriteString("\n%%EOF\n")
	return []byte(b.String())
}

func pad10(n int) string {
	s := iToA(n)
	if len(s) >= 10 {
		return s
	}
	return strings.Repeat("0", 10-len(s)) + s
}

func iToA(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestOpenAIRealCostEstimateOnPlainText (contract P2 #25). The
// adapter must surface a non-zero cost_usd_estimated and cost_model_id
// on GenerationInfo.Additional after a successful call. OpenAI's API
// ships tokens but no USD, so this proves the registry-driven cost
// computation reaches downstream cost ledgers.
func TestOpenAIRealCostEstimateOnPlainText(t *testing.T) {
	adapter, model := newRealOpenAIAdapter(t)
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

// TestOpenAIRealAuthFailureClassified (contract P2 #22). A bogus key
// must produce an error that does NOT echo the key back and that
// hints at auth/credential trouble.
func TestOpenAIRealAuthFailureClassified(t *testing.T) {
	if os.Getenv("RUN_OPENAI_REAL_E2E") == "" {
		t.Skip("set RUN_OPENAI_REAL_E2E=1 to run real OpenAI tests")
	}
	const badKey = "sk-DELIBERATELY-INVALID-FOR-AUTH-CLASSIFICATION-TEST"
	client := openai.NewClient(openaioption.WithAPIKey(badKey))
	adapter := NewOpenAIAdapter(&client, "gpt-5.4-mini", &MockLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := adapter.GenerateContent(ctx,
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
		t.Fatalf("error leaked the bogus key text — a real user's mis-pasted key would leak through this surface.\nerror=%q", msg)
	}
	lower := strings.ToLower(msg)
	hint := false
	for _, marker := range []string{"auth", "key", "invalid", "credential", "unauthorized", "401"} {
		if strings.Contains(lower, marker) {
			hint = true
			break
		}
	}
	if !hint {
		t.Fatalf("error doesn't hint at auth trouble — user won't know to recheck key.\nerror=%q", msg)
	}
}

// TestOpenAIRealRateLimitClassified (contract P2 #23). Fires a burst
// of tiny requests in parallel; if any returns a 429 / quota /
// throttle error, assert it is classifiable and does NOT echo the
// API key. If no rate-limit response triggers inside the budget,
// self-skip rather than fail (provider headroom is not under our
// control).
func TestOpenAIRealRateLimitClassified(t *testing.T) {
	adapter, _ := newRealOpenAINonReasoningAdapter(t)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))

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
				llmtypes.WithMaxTokens(8),
			)
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	classify := func(s string) bool {
		lower := strings.ToLower(s)
		for _, marker := range []string{"rate limit", "rate-limit", "ratelimit", "429", "too many requests", "quota", "throttle"} {
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
