package llmerrors

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClassifyKinds(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		// Cancellation / timeout
		{"context canceled", context.Canceled, KindCanceled},
		{"wrapped canceled", fmt.Errorf("call failed: %w", context.Canceled), KindCanceled},
		{"deadline exceeded", context.DeadlineExceeded, KindTimeout},
		{"timeout text", errors.New("request timed out after 30s"), KindTimeout},

		// Billing failures must not acquire usage-reset advice.
		{"billing verification", errors.New("API error 402: Billing verification failed. Please check your payment method. (billing_error)"), KindBilling},
		{"402 exhausted credits", errors.New("status code: 402 insufficient credits"), KindQuotaExhausted},
		{"billing status", errors.New("status code: 402 payment required"), KindBilling},
		// PLAT-872-64: a fixed-settings plan refusal was retried five times as a server failure.
		{"Cursor Plus model setting refusal", errors.New("Cursor SDK turn failed: Model unavailable on Plus. Plus includes Composer 2.5, Grok 4.7, Grok 4.6, and Grok 4.5 with fixed settings. Switch to an included model to continue."), KindBilling},
		{"billing mention alone", errors.New("unable to continue; check billing details"), KindUnknown},

		// Quota exhaustion (must win over rate-limit patterns in the same message)
		{"gemini daily quota", errors.New("googleapi: Error 429: Quota exceeded for metric generate_requests_per_model_per_day"), KindQuotaExhausted},
		{"openai quota", errors.New("429: You exceeded your current quota, please check your plan and billing details"), KindQuotaExhausted},
		// PLAT-872: native Codex API-key exhaustion was classified unknown.
		{"codex native quota requires billing action, not a reset wait", errors.New("codex-cli turn failed: Quota exceeded. Check your plan and billing details."), KindBilling},
		{"claude historical login is not current auth", errors.New("failed to clear stale prompt; latest pane tail: Select login method: 1. Claude account with subscription 2. Anthropic Console account · API usage billing"), KindUnknown},
		{"claude current login is not billing quota", errors.New("Select login method: 1. Claude account with subscription 2. Anthropic Console account · API usage billing"), KindAuth},
		{"claude pane billing text is not quota", errors.New("failed to clear stale prompt; latest pane tail: API usage billing"), KindUnknown},
		{"claude code usage", errors.New("you've hit your usage limit, resets at 3pm"), KindQuotaExhausted},
		// PLAT-746-863: exact wrapped refusal from the SDK result path.
		{"claude five-hour result", errors.New("llm error: selected LLM failed: claude-code/claude-opus-5-5 [unknown]: claude run reported an error result: You've hit your 5-hour limit · resets 2:40pm (Asia/Calcutta)"), KindQuotaExhausted},
		{"claude weekly result", errors.New("claude run reported an error result: You've hit your weekly limit · resets 2:40pm (Asia/Calcutta)"), KindQuotaExhausted},
		{"claude historical window limit", errors.New("failed to clear stale prompt; latest pane tail: You've hit your 5-hour limit · resets 2:40pm (Asia/Calcutta)"), KindUnknown},
		{"muse usage", errors.New("Usage limit reached · /upgrade; wait for usage to reset at Sep 14 at 5:30 AM"), KindQuotaExhausted},
		// Codex 0.153+ appends an OFFER to every reply ("You have N usage limit
		// resets available"); an unrelated failure that embeds that pane must
		// not read as quota exhaustion, while a real limit hit still does.
		{"codex offer line is not a limit", errors.New("codex-cli turn failed: Codex input remained unconfirmed after 8s; latest pane tail: • You have 2 usage limit resets available. Run /usage to use one."), KindUnknown},
		{"codex real limit with offer line", errors.New("you've hit your usage limit. You have 1 usage limit reset available. Run /usage to use one."), KindQuotaExhausted},
		{"resource exhausted", errors.New("rpc error: code = ResourceExhausted desc = RESOURCE_EXHAUSTED"), KindQuotaExhausted},

		// Rate limit
		{"bedrock throttle", errors.New("operation error Bedrock Runtime: ThrottlingException"), KindRateLimit},
		{"status 429", errors.New("API returned unexpected status code: 429"), KindRateLimit},
		{"anthropic overloaded", errors.New("overloaded_error: Anthropic is temporarily overloaded"), KindRateLimit},
		{"rate limit text", errors.New("rate limit exceeded, retry after 30s"), KindRateLimit},

		// Auth
		{"invalid key", errors.New("invalid x-api-key"), KindAuth},
		{"Cursor SDK invalid user key", errors.New("Cursor SDK turn failed: [unknown] Invalid User API Key"), KindAuth},
		{"Cursor SDK missing login", errors.New("Cursor SDK did not start: Sign in again to use Cursor. Open Providers, select this account, and choose Sign in."), KindAuth},
		{"status 401", errors.New("status code: 401 Unauthorized"), KindAuth},
		{"Codex reused refresh token", errors.New("codex-cli turn failed: Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again."), KindAuth},
		{"bedrock denied", errors.New("AccessDeniedException: not authorized to invoke this model"), KindAuth},

		// Context too long
		{"anthropic long", errors.New("prompt is too long: 250000 tokens > 200000 maximum"), KindContextTooLong},
		{"openai context", errors.New("context_length_exceeded: this model's maximum context length is 128000"), KindContextTooLong},

		// Model not found
		{"openai 404", errors.New("status code: 404 The model `gpt-9` does not exist or you do not have access to it"), KindModelNotFound},

		// Server / outage
		{"status 500", errors.New("status code: 500 internal server error"), KindServerError},
		{"bad gateway", errors.New("502 Bad Gateway"), KindServerError},
		{"service unavailable", errors.New("Service Unavailable"), KindServerError},

		// Network
		{"conn refused", errors.New("dial tcp 1.2.3.4:443: connection refused"), KindNetwork},
		{"reset", errors.New("read: connection reset by peer"), KindNetwork},
		{"eof", errors.New("unexpected EOF"), KindNetwork},

		// Invalid request (4xx with extractable status, no other match)
		{"bad request", errors.New("status code: 400 malformed JSON body"), KindInvalidRequest},

		// Unknown
		{"tool message", errors.New("tool call rejected by policy"), KindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := Classify("test-provider", "test-model", tt.err)
			if got := KindOf(wrapped); got != tt.want {
				t.Errorf("KindOf(%q) = %s, want %s", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyPreservesChainAndIdempotent(t *testing.T) {
	if Classify("p", "m", nil) != nil {
		t.Fatal("Classify(nil) must be nil")
	}

	base := errors.New("rate limit exceeded")
	once := Classify("anthropic", "claude-x", base)

	// Original error stays reachable (string-based handling keeps working).
	if !errors.Is(once, base) {
		t.Error("classified error must wrap the original")
	}
	var typed *Error
	if !errors.As(once, &typed) {
		t.Fatal("expected *Error in chain")
	}
	if typed.Provider != "anthropic" || typed.Model != "claude-x" {
		t.Errorf("provider/model not carried: %+v", typed)
	}

	// Re-classifying (e.g., a second wrapper layer) must not double-wrap.
	twice := Classify("other", "other", fmt.Errorf("outer: %w", once))
	var inner *Error
	if !errors.As(twice, &inner) || inner.Provider != "anthropic" {
		t.Error("re-classification must pass through the existing *Error")
	}
}

func TestClassifyIgnoresCapturedPaneConversation(t *testing.T) {
	err := errors.New("timed out waiting for muse TUI to settle; latest pane:\nreflection: python3 << 'EOF' and unauthorized are mentioned in ordinary conversation")
	classified := Classify("muse-cli", "muse-spark", err)

	if got := KindOf(classified); got != KindTimeout {
		t.Fatalf("KindOf(classified) = %q, want %q; captured pane text must not override the timeout prefix", got, KindTimeout)
	}
	if !strings.Contains(classified.Error(), "latest pane:") {
		t.Fatal("classification must retain the captured pane in the surfaced diagnostic")
	}
}

func TestClassifyAGYTimeoutIgnoresHistoricalPane(t *testing.T) {
	for _, marker := range []string{"pane tail:", "latest pane tail:", "latest pane:", "AGY pane tail:"} {
		t.Run(marker, func(t *testing.T) {
			pane := marker + "\nstatus code: 403 PERMISSION_DENIED\nSelect login method: Claude account with subscription"
			for _, err := range []error{
				errors.New("sidecar prompt timed out after 90s; " + pane),
				fmt.Errorf("sidecar prompt has no matching user step after 90s (conversation %q, read error none); %s", "test-conversation", pane),
				errors.New("sidecar turn has no settled recorded answer within 15m0s (read error none); " + pane),
			} {
				classified := Classify("agy-cli", "test-model", err)
				var typed *Error
				if !errors.As(classified, &typed) || typed.Kind != KindTimeout || typed.Status != 0 {
					t.Fatalf("classification = %+v, want timeout without historical HTTP status", classified)
				}
				if !errors.Is(classified, err) || !strings.Contains(classified.Error(), pane) {
					t.Fatal("classification must preserve the original error and historical pane for diagnosis")
				}
				if errors.Is(classified, context.DeadlineExceeded) || errors.Is(classified, context.Canceled) {
					t.Fatal("a sidecar timeout must not carry caller cancellation semantics")
				}
			}
		})
	}
}

func TestStatusCodeExtraction(t *testing.T) {
	tests := []struct {
		msg  string
		want int
	}{
		{"status code: 429", 429},
		{"StatusCode: 500", 500},
		{"API returned unexpected status code: 503", 503},
		{"status 404", 404},
		{"model claude-500 failed", 0}, // model names must not match
		{"no status here", 0},
	}
	for _, tt := range tests {
		if got := extractStatusCode(tt.msg); got != tt.want {
			t.Errorf("extractStatusCode(%q) = %d, want %d", tt.msg, got, tt.want)
		}
	}
}

func TestIsRetryable(t *testing.T) {
	retryable := []Kind{KindRateLimit, KindServerError, KindNetwork, KindTimeout}
	for _, k := range retryable {
		if !IsRetryable(&Error{Kind: k, Err: errors.New("x")}) {
			t.Errorf("kind %s should be retryable", k)
		}
	}
	terminal := []Kind{KindBilling, KindQuotaExhausted, KindAuth, KindContextTooLong, KindModelNotFound, KindInvalidRequest, KindCanceled, KindUnknown}
	for _, k := range terminal {
		if IsRetryable(&Error{Kind: k, Err: errors.New("x")}) {
			t.Errorf("kind %s should NOT be retryable", k)
		}
	}
	if IsRetryable(errors.New("unclassified")) {
		t.Error("unclassified errors should not be considered retryable")
	}
}
