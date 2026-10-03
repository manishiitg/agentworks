package server

import "testing"

// The platform decides how coding CLIs run; there is no switch to forget.
// Only a person's own Mac runs them unconfined. Everywhere else they are
// locked, and if the lock cannot be applied the chat loses native tools
// rather than running unconfined.
func TestDecideCLIConfinement(t *testing.T) {
	origOS, origRunner := cliHostOS, cliLandlockRunner
	t.Cleanup(func() { cliHostOS, cliLandlockRunner = origOS, origRunner })
	withLock := func() (string, bool) { return "/usr/lib/agentworks/landlock-run", true }
	noLock := func() (string, bool) { return "", false }

	cases := []struct {
		name       string
		os         string
		multiUser  string
		workingDir string
		runner     func() (string, bool)
		want       cliRunDecision
	}{
		{"own Mac", "darwin", "", "/w", noLock, cliRunUnconfined},
		{"own Mac, no working folder", "darwin", "", "", noLock, cliRunUnconfined},
		{"multi-user Mac never runs unconfined", "darwin", "true", "/w", noLock, cliRunBridgeOnly},
		{"Linux server with the lock", "linux", "true", "/w", withLock, cliRunConfined},
		{"Linux without MULTI_USER_MODE still locks", "linux", "", "/w", withLock, cliRunConfined},
		{"Linux whose lock is broken fails closed", "linux", "true", "/w", noLock, cliRunBridgeOnly},
		{"Linux with no working folder fails closed", "linux", "true", "", withLock, cliRunBridgeOnly},
	}
	for _, tc := range cases {
		cliHostOS, cliLandlockRunner = tc.os, tc.runner
		t.Setenv("MULTI_USER_MODE", tc.multiUser)
		got, runner, why := decideCLIConfinement(tc.workingDir)
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
		if got == cliRunConfined && runner == "" {
			t.Errorf("%s: confined without a launcher", tc.name)
		}
		if got == cliRunBridgeOnly && why == "" {
			t.Errorf("%s: fell back without saying why", tc.name)
		}
	}
}
