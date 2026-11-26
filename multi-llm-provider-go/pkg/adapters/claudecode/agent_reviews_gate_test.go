package claudecode

import (
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
)

// TestAgentReviewsApproved is the cheap enforcement gate for the agentic P0
// suite: it fails until an agent has reviewed and approved EVERY captured live
// streaming-output record (testdata/agent-reviews/*.json). It runs in a normal
// `go test` (no live CLI), so any agent — now or in a future CLI release — is
// told, on failure, to review the recorded output before the suite passes.
func TestAgentReviewsApproved(t *testing.T) {
	agentreview.RequireAllApproved(t, filepath.Join("testdata", "agent-reviews"))
}
