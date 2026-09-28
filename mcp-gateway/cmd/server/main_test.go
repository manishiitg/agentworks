package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalAdminTokenRotatesAndStaysPrivate(t *testing.T) {
	dir := t.TempDir()
	first, err := localAdminToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := localAdminToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(second) != 64 {
		t.Fatal("local admin token was not regenerated")
	}
	path := filepath.Join(dir, "admin-token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token file mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != second {
		t.Fatal("token file does not contain the current token")
	}
}

func TestResolveBind(t *testing.T) {
	cases := []struct {
		name      string
		bind      string
		token     string
		want      string
		wantError bool
	}{
		{name: "default is loopback", bind: "", want: "127.0.0.1"},
		{name: "explicit loopback", bind: "127.0.0.1", want: "127.0.0.1"},
		{name: "ipv6 loopback", bind: "::1", want: "::1"},
		{name: "localhost", bind: "localhost", want: "localhost"},
		{name: "lan with secret", bind: "192.168.1.10", token: "a-long-private-secret", want: "192.168.1.10"},
		{name: "public bind with secret", bind: "0.0.0.0", token: "a-long-private-secret", want: "0.0.0.0"},
		{name: "public ipv6 bind with secret", bind: "::", token: "a-long-private-secret", want: "::"},
		{name: "lan without secret refused", bind: "192.168.1.10", wantError: true},
		{name: "public bind without secret refused", bind: "0.0.0.0", wantError: true},
		{name: "public ipv6 bind without secret refused", bind: "::", wantError: true},
		{name: "launcher default refused", bind: "0.0.0.0", token: "local-admin", wantError: true},
		{name: "server default refused", bind: "192.168.1.10", token: "m0-human-token", wantError: true},
		{name: "weak token refused", bind: "0.0.0.0", token: "short", wantError: true},
		{name: "unverified hostname refused", bind: "gateway.internal", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveBind(tc.bind, tc.token)
			if tc.wantError {
				if err == nil {
					t.Fatalf("resolveBind(%q) = %q, want error", tc.bind, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBind(%q) error: %v", tc.bind, err)
			}
			if got != tc.want {
				t.Fatalf("resolveBind(%q) = %q, want %q", tc.bind, got, tc.want)
			}
		})
	}
}

func TestValidatePublicURL(t *testing.T) {
	strong := "private-alpha-secret-with-32-characters"
	cases := []struct {
		name, publicURL, token string
		wantError              bool
	}{
		{name: "local default", publicURL: "http://127.0.0.1:18745", token: "m0-human-token"},
		{name: "hosted alpha", publicURL: "https://caplayer.example.com", token: strong},
		{name: "proxy to loopback still needs strong token", publicURL: "https://caplayer.example.com", token: "m0-human-token", wantError: true},
		{name: "public http refused", publicURL: "http://caplayer.example.com", token: strong, wantError: true},
		{name: "URL credentials refused", publicURL: "https://user:pass@caplayer.example.com", token: strong, wantError: true},
		{name: "path refused", publicURL: "https://caplayer.example.com/mcp", token: strong, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePublicURL(tc.publicURL, tc.token)
			if (err != nil) != tc.wantError {
				t.Fatalf("validatePublicURL(%q) error = %v, wantError %t", tc.publicURL, err, tc.wantError)
			}
		})
	}
}

func TestValidateExposure(t *testing.T) {
	if err := validateExposure("127.0.0.1", "http://127.0.0.1:18745"); err != nil {
		t.Fatalf("loopback development should work: %v", err)
	}
	if err := validateExposure("0.0.0.0", "http://127.0.0.1:18745"); err == nil {
		t.Fatal("public bind with local advertised URL must fail")
	}
	if err := validateExposure("0.0.0.0", "https://caplayer.example.com"); err == nil {
		t.Fatal("public alpha endpoint should fail before per-user sign-in and persistence exist")
	}
	if err := validateExposure("127.0.0.1", "https://caplayer.example.com"); err == nil {
		t.Fatal("reverse-proxied public alpha endpoint should fail")
	}
}
