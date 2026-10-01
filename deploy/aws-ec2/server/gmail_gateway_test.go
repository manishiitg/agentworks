package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGmailPushBypassesBrowserGatePreservingGoogleIdentity(t *testing.T) {
	for _, passwordGateOff := range []bool{false, true} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer google-oidc" {
				t.Error("gateway replaced Google identity")
			}
			if r.Header.Get("X-User-ID") != "" {
				t.Error("gateway forwarded spoofed app user")
			}
			w.WriteHeader(204)
		}))
		gw := &gateway{agent: proxyFor(upstream.URL), disablePasswordGate: passwordGateOff}
		req := httptest.NewRequest("POST", "/api/hooks/gmail/events", nil)
		req.Header.Set("Authorization", "Bearer google-oidc")
		req.Header.Set("X-User-ID", "admin")
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("push status %d", rec.Code)
		}
		upstream.Close()
	}
	for _, path := range []string{"/api/hooks/gmail/events/extra", "/api/gmail-inbound/route", "/api/hooks/gmail/events/"} {
		if isWebhookRequest(httptest.NewRequest("POST", path, nil)) {
			t.Fatalf("public management or prefix bypass: %s", path)
		}
	}
	if isWebhookRequest(httptest.NewRequest("GET", "/api/hooks/gmail/events", nil)) {
		t.Fatal("GET bypassed browser gate")
	}
}
