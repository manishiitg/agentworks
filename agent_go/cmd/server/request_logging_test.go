package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogPathRedactsAuthenticationQueryTokens(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/terminals/terminal-1/stream?cols=120&token=secret-jwt&access_token=secret-access&auth_token=secret-auth&code=secret-code&state=secret-state", nil)

	got := requestLogPath(req)
	for _, secret := range []string{"secret-jwt", "secret-access", "secret-auth", "secret-code", "secret-state"} {
		if strings.Contains(got, secret) {
			t.Fatalf("requestLogPath leaked %q in %q", secret, got)
		}
	}
	for _, want := range []string{"cols=120", "token=%5BREDACTED%5D", "access_token=%5BREDACTED%5D", "auth_token=%5BREDACTED%5D"} {
		if !strings.Contains(got, want) {
			t.Fatalf("requestLogPath = %q, want %q", got, want)
		}
	}
}

func TestRequestLogPathPreservesPathWithoutQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/terminals", nil)
	if got := requestLogPath(req); got != "/api/terminals" {
		t.Fatalf("requestLogPath = %q, want /api/terminals", got)
	}
}

func TestShouldTraceAPIRequest(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{http.MethodGet, false},
		{http.MethodHead, false},
		{http.MethodOptions, false},
		{http.MethodPost, true},
		{http.MethodPut, true},
		{http.MethodPatch, true},
		{http.MethodDelete, true},
	}

	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/api/example", nil)
			if got := shouldTraceAPIRequest(req); got != test.want {
				t.Fatalf("shouldTraceAPIRequest(%s) = %t, want %t", test.method, got, test.want)
			}
		})
	}
}
