package main

import "testing"

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
