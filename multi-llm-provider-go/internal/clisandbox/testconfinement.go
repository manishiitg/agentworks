package clisandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestConfinement is the lock a live Full CLI test runs under, the same one a
// real chat gets: Seatbelt on a Mac, the Landlock launcher on Linux
// (CODING_TEST_LANDLOCK_RUNNER names it). ok is false when this host cannot
// confine a CLI, and the test should skip: Full CLI never runs unconfined.
func TestConfinement(provider, workDir string) (policy *llmtypes.CLISecurityPolicy, ok bool) {
	policy = &llmtypes.CLISecurityPolicy{
		Mode:        llmtypes.CLISecurityModeIsolated,
		Provider:    provider,
		PrivateHome: filepath.Join(workDir, ".agentworks-test-sandbox", "cli-home", provider),
	}
	switch runtime.GOOS {
	case "darwin":
		policy.Seatbelt = true
	case "linux":
		policy.LandlockRunner = strings.TrimSpace(os.Getenv("CODING_TEST_LANDLOCK_RUNNER"))
	}
	return policy, policy.Confined()
}
