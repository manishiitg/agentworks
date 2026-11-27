package llmproviders

import (
	"context"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type compactOnlyTestModel struct{ providerAwareLoggingTestModel }

func (m *compactOnlyTestModel) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// agy's compaction reply: no text, no session handle, a compacted result.
	resp := llmtypes.IdleCompactSkipResponse("agy-cli", llmtypes.IdleCompactReasonCompacted)
	llmtypes.AttachIdleCompactResult(resp.Choices[0].GenerationInfo, llmtypes.IdleCompactResult{Compacted: true, Reason: llmtypes.IdleCompactReasonCompacted})
	return resp, nil
}

// A compaction-only turn has no answer text by design; it must not fail as
// "choice.Content is empty" (every agy compaction did).
func TestCompactionOnlyResponseIsNotEmptyContentError(t *testing.T) {
	wrapped := NewProviderAwareLLM(&compactOnlyTestModel{}, ProviderAgyCLI, "agy-test", nil, "", &providerAwareCaptureLogger{})
	resp, err := wrapped.GenerateContent(context.Background(), []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "/compact"}}}})
	if err != nil {
		t.Fatalf("compaction-only response refused: %v", err)
	}
	if result, ok := llmtypes.IdleCompactResultFromResponse(resp); !ok || !result.Compacted {
		t.Fatalf("result=%+v ok=%v", result, ok)
	}
}
