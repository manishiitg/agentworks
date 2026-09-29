package server

import "testing"

func TestCLILandlockRequestedRollout(t *testing.T) {
	cases := []struct {
		value, user, email string
		want               bool
	}{
		{"", "u1", "a@x.com", false},
		{"off", "u1", "a@x.com", false},
		{"on", "u1", "a@x.com", true},
		{"users:a@x.com", "u1", "A@X.com", true},
		{"users:u1, b@x.com", "u1", "", true},
		{"users:b@x.com", "u1", "a@x.com", false},
		{"users:", "u1", "a@x.com", false},
	}
	for _, tc := range cases {
		t.Setenv(cliLandlockEnv, tc.value)
		if got := cliLandlockRequested(tc.user, tc.email); got != tc.want {
			t.Fatalf("%q user=%q email=%q: got %v, want %v", tc.value, tc.user, tc.email, got, tc.want)
		}
	}
}
