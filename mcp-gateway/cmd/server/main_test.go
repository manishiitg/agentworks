package main

import "testing"

func TestResolveBind(t *testing.T) {
	cases := []struct {
		name      string
		bind      string
		tokenSet  bool
		want      string
		wantError bool
	}{
		{name: "default is loopback", bind: "", tokenSet: false, want: "127.0.0.1"},
		{name: "explicit loopback", bind: "127.0.0.1", tokenSet: false, want: "127.0.0.1"},
		{name: "explicit lan address", bind: "192.168.1.10", tokenSet: false, want: "192.168.1.10"},
		{name: "public bind with token", bind: "0.0.0.0", tokenSet: true, want: "0.0.0.0"},
		{name: "public ipv6 bind with token", bind: "::", tokenSet: true, want: "::"},
		{name: "public bind without token refused", bind: "0.0.0.0", tokenSet: false, wantError: true},
		{name: "public ipv6 bind without token refused", bind: "::", tokenSet: false, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveBind(tc.bind, tc.tokenSet)
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
