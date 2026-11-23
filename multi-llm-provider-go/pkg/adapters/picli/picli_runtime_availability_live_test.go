package picli

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestPiRuntimeAvailabilityLive is the CertRuntimeAvailability P0 proof for
// Pi: the contract binary resolves on PATH and answers the version probe.
// Credential-free; the providers page and the P0 runner derive
// install/upgrade status from this same probe.
//
// Gated behind -coding-cli-p0-live so plain unit runs stay hermetic.
func TestPiRuntimeAvailabilityLive(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	version, err := llmtypes.ProbeCLIBinaryVersion(ctx, "pi", "--version")
	if err != nil {
		t.Fatalf("version probe error = %v", err)
	}
	if version == "" {
		t.Fatal("empty version from probe")
	}
	t.Logf("pi version %s", version)
}
