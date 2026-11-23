package mcpagent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType string
	}{
		// nil
		{name: "nil error", err: nil, wantType: ""},

		// max_token_error
		{name: "max_token bedrock", err: fmt.Errorf("ValidationException: Input is too long for model"), wantType: "max_token_error"},
		{name: "max tokens generic", err: fmt.Errorf("max tokens exceeded"), wantType: "max_token_error"},

		// quota_exhausted_error
		{name: "quota per_day", err: fmt.Errorf("per_day limit reached"), wantType: "quota_exhausted_error"},
		{name: "quota per_month", err: fmt.Errorf("per_month budget exceeded"), wantType: "quota_exhausted_error"},
		{name: "quota resource_exhausted", err: fmt.Errorf("resource_exhausted: daily limit"), wantType: "quota_exhausted_error"},
		{name: "quota codex usage limit", err: fmt.Errorf("hit your usage limit"), wantType: "quota_exhausted_error"},
		{name: "quota claude code limit", err: fmt.Errorf("you've hit your limit for today"), wantType: "quota_exhausted_error"},
		{name: "quota exceeded current", err: fmt.Errorf("exceeded your current quota"), wantType: "quota_exhausted_error"},

		// throttling_error
		{name: "throttle 429", err: fmt.Errorf("StatusCode: 429 Too Many Requests"), wantType: "throttling_error"},
		{name: "throttle rate limit", err: fmt.Errorf("rate limit exceeded, retry after 30s"), wantType: "throttling_error"},
		{name: "throttle overloaded", err: fmt.Errorf("model is overloaded, please try again"), wantType: "throttling_error"},
		{name: "throttle bedrock", err: fmt.Errorf("ThrottlingException: Rate exceeded"), wantType: "throttling_error"},

		// zero_candidates_error
		{name: "zero candidates", err: fmt.Errorf("returned zero candidates"), wantType: "zero_candidates_error"},
		{name: "no candidates", err: fmt.Errorf("no candidates in response"), wantType: "zero_candidates_error"},

		// empty_content_error
		{name: "empty content", err: fmt.Errorf("Choice.Content is empty string"), wantType: "empty_content_error"},
		{name: "empty response", err: fmt.Errorf("empty response from model"), wantType: "empty_content_error"},

		// connection_error
		{name: "connection refused", err: fmt.Errorf("dial tcp 127.0.0.1:8080: connection refused"), wantType: "connection_error"},
		{name: "connection timeout", err: fmt.Errorf("timeout waiting for response"), wantType: "connection_error"},
		{name: "connection EOF", err: fmt.Errorf("unexpected EOF"), wantType: "connection_error"},

		// stream_error — note: "stream error: connection reset" matches connection_error first
		// because classifyLLMError checks isConnectionError before isStreamError
		{name: "stream error pure", err: fmt.Errorf("stream interrupted mid-response"), wantType: "stream_error"},
		{name: "stream closed", err: fmt.Errorf("stream closed unexpectedly"), wantType: "stream_error"},

		// internal_error
		{name: "internal 500", err: fmt.Errorf("API returned unexpected status code: 500"), wantType: "internal_error"},
		{name: "bad gateway", err: fmt.Errorf("Bad Gateway"), wantType: "internal_error"},
		{name: "service unavailable", err: fmt.Errorf("Service Unavailable"), wantType: "internal_error"},

		// unknown
		{name: "unknown error", err: fmt.Errorf("something completely unexpected"), wantType: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLLMError(tt.err)
			if got != tt.wantType {
				t.Fatalf("classifyLLMError(%q) = %q, want %q", tt.err, got, tt.wantType)
			}
		})
	}
}

func TestContextCanceledNotMisclassified(t *testing.T) {
	cancelErr := context.Canceled
	deadlineErr := context.DeadlineExceeded
	wrappedCancel := fmt.Errorf("operation failed: %w", context.Canceled)

	for _, err := range []error{cancelErr, deadlineErr, wrappedCancel} {
		if !isContextCanceledError(err) {
			t.Fatalf("isContextCanceledError(%v) = false, want true", err)
		}
		if isThrottlingError(err) {
			t.Fatalf("isThrottlingError should return false for context cancel: %v", err)
		}
		if isConnectionError(err) {
			t.Fatalf("isConnectionError should return false for context cancel: %v", err)
		}
		if isStreamError(err) {
			t.Fatalf("isStreamError should return false for context cancel: %v", err)
		}
		if isMaxTokenError(err) {
			t.Fatalf("isMaxTokenError should return false for context cancel: %v", err)
		}
	}
}

func TestQuotaVsThrottlingDisambiguation(t *testing.T) {
	quotaErr := fmt.Errorf("resource_exhausted: per_day limit")
	if !isQuotaExhaustedError(quotaErr) {
		t.Fatal("resource_exhausted per_day should be quota exhausted")
	}

	throttleErr := fmt.Errorf("StatusCode: 429 rate limit")
	if !isThrottlingError(throttleErr) {
		t.Fatal("429 should be throttling")
	}

	// Quota errors take priority over throttling in classifyLLMError
	// because quota is checked first in the classification chain
	combined := fmt.Errorf("resource_exhausted: 429 per_day limit hit")
	got := classifyLLMError(combined)
	if got != "quota_exhausted_error" {
		t.Fatalf("combined quota+throttle classified as %q, want quota_exhausted_error (quota takes priority)", got)
	}
}

// TestTypedErrorsClassifyWithoutStringMatching verifies the kind-first path:
// errors classified by the provider layer (llmerrors) are recognized even
// when their message text matches none of the legacy string patterns.
// This is what makes classification robust to provider message changes.
func TestTypedErrorsClassifyWithoutStringMatching(t *testing.T) {
	opaque := func(kind llmerrors.Kind) error {
		// Message deliberately contains no matchable fragments; only the
		// typed Kind carries the signal.
		return &llmerrors.Error{Kind: kind, Provider: "p", Model: "m", Err: fmt.Errorf("opaque failure %d", 12)}
	}

	tests := []struct {
		kind llmerrors.Kind
		want string
	}{
		{llmerrors.KindContextTooLong, "max_token_error"},
		{llmerrors.KindQuotaExhausted, "quota_exhausted_error"},
		{llmerrors.KindBilling, "billing_error"},
		{llmerrors.KindRateLimit, "throttling_error"},
		{llmerrors.KindNetwork, "connection_error"},
		{llmerrors.KindTimeout, "connection_error"},
		{llmerrors.KindServerError, "internal_error"},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := classifyLLMError(opaque(tt.kind)); got != tt.want {
				t.Fatalf("classifyLLMError(typed %s) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}

	// Wrapped typed errors must classify the same way.
	wrapped := fmt.Errorf("generation failed: %w", opaque(llmerrors.KindRateLimit))
	if !isThrottlingError(wrapped) {
		t.Error("wrapped typed rate-limit error should classify as throttling")
	}
}

func TestAuthAndModelNotFoundClassification(t *testing.T) {
	authCases := []error{
		fmt.Errorf("status code: 401 Unauthorized"),
		fmt.Errorf("invalid x-api-key"),
		fmt.Errorf("AccessDeniedException: not authorized to invoke this model"),
		&llmerrors.Error{Kind: llmerrors.KindAuth, Err: fmt.Errorf("opaque")},
	}
	for _, err := range authCases {
		if !isAuthError(err) {
			t.Errorf("isAuthError(%v) = false, want true", err)
		}
		if got := classifyLLMError(err); got != "auth_error" {
			t.Errorf("classifyLLMError(%v) = %q, want auth_error", err, got)
		}
	}

	modelCases := []error{
		fmt.Errorf("status code: 404 The model `gpt-9` does not exist or you do not have access to it"),
		fmt.Errorf("model_not_found"),
		&llmerrors.Error{Kind: llmerrors.KindModelNotFound, Err: fmt.Errorf("opaque")},
	}
	for _, err := range modelCases {
		if !isModelNotFoundError(err) {
			t.Errorf("isModelNotFoundError(%v) = false, want true", err)
		}
		if got := classifyLLMError(err); got != "model_not_found_error" {
			t.Errorf("classifyLLMError(%v) = %q, want model_not_found_error", err, got)
		}
	}

	// Negative: context cancellation must never look like auth, and a throttling
	// error must not be misread as auth/model-not-found.
	if isAuthError(context.Canceled) {
		t.Error("context.Canceled must not classify as auth")
	}
	throttle := fmt.Errorf("StatusCode: 429 rate limit")
	if isAuthError(throttle) || isModelNotFoundError(throttle) {
		t.Error("429 throttle must not classify as auth or model-not-found")
	}
}

func TestAGYTimeoutWithHistoricalAuthStaysFailure(t *testing.T) {
	for _, prefix := range []string{
		`sidecar prompt has no matching user step after 90s (conversation "test-conversation", read error none)`,
		`sidecar turn has no settled recorded answer within 15m0s (read error none)`,
	} {
		err := llmerrors.Classify("agy-cli", "test-model", fmt.Errorf("%s; pane tail:\nError 403, Status: PERMISSION_DENIED; unauthorized, permission denied", prefix))
		wrapped := fmt.Errorf("selected LLM failed: %w", err)
		if got := llmerrors.KindOf(wrapped); got != llmerrors.KindTimeout {
			t.Fatalf("kind = %q, want timeout", got)
		}
		if isContextCanceledError(wrapped) {
			t.Fatal("native sidecar timeout must emit a failure rather than a context cancellation")
		}
		if isAuthError(wrapped) || classifyLLMError(wrapped) != "connection_error" {
			t.Fatal("historical provider auth text must not turn the timeout into a sign-in failure")
		}
		if !isUnconfirmedAGYTurnTimeout(wrapped) {
			t.Fatal("an unconfirmed native receipt/answer must not retry the prompt")
		}
	}
	ordinary := llmerrors.Classify("agy-cli", "test-model", fmt.Errorf("request timed out; pane tail:\nsidecar prompt has no matching user step after 90s"))
	if isUnconfirmedAGYTurnTimeout(ordinary) {
		t.Fatal("historical pane text must not suppress an ordinary provider timeout's retries")
	}
}

// TestAuthAndModelNotFoundAreNotSameModelRetried locks the intent: neither kind
// is on a same-model retry path. classifyLLMError returns a dedicated type, and
// the retry loop's same-model-retry branches (zero_candidates / throttling /
// empty_content) deliberately exclude them — so the loop breaks straight to the
// caller. This guards against a future "retry unknown errors by default"
// change silently re-retrying terminal auth failures.
func TestAuthAndModelNotFoundAreNotSameModelRetried(t *testing.T) {
	sameModelRetryTypes := map[string]bool{
		"zero_candidates_error": true,
		"throttling_error":      true,
		"internal_error":        true,
		"connection_error":      true,
		"stream_error":          true,
		"empty_content_error":   true,
	}
	for _, et := range []string{"auth_error", "model_not_found_error"} {
		if sameModelRetryTypes[et] {
			t.Errorf("%s must not be a same-model-retry error type", et)
		}
	}
}

func TestNilErrorsReturnFalse(t *testing.T) {
	checks := []struct {
		name string
		fn   func(error) bool
	}{
		{"isContextCanceledError", isContextCanceledError},
		{"isMaxTokenError", isMaxTokenError},
		{"isQuotaExhaustedError", isQuotaExhaustedError},
		{"isThrottlingError", isThrottlingError},
		{"isEmptyContentError", isEmptyContentError},
		{"isZeroCandidatesError", isZeroCandidatesError},
		{"isConnectionError", isConnectionError},
		{"isStreamError", isStreamError},
		{"isInternalError", isInternalError},
		{"isAuthError", isAuthError},
		{"isModelNotFoundError", isModelNotFoundError},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.fn(nil) {
				t.Fatalf("%s(nil) = true, want false", c.name)
			}
		})
	}
}

func TestRetryLogCategoryMatchesActualErrorType(t *testing.T) {
	tests := map[string]string{
		"connection_error":      "CONNECTION",
		"throttling_error":      "THROTTLING",
		"zero_candidates_error": "ZERO_CANDIDATES",
		"":                      "RETRY",
	}
	for input, want := range tests {
		if got := retryLogCategory(input); got != want {
			t.Errorf("retryLogCategory(%q) = %q, want %q", input, got, want)
		}
	}
}

// AGY ending the turn itself (a finished error step) must show its own text and
// must not be replayed, even though its text says "Internal error" (a retry class).
func TestAGYTurnEndedByExecutorIsNotRetried(t *testing.T) {
	ended := fmt.Errorf("selected LLM failed: %w", &llmerrors.Error{Kind: llmerrors.KindServerError, Provider: "agy-cli", Err: errors.New("Antigravity ended the turn: agent executor error: generating and executing: Error 500, Message: Internal error encountered., Status: INTERNAL")})
	if !isAGYTurnEndedByExecutor(ended) {
		t.Fatal("an AGY-ended turn must not retry the prompt")
	}
	if classifyLLMError(ended) != "internal_error" || isAuthError(ended) {
		t.Fatalf("classified as %q", classifyLLMError(ended))
	}
	other := &llmerrors.Error{Kind: llmerrors.KindServerError, Provider: "agy-cli", Err: errors.New("Error 500, Message: Internal error encountered.")}
	if isAGYTurnEndedByExecutor(other) || isAGYTurnEndedByExecutor(&llmerrors.Error{Kind: llmerrors.KindServerError, Provider: "codex-cli", Err: errors.New("Antigravity ended the turn: x")}) {
		t.Fatal("only an AGY executor-ended turn may skip the retries")
	}
}

func TestRetryReasonNamesALaunchTimeoutNotAConnectionError(t *testing.T) {
	launch := &llmerrors.Error{Kind: llmerrors.KindTimeout, Provider: "muse-cli", Err: fmt.Errorf("Muse did not start in 1m30s: it was showing a plugin hooks review screen. Your message is saved; press Retry. (timed out waiting for muse TUI to settle; latest pane:\nReview plugin hooks)")}
	if got := retryReasonText("connection_error", launch); got != "Muse did not start in 1m30s: it was showing a plugin hooks review screen" {
		t.Fatalf("launch timeout reason = %q", got)
	}
	if got := retryReasonText("connection_error", &llmerrors.Error{Kind: llmerrors.KindTimeout, Err: fmt.Errorf("deadline exceeded")}); got != "no response in time" {
		t.Fatalf("plain timeout reason = %q", got)
	}
	if got := retryReasonText("connection_error", fmt.Errorf("connection refused")); got != "connection error: connection refused" {
		t.Fatalf("a real connection error includes the provider reason, got %q", got)
	}
}
