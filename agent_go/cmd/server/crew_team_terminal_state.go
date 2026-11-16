package server

import "time"

// One mapping for Team rows and the own terminal menu. An old busy hook is not
// proof of work, and a wait never turns a disconnected attachment into online.
func crewTeamTerminalState(named namedMessageAgent, bindingErr error, waiting bool, ownerName string, you bool) (label, activity string) {
	if bindingErr != nil || !namedAgentOnline(named) {
		return "Offline", ""
	}
	if named.Attachment.Activity == "busy" && !named.Attachment.ActivityAt.IsZero() && time.Since(named.Attachment.ActivityAt) < 10*time.Minute {
		return "Working", "running"
	}
	if waiting {
		return crewTeamWaitingLabel(ownerName, you), "waiting"
	}
	return "Idle", ""
}
