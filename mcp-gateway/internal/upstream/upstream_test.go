package upstream

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestValidateURLAndPrivateEgress(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8080/mcp", "http://127.0.0.1.evil.example/mcp",
		"https://user:pass@example.com/mcp", "file:///etc/passwd", "https://example.com/mcp#fragment",
		"https://example.com/mcp?api_key=secret", "https://example.com/mcp?access_token=x", "https://example.com/mcp?x-api-key=x",
		"https://example.com/mcp?pat=x", "https://example.com/mcp?sig=x", "https://example.com/mcp?jwt=x", "https://example.com/mcp?code=x",
	} {
		if _, err := ValidateURL(raw, DialOptions{}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := ValidateURL("http://127.0.0.1:8080/mcp", DialOptions{AllowPrivate: true}); err != nil {
		t.Fatal(err)
	}
	// Hosted servers take plain settings in the query (a project, read-only mode); only credentials are refused.
	for _, raw := range []string{"https://example.com/mcp", "https://example.com/mcp?project_ref=abc&read_only=true", "https://example.com/mcp?path=a&design=b"} {
		if _, err := ValidateURL(raw, DialOptions{}); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "2001:db8::1"} {
		if !privateIP(net.ParseIP(address)) {
			t.Fatalf("special-use IP accepted: %s", address)
		}
	}
	if privateIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
	_, err := safeHTTPClient(DialOptions{}).Get("http://127.0.0.1:1/")
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("private dial = %v", err)
	}
}

func TestNoRedirectAndBoundedBody(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { followed.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	resp, err := safeHTTPClient(DialOptions{AllowPrivate: true}).Get(redirect.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || followed.Load() != 0 {
		t.Fatalf("redirect followed: status=%d calls=%d", resp.StatusCode, followed.Load())
	}

	limit := &limitedBody{ReadCloser: io.NopCloser(strings.NewReader("12345")), remaining: 4}
	read, err := io.ReadAll(limit)
	if err == nil || string(read) != "1234" {
		t.Fatalf("bounded read = %q, %v", read, err)
	}
	if _, err := Dial(context.Background(), redirect.URL); err == nil {
		t.Fatal("default dial accepted loopback HTTP")
	}
}

func TestManagedBearerIsSentOnlyToConfiguredUpstream(t *testing.T) {
	seen := ""
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	client := safeHTTPClient(DialOptions{AllowPrivate: true, BearerToken: "central-secret"})
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if seen != "Bearer central-secret" {
		t.Fatalf("managed bearer missing: %q", seen)
	}
}

func TestManagedBasicAndOAuthSchemes(t *testing.T) {
	seen := ""
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	for _, tc := range []struct {
		opts DialOptions
		want string
	}{
		{DialOptions{AllowPrivate: true, BearerToken: "dXNlcjpwYXNz", AuthScheme: "Basic"}, "Basic dXNlcjpwYXNz"},
		{DialOptions{AllowPrivate: true, AuthScheme: "Basic", AccessToken: func(context.Context) (string, error) { return "refreshed", nil }}, "Bearer refreshed"},
	} {
		resp, err := safeHTTPClient(tc.opts).Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if seen != tc.want {
			t.Fatalf("scheme changed: %q", seen)
		}
	}
	if _, err := safeHTTPClient(DialOptions{AllowPrivate: true, BearerToken: "fixture", AuthScheme: "Other"}).Get(target.URL); err == nil {
		t.Fatal("unsupported scheme sent")
	}
}

func TestVaultCredentialHeadersStayOnTheValidatedUpstream(t *testing.T) {
	raw := `{"X-API-Key":"example-key","Authorization":"Basic ZXhhbXBsZTprZXk="}`
	if err := ValidateCredentialHeaders(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"Host":"other.example"}`, `{"X-Key":"line\nvalue"}`, `null`, `{}`} {
		if ValidateCredentialHeaders(bad) == nil {
			t.Fatalf("accepted unsafe header metadata: %s", bad)
		}
	}
	seen := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-API-Key") == "example-key" && r.Header.Get("Authorization") == "Basic ZXhhbXBsZTprZXk="
		w.WriteHeader(200)
	}))
	defer target.Close()
	response, err := safeHTTPClient(DialOptions{AllowPrivate: true, AuthScheme: "Headers", BearerToken: raw}).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !seen {
		t.Fatal("saved headers not applied")
	}
}

// A program Vault starts sees only what it needs to run, never the app's or Vault's settings and keys.
func TestCommandEnvironmentPassesOnlyRunEssentials(t *testing.T) {
	t.Setenv("AGENTWORKS_TEST_SECRET", "x")
	t.Setenv("ANTHROPIC_API_KEY", "x")
	t.Setenv("GATEWAY_TEST", "x")
	t.Setenv("LC_ALL", "en_US.UTF-8")
	env := strings.Join(commandEnvironment(LocalCommand{Env: map[string]string{"OWN": "1"}}), "\n")
	for _, hidden := range []string{"AGENTWORKS_TEST_SECRET=", "ANTHROPIC_API_KEY=", "GATEWAY_TEST="} {
		if strings.Contains(env, hidden) {
			t.Fatalf("passed %s to the program", hidden)
		}
	}
	for _, kept := range []string{"PATH=", "LC_ALL=", "OWN=1"} {
		if !strings.Contains(env, kept) {
			t.Fatalf("missing %s", kept)
		}
	}
}
