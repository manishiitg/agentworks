package cursorcli

import (
	"os"
	"path/filepath"

	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// liveConfined runs a Full CLI live test under the lock a real chat gets
// (Seatbelt on a Mac, Landlock on Linux), or skips: Full CLI never runs
// unconfined.
func liveConfined(t *testing.T, provider, workDir string) llmtypes.CallOption {
	t.Helper()
	policy, ok := clisandbox.TestConfinement(provider, workDir)
	if !ok {
		t.Skip("this host cannot confine a coding CLI (set CODING_TEST_LANDLOCK_RUNNER on Linux)")
	}
	if policy.SeatbeltEnforced() {
		// Only SeatbeltArgs writes this profile: proof the CLI started inside it.
		profile := filepath.Join(policy.PrivateHome, "agentworks-cli-seatbelt.sb")
		t.Cleanup(func() {
			if _, err := os.Stat(profile); err != nil {
				t.Errorf("the CLI did not start under Seatbelt: %v", err)
			}
		})
	}
	return func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p }
}
