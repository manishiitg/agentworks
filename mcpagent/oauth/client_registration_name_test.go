package oauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The name a remote server's consent screen shows for the app asking for access.
func TestRegisterClientSendsTheAppName(t *testing.T) {
	var got ClientRegistrationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode registration request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"c1"}`))
	}))
	defer server.Close()

	if _, err := (Discoverer{}).RegisterClient(server.URL, "http://localhost/callback"); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if got.ClientName != "AgentWorks" {
		t.Errorf("client_name = %q, want AgentWorks", got.ClientName)
	}
	if got.ClientURI != "https://agentworkshq.com" {
		t.Errorf("client_uri = %q, want the AgentWorks site", got.ClientURI)
	}
}

// Vercel (2026-10-07) rejects a hosted callback with a 400 whose body says why;
// the error must carry that reason instead of only the status.
func TestRegisterClientRejectionKeepsTheProviderReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_redirect_uri","error_description":"The provided redirect URIs are not approved for use by this authorization server."}`))
	}))
	defer server.Close()

	_, err := (Discoverer{}).RegisterClient(server.URL, "https://app.example.com/api/oauth/callback")
	var regErr *RegistrationError
	if !errors.As(err, &regErr) || regErr.StatusCode != http.StatusBadRequest || regErr.Code != "invalid_redirect_uri" {
		t.Fatalf("err = %v, want a RegistrationError with invalid_redirect_uri", err)
	}
	if want := "The provided redirect URIs are not approved for use by this authorization server (invalid_redirect_uri)"; regErr.Reason() != want {
		t.Errorf("Reason() = %q, want %q", regErr.Reason(), want)
	}
	if !strings.Contains(err.Error(), "invalid_redirect_uri") {
		t.Errorf("Error() = %q, want the response body", err.Error())
	}
}
