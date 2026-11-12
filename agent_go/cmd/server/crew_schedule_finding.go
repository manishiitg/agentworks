package server

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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

const crewFindingRefusal = "Findings come from scheduled runs; use decision or doing instead."

// crewScheduleTurnName is the schedule that started this session's current turn, for this person and Crew.
func crewScheduleTurnName(sessionID, localProject, user string) (string, error) {
	turn, ok := crewScheduleTurns.Load(sessionID)
	st, _ := turn.(crewScheduleTurn)
	if !ok || st.Project != localProject || st.UserID != user {
		return "", fmt.Errorf("%s", crewFindingRefusal)
	}
	return st.Name, nil
}

// crewMacPairSent: the call carries this Crew's pair ID, so it is the owner's Mac speaking for its own Crew.
func crewMacPairSent(r *http.Request, project string) bool {
	b, err := crewroot.ReadSync(project)
	sent := r.Header.Get(crewMacPairHeader)
	return err == nil && b != nil && b.PairID != "" && sent != "" && subtle.ConstantTimeCompare([]byte(sent), []byte(b.PairID)) == 1
}
