package llmtypes

import "testing"

func TestValidateCLISecurityLaunchRejectsUnenforcedStrictModes(t *testing.T) {
	for _, mode := range []CLISecurityMode{CLISecurityModeIsolated, CLISecurityModeVerified} {
		t.Run(string(mode), func(t *testing.T) {
			opts := &CallOptions{CLISecurity: &CLISecurityPolicy{Mode: mode}}
			if err := ValidateCLISecurityLaunch(opts); err == nil {
				t.Fatalf("expected %s to fail closed", mode)
			}
		})
	}
	if err := ValidateCLISecurityLaunch(&CallOptions{
		CLISecurity: &CLISecurityPolicy{Mode: CLISecurityModeCompatibility},
	}); err != nil {
		t.Fatalf("compatibility rejected: %v", err)
	}
}
