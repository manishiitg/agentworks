package mcpagent

import (
	"errors"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// PLAT-101 stage 2. quotaExhaustedModels was a map[string]bool, which could
// only express "never try this again". Two things broke as a result: a
// five-hour window that reopened mid-run left the model benched for the
// agent's lifetime, and the terminal error could not tell a caller when
// capacity returns — the one fact a workflow needs to suspend instead of
// losing the run.

func TestExhaustedModelIsRetriedOnceItsWindowReopens(t *testing.T) {
	a := &Agent{quotaExhaustedModels: map[quotaModelKey]time.Time{
		modelQuotaKey(LLMModel{Provider: "claudecode", ModelID: "sonnet"}): time.Now().Add(-time.Minute), // reopened a minute ago
	}}
	resetAt, exhausted := a.quotaExhaustedModels[modelQuotaKey(LLMModel{Provider: "claudecode", ModelID: "sonnet"})]
	if !exhausted {
		t.Fatal("precondition: model should be recorded as exhausted")
	}
	if resetAt.After(time.Now()) {
		t.Fatal("precondition: this fixture's window should already have reopened")
	}
	// The selection loop deletes the entry and retries a model whose window
	// has reopened; a bool map made that impossible to express at all.
	if resetAt.IsZero() {
		t.Error("a stated reset time must be retained, not flattened away")
	}
}

func TestExhaustedModelWithoutResetTimeStaysSkipped(t *testing.T) {
	a := &Agent{quotaExhaustedModels: map[quotaModelKey]time.Time{
		modelQuotaKey(LLMModel{Provider: "claudecode", ModelID: "sonnet"}): {}, // exhausted, no reliable reset stated
	}}
	resetAt, exhausted := a.quotaExhaustedModels[modelQuotaKey(LLMModel{Provider: "claudecode", ModelID: "sonnet"})]
	if !exhausted {
		t.Fatal("model should still be recorded as exhausted")
	}
	if !resetAt.IsZero() {
		t.Error("an unknown reset must stay zero — PLAT-101 forbids inventing one")
	}
}

// The reset instant must come off the typed provider error rather than being
// parsed out of a sentence at this layer.
func TestQuotaResetIsReadFromTheTypedProviderError(t *testing.T) {
	reset := time.Date(2026, 8, 18, 18, 0, 0, 0, time.UTC)
	err := &llmerrors.Error{
		Kind: llmerrors.KindQuotaExhausted, Provider: "claudecode", Model: "sonnet",
		RetryAt: reset, Err: errors.New("claude code usage limit reached"),
	}

	if !isQuotaExhaustedError(err) {
		t.Fatal("typed quota error must classify as quota exhaustion so same-model retries are skipped")
	}
	if got := llmerrors.RetryAtOrZero(err); !got.Equal(reset) {
		t.Errorf("RetryAtOrZero = %v, want %v", got, reset)
	}
	if classifyLLMError(err) != "quota_exhausted_error" {
		t.Errorf("classifyLLMError = %q, want quota_exhausted_error", classifyLLMError(err))
	}
}

// A quota error with no stated reset is still quota exhaustion: "exhausted,
// time unknown" must stay distinct from "not exhausted".
func TestQuotaErrorWithoutResetStillClassifies(t *testing.T) {
	err := &llmerrors.Error{
		Kind: llmerrors.KindQuotaExhausted, Provider: "claudecode",
		Err: errors.New("usage limit reached"),
	}
	if classifyLLMError(err) != "quota_exhausted_error" {
		t.Error("still quota exhaustion without a reset time")
	}
	if !llmerrors.RetryAtOrZero(err).IsZero() {
		t.Error("RetryAt must be zero rather than guessed")
	}
}
