package mcpagent

import (
	"path/filepath"
	"testing"

	"github.com/manishiitg/mcpagent/internal/agentreview"
)

// TestAgentReviewsApproved is the mcpagent-layer agentic gate: fails until every
// recorded message-modes output is agent-approved. Cheap, no live CLI.
func TestAgentReviewsApproved(t *testing.T) {
	agentreview.RequireAllApproved(t, filepath.Join("testdata", "agent-reviews"))
}
