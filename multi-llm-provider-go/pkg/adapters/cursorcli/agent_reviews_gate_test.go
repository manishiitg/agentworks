package cursorcli

import (
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
)

// TestAgentReviewsApproved is the cheap enforcement gate for the agentic P0
// suite (no live CLI): fails until every captured cursor streaming-output record
// is agent-approved. See internal/agentreview/README.md.
func TestAgentReviewsApproved(t *testing.T) {
	agentreview.RequireAllApproved(t, filepath.Join("testdata", "agent-reviews"))
}
