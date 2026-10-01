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
		"https://example.com/mcp?api_key=secret",
	} {
		if _, err := ValidateURL(raw, DialOptions{}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := ValidateURL("http://127.0.0.1:8080/mcp", DialOptions{AllowPrivate: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateURL("https://example.com/mcp", DialOptions{}); err != nil {
		t.Fatal(err)
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
