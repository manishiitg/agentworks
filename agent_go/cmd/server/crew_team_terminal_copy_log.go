package server

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"log"
)

// Use the Crew's saved source Mac, never this caller's other computer. This
// guidance is returned only after the ordinary Crew/attachment admission.
func crewTerminalCopyUnavailable(team crewTeam) string {
	mac := "the Mac where this Crew lives"
	if binding, err := crewroot.ReadSync(team.ProjectID); err == nil && binding != nil && binding.MacName != "" {
		mac = binding.MacName
	}
	return fmt.Sprintf("%s lives on %s. Run this chat there, or on this Mac join as a Team member.", firstNonEmptyTrimmed(team.Name, "This Crew"), mac)
}

// Fixed reason codes only: no local path, payload, journal text or error body.
func logCrewTerminalCopyRequest(project, request, outcome, reason string) {
	if !crewTeamIdentifier.MatchString(project) {
		project = ""
	}
	if !crewTeamIdentifier.MatchString(request) {
		request = ""
	}
	log.Printf("[TEAM_TERMINAL_AGENT] event=copy_request project=%s request=%s outcome=%s reason=%s", project, request, outcome, reason)
}
