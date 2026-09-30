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

func TestCLIFullRolloutIsSeparate(t *testing.T) {
	t.Setenv(cliLandlockEnv, "users:a@x.com")
	t.Setenv(cliFullEnv, "")
	if !cliLandlockRequested("u1", "a@x.com") || cliFullRequested("u1", "a@x.com") {
		t.Fatal("Full CLI must be opted in separately from the lock")
	}
	t.Setenv(cliFullEnv, "users:a@x.com")
	if !cliFullRequested("u1", "a@x.com") || cliFullRequested("u2", "b@x.com") {
		t.Fatal("Full CLI rollout list not honoured")
	}
}

// Unconfined Full CLI is for a person's own machine only: it needs its own switch and is refused
// on a multi-user server, where the lock is the boundary.
func TestCLIFullUnconfinedIsSingleUserOnly(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv(cliFullUnconfinedEnv, "")
	if cliFullUnconfinedAllowed() {
		t.Fatal("allowed without the switch")
	}
	t.Setenv(cliFullUnconfinedEnv, "on")
	if !cliFullUnconfinedAllowed() {
		t.Fatal("refused on a single-user machine with the switch on")
	}
	t.Setenv("MULTI_USER_MODE", "true")
	if cliFullUnconfinedAllowed() {
		t.Fatal("allowed on a multi-user server")
	}
}
