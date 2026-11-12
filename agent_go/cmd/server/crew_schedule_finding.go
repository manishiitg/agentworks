package server

import (
	"context"
	"strings"
)

// A schedule run's finding reaches Team Activity only when the agent states it:
// a line of the run's final reply that starts with "Finding:". The server never
// excerpts the rest of the reply. The post goes through the same trusted writer
// as Doing and Decision posts, marked with the schedule's name (PLAT-856-564).
func crewScheduleFindingLine(finalResponse string) string {
	line := ""
	for _, l := range strings.Split(finalResponse, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "*_> -"))
		if len(l) >= 8 && strings.EqualFold(l[:8], "finding:") {
			line = strings.TrimSpace(strings.Trim(strings.TrimSpace(l[8:]), "*_"))
		}
	}
	return line
}

func postCrewScheduleFinding(ctx context.Context, job productScheduleJob, chatKey, finalResponse string) {
	line := crewScheduleFindingLine(finalResponse)
	if line == "" || job.ProjectID == "" || job.AutomationKind != "" || job.Profile.ID != crewProfileID || strings.TrimSpace(job.Schedule.Name) == "" {
		return
	}
	// The registry decides who the agent is and whether it is In team; a
	// private agent posts nothing.
	agent, err := crewTeamSendingAgent(ctx, job.ProjectID, job.UserID, chatKey)
	if err != nil || agent.ID == "" {
		return
	}
	if _, err := writeCrewTeamActivityPost(ctx, job.ProjectID, job.UserID, agent.ID, "finding", line, "", job.Schedule.Name); err != nil {
		scheduleLogf("[CREW_TEAM_ACTIVITY] project=%s event=schedule_finding_refused schedule=%s agent_id=%s", job.ProjectID, job.Schedule.ID, agent.ID)
	}
}
