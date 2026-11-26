package netguard

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"100.100.100.200", "0.0.0.0", "::1", "fe80::1", "fd00:ec2::254", "fc00::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "224.0.0.1"} {
		if PublicAddr(netip.MustParseAddr(blocked)) {
			t.Errorf("%s counted as public", blocked)
		}
	}
	for _, public := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !PublicAddr(netip.MustParseAddr(public)) {
			t.Errorf("%s counted as private", public)
		}
	}
}

func TestCheckURL(t *testing.T) {
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "data:text/html,x", "http://localhost/x",
		"https://metadata.google.internal/", "http://169.254.169.254/latest", "http://[::1]:8080/", "https://user:pw@example.com/",
		"https://svc.internal/", "http://127.0.0.1:24000/api"} {
		if err := CheckURL(bad, false); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s allowed: %v", bad, err)
		}
	}
	if err := CheckURL("http://example.com/mcp", true); !errors.Is(err, ErrBlocked) {
		t.Error("plain http allowed where https is required")
	}
	if err := CheckURL("https://mcp.example.com/mcp", true); err != nil {
		t.Errorf("public https refused: %v", err)
	}
}

// A name that resolves to a private address is refused at dial time, after
// DNS: here "localhost", which the client dials without any URL check.
func TestClientRefusesPrivateDestinationsAtDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	defer server.Close()
	port := server.URL[strings.LastIndex(server.URL, ":")+1:]
	for _, target := range []string{server.URL, "http://localhost:" + port} {
		resp, err := Client(0).Get(target)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("reached %s", target)
		}
		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("%s: unexpected error %v", target, err)
		}
	}
}

func TestRedirectsStayOnTheOrigin(t *testing.T) {
	check := Client(0).CheckRedirect
	from, _ := url.Parse("https://mcp.example.com/mcp")
	to := func(raw string) *http.Request { u, _ := url.Parse(raw); return &http.Request{URL: u} }
	if err := check(to("https://mcp.example.com/other"), []*http.Request{{URL: from}}); err != nil {
		t.Fatalf("same-origin redirect refused: %v", err)
	}
	for _, target := range []string{"http://169.254.169.254/latest/meta-data", "https://evil.example.net/", "http://mcp.example.com/mcp"} {
		if err := check(to(target), []*http.Request{{URL: from}}); !errors.Is(err, ErrBlocked) {
			t.Errorf("redirect to %s allowed", target)
		}
	}
}

func TestOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/big", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer server.Close()

	if _, err := Client(0).Get(server.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("default client reached loopback: %v", err)
	}
	resp, err := Client(0, AllowPrivate()).Get(server.URL + "/big")
	if err != nil {
		t.Fatalf("AllowPrivate: %v", err)
	}
	_ = resp.Body.Close()

	resp, err = Client(0, AllowPrivate(), MaxResponseBytes(10)).Get(server.URL + "/big")
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !errors.Is(readErr, ErrResponseTooLarge) {
		t.Fatalf("body cap not enforced: %v", readErr)
	}
	resp, err = Client(0, AllowPrivate(), MaxResponseBytes(100)).Get(server.URL + "/big")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil || len(body) != 100 {
		t.Fatalf("exact-size body refused: %d %v", len(body), readErr)
	}

	if _, err := Client(0, AllowPrivate(), NoRedirects()).Get(server.URL + "/redirect"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("NoRedirects followed a redirect: %v", err)
	}
	if resp, err := Client(0, AllowPrivate()).Get(server.URL + "/redirect"); err != nil {
		t.Fatalf("same-origin redirect refused: %v", err)
	} else {
		_ = resp.Body.Close()
	}
}
