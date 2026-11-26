package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// PLAT-746-863: completion events used to keep only Error(), losing the reset
// known to the quota memo. Stored/replayed events must carry the same instant.
func TestQuotaFailureEventPreservesTypedResetOnly(t *testing.T) {
	reset := time.Date(2026, 11, 11, 9, 10, 0, 0, time.UTC)
	for _, known := range []bool{true, false} {
		failure := &llmerrors.Error{Kind: llmerrors.KindQuotaExhausted, Provider: "claude-code", Model: "claude-opus-5-5", Err: errors.New("selected model is quota-exhausted")}
		if known {
			failure.RetryAt = reset
		}
		wrapped := fmt.Errorf("llm error: %w", failure)
		event := NewUnifiedCompletionEventWithError("session", "chat", "", wrapped.Error(), 0, 1)
		event.SetProviderFailure(wrapped)
		event.SetProviderFailureDisplay() // Later annotation must retain typed metadata.
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var restored UnifiedCompletionEvent
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.Metadata["error_code"] != "quota_exhausted" {
			t.Fatalf("lost quota classification: %+v", restored.Metadata)
		}
		got, present := restored.Metadata["retry_at"]
		if present != known || (known && got != reset.Format(time.RFC3339)) {
			t.Fatalf("reset=%v present=%v known=%v", got, present, known)
		}
	}
	// The diagnostic can mention a clock; only typed RetryAt supplies metadata.
	raw := errors.New("You've hit your 5-hour limit · resets 2:40pm (Asia/Calcutta)")
	if _, present := ProviderFailureMetadata(raw)["retry_at"]; present {
		t.Fatal("invented reset from diagnostic text")
	}
}
