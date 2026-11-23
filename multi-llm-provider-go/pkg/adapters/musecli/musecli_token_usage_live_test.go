package musecli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestMuseTokenUsageLive is the CertTokenUsage P0 proof for Muse: a real
// turn reports input/output token counts through GenerationInfo, the
// surface the cost ledger reads.
//
// Gated behind -coding-cli-p0-live; requires a real muse CLI.
func TestMuseTokenUsageLive(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	marker := "USAGE-" + museRandomHex(t, 3)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Do not use any tools. Answer directly with exactly what is asked."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Reply with exactly: " + marker}}},
	}, llmtypes.WithReasoningEffort("low"), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	final := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(final, marker) {
		t.Fatalf("final = %q, want the marker", final)
	}
	if resp.Choices[0].GenerationInfo == nil {
		t.Fatal("turn reported no GenerationInfo")
	}
	usage := llmtypes.ExtractUsageFromGenerationInfo(resp.Choices[0].GenerationInfo)
	if usage == nil || usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
		t.Fatalf("usage = %+v, want live input/output counts", usage)
	}
}
