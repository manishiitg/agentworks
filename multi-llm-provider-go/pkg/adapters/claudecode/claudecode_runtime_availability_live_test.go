package claudecode

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeRuntimeAvailabilityLive is the CertRuntimeAvailability P0 proof
// for Claude: the contract binary resolves on PATH and answers the version
// probe. Credential-free; the providers page and the P0 runner derive
// install/upgrade status from this same probe.
//
// Gated behind -coding-cli-p0-live so plain unit runs stay hermetic.
func TestClaudeRuntimeAvailabilityLive(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	version, err := llmtypes.ProbeCLIBinaryVersion(ctx, "claude", "--version")
	if err != nil {
		t.Fatalf("version probe error = %v", err)
	}
	if version == "" {
		t.Fatal("empty version from probe")
	}
	t.Logf("claude version %s", version)
}
