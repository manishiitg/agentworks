package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// A schedule run's finding is an explicit team_sync call (action=finding), like
// doing and decision (PLAT-856-564). The server accepts it only while a schedule
// started the session's current turn, and fills in the schedule name and owner
// from that turn; the model never passes them. Same trusted writer as the rest.
type crewScheduleTurn struct{ Project, UserID, Name string }

var crewScheduleTurns sync.Map // session ID -> crewScheduleTurn

func beginCrewScheduleTurn(sessionID string, job productScheduleJob) (end func()) {
	if sessionID == "" || job.ProjectID == "" || job.AutomationKind != "" || job.Profile.ID != crewProfileID || strings.TrimSpace(job.Schedule.Name) == "" {
		return func() {}
	}
	crewScheduleTurns.Store(sessionID, crewScheduleTurn{Project: job.ProjectID, UserID: job.UserID, Name: strings.TrimSpace(job.Schedule.Name)})
	return func() { crewScheduleTurns.Delete(sessionID) }
}

func (api *StreamingAPI) postCrewTeamFinding(ctx context.Context, caller *UserClaims, localProject, project, sessionID, sourceKey, line string) (string, error) {
	turn, ok := crewScheduleTurns.Load(sessionID)
	st, _ := turn.(crewScheduleTurn)
	if !ok || st.Project != localProject || st.UserID != caller.UserID || project != localProject || IsLocalMode() {
		return "", fmt.Errorf("Findings come from scheduled runs; use decision or doing instead.")
	}
	agentID, err := api.crewTeamActivityAgentForSession(ctx, caller, nil, localProject, project, sessionID, sourceKey)
	if err != nil {
		return "", err
	}
	if _, err := writeCrewTeamActivityPost(ctx, project, caller.UserID, agentID, "finding", line, "", st.Name); err != nil {
		return "", fmt.Errorf("Could not post. Use one short line, and only an In team agent can post.")
	}
	return "Posted to the Team as a finding from " + st.Name + ".", nil
}
