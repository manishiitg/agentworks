package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// requireAnthropicRealE2E gates every real-API test in this file on
//   - RUN_ANTHROPIC_REAL_E2E=1 (opt-in flag, so CI without the key does not
//     accidentally bill)
//   - ANTHROPIC_API_KEY (the secret itself)
//
// Tests skip cleanly when either is missing.
func requireAnthropicRealE2E(t *testing.T) (string, string) {
	t.Helper()
	if os.Getenv("RUN_ANTHROPIC_REAL_E2E") == "" {
		t.Skip("set RUN_ANTHROPIC_REAL_E2E=1 to run real Anthropic Messages API tests")
	}
	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		t.Skip("set ANTHROPIC_API_KEY to run real Anthropic Messages API tests")
	}
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_REAL_E2E_MODEL"))
	if model == "" {
		// Haiku 4.5 is the fastest + cheapest current Anthropic model
		// and is the default for these tests. Override with
		// ANTHROPIC_REAL_E2E_MODEL when validating a different model.
		model = ModelClaudeHaiku45
	}
	return key, model
}

func newRealAnthropicAdapter(t *testing.T) (*AnthropicAdapter, string) {
	t.Helper()
	key, model := requireAnthropicRealE2E(t)
	client := anthropic.NewClient(anthropicoption.WithAPIKey(key))
	return NewAnthropicAdapter(client, model, &MockLogger{}), model
}

// TestAnthropicRealToolDescriptionInfluencesSelection is the regression
// test for the dropped-tool-description bug. Before the fix, convertTools
// silently discarded tool.Function.Description, so Claude only saw the
// tool name + input schema. We construct TWO tools with intentionally
// confusable names and rely on the description to disambiguate which one
// Claude must call. If descriptions ever stop being forwarded, the model
// will fall back to alphabetical / name-similarity heuristics and pick
// the wrong tool, failing the assertion.
func TestAnthropicRealToolDescriptionInfluencesSelection(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	weatherProperties := map[string]interface{}{
		"location": map[string]interface{}{
			"type":        "string",
			"description": "The city or place to look up.",
		},
	}
	timeProperties := map[string]interface{}{
		"location": map[string]interface{}{
			"type":        "string",
			"description": "The city or place to look up.",
		},
	}

	tools := []llmtypes.Tool{
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name: "lookup_alpha",
				// Description is the only signal Claude has to decide
				// this is the time tool.
				Description: "Returns the current local time in a given city. Use ONLY when the user asks about time, hours, or what time it is.",
				Parameters: &llmtypes.Parameters{
					Type:       "object",
					Properties: timeProperties,
					Required:   []string{"location"},
				},
			},
		},
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name: "lookup_beta",
				// Description is the only signal Claude has to decide
				// this is the weather tool.
				Description: "Returns the current weather conditions (temperature, precipitation, wind) for a given city. Use ONLY when the user asks about weather or temperature.",
				Parameters: &llmtypes.Parameters{
					Type:       "object",
					Properties: weatherProperties,
					Required:   []string{"location"},
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ask a weather question. With descriptions forwarded, Claude must
	// call lookup_beta. Without them, the model has no semantic signal
	// at all — both tools have identical signatures and confusable
	// names — so it picks effectively at random.
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What's the weather like in Paris right now?"}},
			},
		},
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoiceString("required"),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("response had no choices")
	}
	toolCalls := resp.Choices[0].ToolCalls
	if len(toolCalls) == 0 {
		t.Fatalf("model did not emit any tool_use; content=%q", resp.Choices[0].Content)
	}

	got := toolCalls[0].FunctionCall.Name
	if got != "lookup_beta" {
		t.Fatalf("model selected %q for a weather question; tool description was likely dropped (lookup_beta should be the weather tool)", got)
	}
}

// TestAnthropicRealExtendedThinking proves extended thinking is fully
// plumbed: we set ThinkingBudget on a reasoning-class request, ask a
// question that benefits from chain-of-thought, and verify
//
//	(a) the model returns at least one thinking block (signed with a
//	    non-empty signature so it can be re-fed on the next turn), and
//	(b) the final answer still contains a meaningful response, and
//	(c) GenerationInfo.Additional surfaces the thinking text + signatures
//	    so callers can re-feed them on follow-up turns.
//
// Regression-locks the fix for the previously-missing thinking plumbing.
func TestAnthropicRealExtendedThinking(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Three positive integers a, b, c satisfy a + b + c = 17 and a^2 + b^2 + c^2 = 105. Find all triples (a,b,c). Think step by step and then state the answer on a final line beginning with ANSWER:."}},
			},
		},
		llmtypes.WithThinkingBudget(2048),
		llmtypes.WithMaxTokens(8192),
	)
	if err != nil {
		t.Fatalf("GenerateContent with thinking error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("response had no choices")
	}
	choice := resp.Choices[0]
	if choice.GenerationInfo == nil || choice.GenerationInfo.Additional == nil {
		t.Fatalf("expected GenerationInfo.Additional to be populated; got %+v", choice.GenerationInfo)
	}
	thinkingRaw, hasThinking := choice.GenerationInfo.Additional["thinking"]
	if !hasThinking {
		t.Fatalf("response missing thinking block in Additional; ThinkingBudget was 2048 but no chain-of-thought returned")
	}
	thinking, _ := thinkingRaw.(string)
	if strings.TrimSpace(thinking) == "" {
		t.Fatalf("thinking key present but empty: %q", thinking)
	}
	sigsRaw, hasSigs := choice.GenerationInfo.Additional["thinking_signatures"]
	sigs, _ := sigsRaw.([]string)
	if !hasSigs || len(sigs) == 0 || strings.TrimSpace(sigs[0]) == "" {
		t.Fatalf("expected at least one signed thinking block for re-feed on follow-up turns; got %v", sigsRaw)
	}
	if !strings.Contains(strings.ToUpper(choice.Content), "ANSWER:") {
		t.Fatalf("final user-facing content missing ANSWER: marker; thinking-mode may have eaten the response text. content=%q", choice.Content)
	}
}

// TestAnthropicRealInterleavedThinkingWithTools combines extended
// thinking with a tool call to prove the interleaved-thinking beta is
// actually opted-into and accepted by the API. Without the beta token
// the request would 400 with a different error ("interleaved-thinking
// header required for thinking + tools"); the test passing is itself
// the assertion the header engaged.
//
// Note: Anthropic disallows `tool_choice: required` together with
// thinking ("Thinking may not be enabled when tool_choice forces tool
// use."), so this test uses `tool_choice: auto` and frames the prompt
// to make tool use the obvious answer.
func TestAnthropicRealInterleavedThinkingWithTools(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	tools := []llmtypes.Tool{
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "evaluate_expression",
				Description: "Evaluates a simple arithmetic expression and returns the numeric result. ALWAYS call this tool for any arithmetic computation; never compute mentally.",
				Parameters: &llmtypes.Parameters{
					Type: "object",
					Properties: map[string]interface{}{
						"expression": map[string]interface{}{
							"type":        "string",
							"description": "A pure arithmetic expression, e.g. '17*23'.",
						},
					},
					Required: []string{"expression"},
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeSystem,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "You have access to the evaluate_expression tool. You MUST use it for any arithmetic. Do not compute results yourself."}},
			},
			{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What is 23 * 17? Use the tool."}},
			},
		},
		llmtypes.WithThinkingBudget(2048),
		llmtypes.WithMaxTokens(8192),
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoiceString("auto"),
	)
	if err != nil {
		t.Fatalf("GenerateContent (thinking + tools) error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("response had no choices")
	}
	choice := resp.Choices[0]
	if len(choice.ToolCalls) == 0 {
		t.Fatalf("model did not emit any tool_use; thinking+tools path likely broken. content=%q", choice.Content)
	}
	if choice.ToolCalls[0].FunctionCall.Name != "evaluate_expression" {
		t.Fatalf("model called %q, want evaluate_expression", choice.ToolCalls[0].FunctionCall.Name)
	}
	if choice.GenerationInfo == nil || choice.GenerationInfo.Additional == nil {
		t.Fatalf("expected GenerationInfo.Additional populated")
	}
	if _, hasThinking := choice.GenerationInfo.Additional["thinking"]; !hasThinking {
		t.Fatalf("interleaved-thinking + tools turn returned no thinking block; the interleaved beta may not have engaged")
	}
}

// TestAnthropicRealStopSequences proves the adapter forwards
// StopSequences to the Messages API: we ask Claude to produce a list
// where each line is "ITEM-N", then set "ITEM-3" as a stop sequence.
// The response must therefore terminate before "ITEM-3" appears.
//
// Stop sequences are the easiest sampling control to assert
// behaviorally — top_p / top_k only change the *distribution* of
// tokens, which is hard to test deterministically with one call.
func TestAnthropicRealStopSequences(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Output exactly these five lines, in order, one per line: ITEM-1, ITEM-2, ITEM-3, ITEM-4, ITEM-5. Nothing else."}},
			},
		},
		llmtypes.WithStopSequences([]string{"ITEM-3"}),
	)
	if err != nil {
		t.Fatalf("GenerateContent with stop sequences error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("response had no choices")
	}
	content := resp.Choices[0].Content
	if strings.Contains(content, "ITEM-3") {
		t.Fatalf("response contains the stop sequence; adapter did not forward StopSequences. content=%q", content)
	}
	// ITEM-1 and ITEM-2 should appear (they precede the stop).
	if !strings.Contains(content, "ITEM-1") || !strings.Contains(content, "ITEM-2") {
		t.Fatalf("response stopped too early; expected ITEM-1 and ITEM-2 before stop. content=%q", content)
	}
	// stop_reason should reflect the stop sequence path.
	stop := resp.Choices[0].StopReason
	if stop == "" {
		t.Logf("warning: empty StopReason on stop-sequence termination")
	}
}

// TestAnthropicRealTopPDoesNotError exercises the top_p plumbing. We
// can't deterministically assert behavioral changes from one call, so
// the test just proves the parameter is accepted by the API (the
// adapter forwards it, the SDK marshals it, the server validates it).
func TestAnthropicRealTopPDoesNotError(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with the single word OK."}}},
		},
		llmtypes.WithTopP(0.9),
		llmtypes.WithMaxTokens(16),
	)
	if err != nil {
		t.Fatalf("GenerateContent with top_p=0.9 error = %v", err)
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Content) == "" {
		t.Fatalf("response with top_p was empty")
	}
}

// minimalPDFWithMarker returns the raw bytes of a tiny valid PDF whose
// only printable text is a caller-supplied marker. Generated inline so
// the test doesn't rely on an external fixture file. The xref offsets
// are computed dynamically (rather than hardcoded) so the PDF actually
// parses; lenient parsers tolerate wrong offsets, but Claude's parser
// is strict enough to reject malformed cross-reference tables.
func minimalPDFWithMarker(marker string) []byte {
	header := "%PDF-1.4\n%\xE2\xE3\xCF\xD3\n"
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Count 1/Kids[3 0 R]>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>",
		"", // placeholder — content stream object filled in below.
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
	}
	stream := "BT /F1 24 Tf 72 720 Td (" + marker + ") Tj ET"
	objs[3] = "<</Length " + iToA(len(stream)) + ">>\nstream\n" + stream + "\nendstream"

	var b strings.Builder
	b.WriteString(header)
	offsets := make([]int, len(objs)+1) // index 0 = free entry
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

// iToA is a local int→string helper. Hand-rolled so minimalPDFWithMarker
// has no extra imports (strconv.Itoa would do the same job).
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

// TestAnthropicRealPlainTextDocumentInput is the strict regression test
// for the document-block plumbing. Anthropic accepts plain-text
// documents via PlainTextSourceParam, which carries raw decoded text
// inline — there is no parsing layer between us and the model. We
// embed a unique marker in the document and assert the model reads it
// back. If convertMessages ever drops DocumentContent parts again, the
// model will respond "I don't see a document" and the test fails.
func TestAnthropicRealPlainTextDocumentInput(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	marker := "DOC_MARKER_" + time.Now().Format("150405")
	body := "Sample policy document for adapter test.\n\nMARKER LINE: " + marker + "\n\nEnd of document."

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.DocumentContent{
						SourceType: "raw", // PlainTextSource takes text as-is
						MediaType:  "text/plain",
						Data:       body,
						Title:      "policy-doc",
					},
					llmtypes.TextContent{Text: "The attached document contains a line that begins 'MARKER LINE:'. Reply with ONLY the value after that prefix — no other words."},
				},
			},
		},
		llmtypes.WithMaxTokens(64),
	)
	if err != nil {
		t.Fatalf("GenerateContent with text document error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("response had no choices")
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, marker) {
		t.Fatalf("response missing document marker %q (DocumentContent likely dropped). content=%q", marker, content)
	}
}

// TestAnthropicRealPDFInputDocumentReceived proves the application/pdf
// path of createDocumentBlock reaches Anthropic and the API accepts the
// document. We don't assert text extraction from the hand-rolled PDF
// (extraction depends on Claude's PDF parser tolerating our minimal
// content stream) — only that the request succeeds and the model
// produces a non-empty reply, i.e. it did NOT respond with "I don't see
// a PDF attached". The strict text-extraction assertion lives in
// TestAnthropicRealPlainTextDocumentInput, which doesn't depend on
// fragile PDF parsing.
func TestAnthropicRealPDFInputDocumentReceived(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	marker := "PDF_MARKER_" + time.Now().Format("150405")
	pdfBytes := minimalPDFWithMarker(marker)
	encoded := base64.StdEncoding.EncodeToString(pdfBytes)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
						Title:      "test-pdf",
					},
					llmtypes.TextContent{Text: "Briefly describe what kind of file is attached. One short sentence."},
				},
			},
		},
		llmtypes.WithMaxTokens(80),
	)
	if err != nil {
		t.Fatalf("GenerateContent with PDF document error = %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		t.Fatalf("response had no choices")
	}
	content := strings.ToLower(resp.Choices[0].Content)
	// The model must not be claiming the PDF is absent. The exact
	// phrasing varies, but if Anthropic rejected the document block we
	// know the canonical refusal text shape.
	for _, refusal := range []string{
		"i don't see",
		"i do not see",
		"no pdf attached",
		"no document attached",
		"don't see a pdf",
		"don't see any pdf",
	} {
		if strings.Contains(content, refusal) {
			t.Fatalf("model claims no PDF was attached; document block likely not forwarded. content=%q", resp.Choices[0].Content)
		}
	}
}

// TestAnthropicRealToolChoiceModes is the P0 #9 contract test — proves
// the adapter's `tool_choice: auto/none/required` translation actually
// works against the live Messages API. Each branch sends the same
// prompt with the same single tool and asserts the behavioral
// difference: `auto` may or may not call (so we accept either),
// `none` MUST emit text and no tool_use, `required` MUST emit a
// tool_use.
//
// Anthropic's API rejects `tool_choice: required` together with
// thinking, but we don't enable thinking here.
func TestAnthropicRealToolChoiceModes(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	tools := []llmtypes.Tool{
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "lookup_city_population",
				Description: "Look up the population of a city.",
				Parameters: &llmtypes.Parameters{
					Type: "object",
					Properties: map[string]interface{}{
						"city": map[string]interface{}{"type": "string"},
					},
					Required: []string{"city"},
				},
			},
		},
	}
	prompt := []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What is the population of Tokyo? Use the tool if you have it."}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// "none" — model must NOT emit any tool_use blocks. The contract
	// here is the absence of tool calls, not the presence of text:
	// some models reply with an empty content array when they
	// genuinely have nothing to say (e.g. they would have used the
	// tool if allowed), and that's a valid response shape — the wire
	// constraint is just "no tool_use".
	respNone, err := adapter.GenerateContent(ctx, prompt,
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoiceString("none"),
		llmtypes.WithMaxTokens(120),
	)
	if err != nil {
		t.Fatalf("none branch: GenerateContent error = %v", err)
	}
	if len(respNone.Choices) == 0 || respNone.Choices[0] == nil {
		t.Fatalf("none branch: no choices")
	}
	if len(respNone.Choices[0].ToolCalls) > 0 {
		t.Fatalf("none branch: model emitted a tool_use despite tool_choice=none: %+v", respNone.Choices[0].ToolCalls)
	}

	// "required" — model MUST emit a tool_use.
	respRequired, err := adapter.GenerateContent(ctx, prompt,
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoiceString("required"),
		llmtypes.WithMaxTokens(120),
	)
	if err != nil {
		t.Fatalf("required branch: GenerateContent error = %v", err)
	}
	if len(respRequired.Choices) == 0 || respRequired.Choices[0] == nil {
		t.Fatalf("required branch: no choices")
	}
	if len(respRequired.Choices[0].ToolCalls) == 0 {
		t.Fatalf("required branch: model did not emit a tool_use despite tool_choice=required. content=%q", respRequired.Choices[0].Content)
	}

	// "auto" — model may or may not call the tool. We only assert the
	// request succeeded and produced *some* output, so the adapter's
	// auto-branch wiring is exercised end-to-end.
	respAuto, err := adapter.GenerateContent(ctx, prompt,
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoiceString("auto"),
		llmtypes.WithMaxTokens(120),
	)
	if err != nil {
		t.Fatalf("auto branch: GenerateContent error = %v", err)
	}
	if len(respAuto.Choices) == 0 {
		t.Fatalf("auto branch: no choices")
	}
	if strings.TrimSpace(respAuto.Choices[0].Content) == "" && len(respAuto.Choices[0].ToolCalls) == 0 {
		t.Fatalf("auto branch: response had neither text nor tool_use")
	}
}

// TestAnthropicRealPromptCachingCacheRead is the P1 #19 contract test:
// send the same large cached system prompt twice and assert that the
// second turn reads from cache. The proof point is
// GenerationInfo.Additional["cache_read_input_tokens"] > 0 on the
// second response. Without this, prompt-caching regressions slip
// silently because the request still succeeds; we just stop paying
// the discount.
//
// Anthropic's cache requires the system block to be ≥1024 tokens for
// Sonnet/Opus and ≥2048 for Haiku. We pad to ~3000 tokens to be safe
// regardless of which model the test runs against.
func TestAnthropicRealPromptCachingCacheRead(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	// We need to exceed Anthropic's minimum cache breakpoint, which
	// varies by model: ~1024 tokens for Sonnet/Opus, ~4096 tokens for
	// Haiku 4.5 (the default real-e2e model). 100 repetitions of the
	// boilerplate paragraph yields ~6.5k tokens, comfortably above all
	// current minimums while still being a small enough request to
	// avoid noise from rate limits.
	//
	// The text must be deterministic — Anthropic computes the cache
	// key from the content hash, so any randomness would invalidate
	// the cache between the two calls in this test.
	const sysLine = "This is a stable instructional paragraph used to fill the cache_control breakpoint. " +
		"It establishes the operating constraints and persona for a helpful assistant. " +
		"It must be long enough to exceed Anthropic's minimum cache breakpoint, which is approximately 1024 tokens for Sonnet/Opus and 4096 tokens for Haiku. "
	systemPrompt := strings.Repeat(sysLine, 100)

	prompt := func() []llmtypes.MessageContent {
		return []llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeSystem,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}},
			},
			{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only the word OK."}},
			},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Warm the cache.
	resp1, err := adapter.GenerateContent(ctx, prompt(), llmtypes.WithMaxTokens(16))
	if err != nil {
		t.Fatalf("first (cache-priming) call error = %v", err)
	}
	if resp1.Choices[0].GenerationInfo == nil || resp1.Choices[0].GenerationInfo.Additional == nil {
		t.Fatalf("first call missing GenerationInfo.Additional")
	}
	// On the first call we expect cache_creation_input_tokens > 0
	// (cache write). cache_read may or may not be > 0 depending on
	// whether a previous identical call within the 5m TTL has primed
	// it from another test run.
	createdRaw := resp1.Choices[0].GenerationInfo.Additional["cache_creation_input_tokens"]
	created, _ := createdRaw.(int)
	t.Logf("first call: cache_creation=%d cache_read=%v", created, resp1.Choices[0].GenerationInfo.Additional["cache_read_input_tokens"])

	// Second call — same system prompt, must hit the cache.
	resp2, err := adapter.GenerateContent(ctx, prompt(), llmtypes.WithMaxTokens(16))
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}
	if resp2.Choices[0].GenerationInfo == nil || resp2.Choices[0].GenerationInfo.Additional == nil {
		t.Fatalf("second call missing GenerationInfo.Additional")
	}
	readRaw := resp2.Choices[0].GenerationInfo.Additional["cache_read_input_tokens"]
	read, ok := readRaw.(int)
	t.Logf("second call: cache_read=%d cache_creation=%v", read, resp2.Choices[0].GenerationInfo.Additional["cache_creation_input_tokens"])
	if !ok || read <= 0 {
		t.Fatalf("expected cache_read_input_tokens > 0 on the second call; got %v (type %T). The adapter may have stopped attaching cache_control breakpoints, or the GA-era prompt-caching beta dropped.", readRaw, readRaw)
	}
}

// TestAnthropicRealAuthFailureClassified is the P2 #22 contract test.
// TestAnthropicRealJSONSchemaStrictViaTool (contract P1 #21).
// Anthropic does not expose a separate strict JSON schema mode like
// OpenAI's response_format=json_schema. The canonical strict-output
// pattern on Claude is forcing tool_use on a tool whose input_schema
// pins the shape — the model is required to emit a tool_use block
// whose `input` validates against that schema. We test that the
// adapter both: (a) forwards tool_choice so the model is forced to
// call it, and (b) returns a parseable tool_call whose arguments
// match the required keys and types.
func TestAnthropicRealJSONSchemaStrictViaTool(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)

	tools := []llmtypes.Tool{
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "record_movie_review",
			Description: "Record a movie review with a numeric rating and a one-sentence summary.",
			Parameters: &llmtypes.Parameters{
				Type: "object",
				Properties: map[string]interface{}{
					"title":   map[string]interface{}{"type": "string", "description": "The movie's title."},
					"rating":  map[string]interface{}{"type": "integer", "description": "Rating from 1 to 10."},
					"summary": map[string]interface{}{"type": "string", "description": "One-sentence summary."},
				},
				Required: []string{"title", "rating", "summary"},
			},
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Record a review for the movie 'Arrival' with a rating of 9 and a short summary."}}},
		},
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoice(&llmtypes.ToolChoice{Type: "function", Function: &llmtypes.FunctionName{Name: "record_movie_review"}}),
		llmtypes.WithMaxTokens(512),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].ToolCalls) == 0 {
		t.Fatalf("model did not emit a forced tool_call; content=%q", resp.Choices[0].Content)
	}
	call := resp.Choices[0].ToolCalls[0]
	if call.FunctionCall == nil || call.FunctionCall.Name != "record_movie_review" {
		t.Fatalf("forced tool_choice did not pin tool: got call=%+v", call)
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(call.FunctionCall.Arguments), &args); err != nil {
		t.Fatalf("tool arguments are not valid JSON: %v\nargs=%s", err, call.FunctionCall.Arguments)
	}
	for _, key := range []string{"title", "rating", "summary"} {
		if _, ok := args[key]; !ok {
			t.Fatalf("schema violated: required key %q missing from tool args. args=%v", key, args)
		}
	}
	if _, ok := args["rating"].(float64); !ok {
		t.Fatalf("schema violated: rating is not numeric. args=%v", args)
	}
	if _, ok := args["title"].(string); !ok {
		t.Fatalf("schema violated: title is not string. args=%v", args)
	}
}

// A deliberately invalid key must produce an error whose surface text
//   - mentions auth/credential trouble,
//   - does NOT echo the invalid key back (otherwise screen-share
//     debugging leaks real keys when a user pastes one with a typo).
//
// The adapter today wraps SDK errors verbatim, so this test pins down
// the user-facing contract independently from whatever upstream
// changes Anthropic makes to its 401 body.
func TestAnthropicRealAuthFailureClassified(t *testing.T) {
	if os.Getenv("RUN_ANTHROPIC_REAL_E2E") == "" {
		t.Skip("set RUN_ANTHROPIC_REAL_E2E=1 to run real Anthropic auth-failure tests")
	}
	const badKey = "sk-ant-api03-DELIBERATELY-INVALID-FOR-AUTH-CLASSIFICATION-TEST"
	client := anthropic.NewClient(anthropicoption.WithAPIKey(badKey))
	adapter := NewAnthropicAdapter(client, ModelClaudeHaiku45, &MockLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}}},
	}, llmtypes.WithMaxTokens(8))
	if err == nil {
		t.Fatal("expected GenerateContent to fail with a bogus key; got nil error")
	}
	msg := err.Error()
	if strings.Contains(msg, badKey) {
		t.Fatalf("error leaked the bogus key text — a real user's mis-pasted key would also leak through this surface.\nerror=%q", msg)
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
		t.Fatalf("error does not hint at auth/credential trouble; users won't know to re-check their key.\nerror=%q", msg)
	}
}

// TestAnthropicRealTopKDoesNotError mirrors TestAnthropicRealTopPDoesNotError
// for the top_k parameter. Anthropic accepts top_k (unlike OpenAI Chat
// Completions), so a non-zero value should round-trip cleanly.
func TestAnthropicRealTopKDoesNotError(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with the single word OK."}}},
		},
		llmtypes.WithTopK(40),
		llmtypes.WithMaxTokens(16),
	)
	if err != nil {
		t.Fatalf("GenerateContent with top_k=40 error = %v", err)
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Content) == "" {
		t.Fatalf("response with top_k was empty")
	}
}

// TestAnthropicRealCostEstimateOnPlainText (contract P2 #25). After a
// successful call the adapter must surface cost_usd_estimated +
// cost_model_id on GenerationInfo.Additional. Anthropic's Messages
// API ships tokens but no USD field; this proves the registry-driven
// cost math reaches downstream cost ledgers.
func TestAnthropicRealCostEstimateOnPlainText(t *testing.T) {
	adapter, model := newRealAnthropicAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(16),
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

// TestAnthropicRealRateLimitClassified (contract P2 #23). Real
// rate-limit responses are hard to provoke deterministically, so this
// test fires a burst of concurrent tiny requests and inspects whatever
// errors come back. If at least one is classifiable as a rate-limit
// (429 / "rate limit" / "quota" / "throttle"), we assert:
//   - the error text does NOT echo the API key
//   - the surfaced text would tell a user the request was throttled
// If no 429 fires inside the budget, the test self-skips rather than
// fails — provider tier headroom is not under our control.
func TestAnthropicRealRateLimitClassified(t *testing.T) {
	adapter, _ := newRealAnthropicAdapter(t)
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))

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
		for _, marker := range []string{"rate limit", "rate-limit", "ratelimit", "429", "too many requests", "quota", "throttle", "overloaded"} {
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
