package llmerrors

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// PLAT-872-64: useful provider reasons must not expose credentials in banners or retry rows.
func TestProviderDisplayKeepsReasonAndMasksCredentialsBeforeClipping(t *testing.T) {
	for _, tc := range []struct{ raw, code, step string }{
		{"Cursor SDK turn failed: Model unavailable on Plus. Switch to an included model to continue.", "billing_error", "Choose another model"},
		{"invalid API key; API_KEY=private-value", "authentication_failed", "Update this account"},
		{"Cursor SDK turn failed: [unknown] Invalid User API Key", "authentication_failed", "Update this account"},
		{"Cursor SDK did not start: Sign in again to use Cursor.", "authentication_failed", "Sign in again"},
		{"model not found", "provider_unavailable", "Choose another model"},
		{"internal error: upstream refused; Bearer private-value\nsecond line", "internal_error", "Open technical details"},
		{`internal error: {"api_key":"` + strings.Repeat("private-value", 30) + `"}`, "internal_error", "Open technical details"},
	} {
		d := ProviderDisplay(errors.New(tc.raw))
		if d.Code != tc.code || !strings.Contains(d.NextStep, tc.step) || d.Message == "" || strings.Contains(d.Message, "private-value") || strings.Contains(d.Message, "second line") || len([]rune(d.Message)) > 200 {
			t.Fatalf("unexpected display: %+v", d)
		}
		if tc.code != "internal_error" && d.Retryable {
			t.Fatalf("terminal refusal was retryable: %+v", d)
		}
	}
}

// PLAT-746-158 A4: limits, configuration failures and outages need distinct
// next steps; a billing refusal never promises recovery at a quota reset.
func TestProviderDisplayFailureActions(t *testing.T) {
	for _, tc := range []struct {
		kind  Kind
		code  string
		retry bool
	}{
		{KindBilling, "billing_error", false},
		{KindQuotaExhausted, "quota_exhausted", false},
		{KindRateLimit, "rate_limited", true},
		{KindContextTooLong, "configuration_error", false},
		{KindInvalidRequest, "configuration_error", false},
		{KindServerError, "provider_unavailable", true},
		{KindNetwork, "provider_unavailable", true},
		{KindTimeout, "provider_unavailable", true},
		{KindUserInputRequired, "input_required", false},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			d := ProviderDisplay(&Error{Kind: tc.kind, RetryAt: time.Now().Add(time.Hour), Err: errors.New(`{"message":"diagnostic payload","token":"private-value"}`)})
			if d.Code != tc.code || d.Retryable != tc.retry || d.Message == "" || d.NextStep == "" || strings.Contains(d.Message, "diagnostic") || strings.Contains(d.Message, "private-value") {
				t.Fatalf("unhelpful display: %+v", d)
			}
			if tc.kind == KindBilling && strings.Contains(strings.ToLower(d.Message+d.NextStep), "reset") {
				t.Fatalf("billing promised reset: %+v", d)
			}
		})
	}
}

// PLAT-746-987: native failures keep useful account actions without leaking model credentials.
func TestNativeProviderFailuresHaveSpecificActionsAndMaskedModels(t *testing.T) {
	reset := time.Date(2030, 1, 2, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		failure         *Error
		message, action string
	}{
		{&Error{Provider: "agy-cli", Kind: KindQuotaExhausted, Err: errors.New("Antigravity stopped this turn; it may be out of quota.")}, "may be out of quota", "pick another model"},
		{&Error{Provider: "agy-cli", Kind: KindQuotaExhausted, RetryAt: reset, Err: errors.New("quota")}, "Antigravity quota", "2030-01-02T01:00:00Z"},
		{&Error{Provider: "pi-cli", Kind: KindAuth, Err: errors.New("API_KEY=private-value")}, "not signed in", "/login"},
		{&Error{Provider: "agy-cli", Kind: KindModelNotFound, Model: "model API_KEY=private-value", Err: errors.New("missing")}, "not available for this account", "Pick another model"},
	} {
		display := ProviderDisplay(tc.failure)
		if !strings.Contains(display.Message, tc.message) || !strings.Contains(display.NextStep, tc.action) || strings.Contains(display.Message+display.NextStep, "private-value") {
			t.Fatalf("unhelpful or unsafe display: %+v", display)
		}
	}
}
