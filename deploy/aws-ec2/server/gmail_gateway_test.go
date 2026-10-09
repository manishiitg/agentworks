package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// Regression from server A: opening the review link without an app bearer token
// returned authentication_required before the plan handler could validate it.
func TestGmailSetupBrowserRoundTripBypassesOnlyItsReviewAndCallbacks(t *testing.T) {
	for _, gateOff := range []bool{false, true} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-User-ID") != "" {
				t.Error("spoofed app identity reached setup handler")
			}
			if r.URL.Query().Get("plan_id") != "expired-review" && r.URL.Query().Get("state") != "gmail-setup-expired" {
				t.Errorf("setup capability query lost: %s", r.URL.Path)
			}
			http.Error(w, "Setup review expired", http.StatusGone)
		}))
		gw := httptest.NewServer(&gateway{agent: proxyFor(upstream.URL), disablePasswordGate: gateOff})
		for _, check := range []struct {
			method, path string
			status       int
		}{
			{"GET", "/api/gmail-inbound/setup/start?plan_id=expired-review", http.StatusGone},
			{"POST", "/api/gmail-inbound/setup/start?plan_id=expired-review", http.StatusGone},
			{"GET", "/api/oauth/callback?state=gmail-setup-expired", http.StatusGone},
			{"GET", "/api/human-feedback/gmail/auth/callback?state=gmail-setup-expired", http.StatusGone},
			{"GET", "/api/gmail-inbound/route", http.StatusUnauthorized},
			{"POST", "/api/gmail-inbound/sender-consent", http.StatusUnauthorized},
			{"GET", "/api/gmail-inbound/setup/start/extra", http.StatusUnauthorized},
			{"DELETE", "/api/gmail-inbound/setup/start", http.StatusUnauthorized},
		} {
			req, err := http.NewRequest(check.method, gw.URL+check.path, strings.NewReader(""))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-User-ID", "admin")
			resp, err := gw.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != check.status {
				t.Errorf("gateOff=%v %s %s: status %d, want %d", gateOff, check.method, check.path, resp.StatusCode, check.status)
			}
		}
		gw.Close()
		upstream.Close()
	}
}
