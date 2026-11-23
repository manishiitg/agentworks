package codexcli

import (
	"runtime"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodexSandboxUnderConfinement(t *testing.T) {
	policy := &llmtypes.CLISecurityPolicy{
		Mode: llmtypes.CLISecurityModeIsolated, PrivateHome: "/private/cli-home",
		LandlockRunner: "/platform/landlock-runner", Seatbelt: true,
	}
	wantNative := "workspace-write"
	wantReadOnly := "read-only"
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		wantNative = "danger-full-access"
	}
	if runtime.GOOS == "darwin" {
		wantReadOnly = "danger-full-access"
	}
	tests := []struct {
		name          string
		opts          *llmtypes.CallOptions
		sandbox, want string
	}{
		{"native under outer sandbox", &llmtypes.CallOptions{CLISecurity: policy}, "workspace-write", wantNative},
		{"bridge-only retains Linux read-only", &llmtypes.CallOptions{CLISecurity: policy}, "read-only", wantReadOnly},
		{"no options", nil, "workspace-write", "workspace-write"},
		{"no policy", &llmtypes.CallOptions{}, "workspace-write", "workspace-write"},
		{"compatibility", &llmtypes.CallOptions{CLISecurity: &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeCompatibility, PrivateHome: policy.PrivateHome, LandlockRunner: policy.LandlockRunner, Seatbelt: true}}, "workspace-write", "workspace-write"},
		{"incomplete policy", &llmtypes.CallOptions{CLISecurity: &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, LandlockRunner: policy.LandlockRunner, Seatbelt: true}}, "workspace-write", "workspace-write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codexSandboxUnderConfinement(test.opts, test.sandbox); got != test.want {
				t.Fatalf("sandbox = %q; want %q", got, test.want)
			}
		})
	}
}
