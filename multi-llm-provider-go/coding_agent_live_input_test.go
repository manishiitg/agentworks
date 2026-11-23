package llmproviders

import (
	"context"
	"testing"
)

func TestSendCodingAgentLiveInputRejectsNonCodingProvider(t *testing.T) {
	err := SendCodingAgentLiveInput(context.Background(), ProviderOpenAI, "gpt-5", "session-1", "hello")
	if err == nil {
		t.Fatal("expected error for non-coding provider")
	}
	if !IsCodingAgentContinuationError(err, CodingAgentContinuationErrorNonApplicable) {
		t.Fatalf("expected non-applicable error, got %T: %v", err, err)
	}
}

func TestSendCodingAgentLiveInputRequiresMessageAndOwner(t *testing.T) {
	if err := SendCodingAgentLiveInput(context.Background(), ProviderClaudeCode, "claude-sonnet-4-6", "", "hello"); !IsCodingAgentContinuationError(err, CodingAgentContinuationErrorNonContinuable) {
		t.Fatalf("expected missing owner non-continuable error, got %v", err)
	}
	if err := SendCodingAgentLiveInput(context.Background(), ProviderClaudeCode, "claude-sonnet-4-6", "session-1", " "); !IsCodingAgentContinuationError(err, CodingAgentContinuationErrorNonContinuable) {
		t.Fatalf("expected empty message non-continuable error, got %v", err)
	}
}
