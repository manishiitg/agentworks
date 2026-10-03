package server

// workflowSchedulesAllPaused reports a workflow the person has paused: it has
// schedules and none of them is enabled (the Schedules page's "All paused").
// The Pulse launchers skip such a workflow, so pausing a workflow pauses its
// Pulse and fix runs too; chosen Pulse times and fast requests stay durable and
// run on the first tick after a schedule is resumed. A workflow with no
// schedules at all is not paused: it runs only when asked, and Pulse is how it
// gets looked after. A manual "Run Pulse" does not go through the launchers.
func workflowSchedulesAllPaused(manifest *WorkflowManifest) bool {
	if manifest == nil || len(manifest.Schedules) == 0 {
		return false
	}
	for _, schedule := range manifest.Schedules {
		if schedule.Enabled {
			return false
		}
	}
	return true
}
