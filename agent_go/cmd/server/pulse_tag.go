package server

import (
	"context"
	"strings"
)

// The picker inserts a plain leading #pulse tag. Refuse that request while
// Pulse is off before it can be queued or delivered to a retained Builder.
// Discussing the tag in ordinary prose still belongs to Builder.
func workflowPulseTagIsOff(ctx context.Context, req QueryRequest) bool {
	if req.AgentMode != "workflow_phase" || req.PhaseID != "workflow-builder" || req.AgentProfileID != "" || req.ParentSessionID != "" || req.IsAutoNotification || req.PulseLifecycleTurn {
		return false
	}
	words := strings.Fields(req.Query)
	if len(words) == 0 || !strings.EqualFold(strings.TrimRight(words[0], ":,."), "#pulse") {
		return false
	}
	manifest, found, err := ReadWorkflowManifest(ctx, req.SelectedFolder)
	return err == nil && found && manifest != nil && manifest.Kind != "relay" && !manifest.PulseEnabled()
}
