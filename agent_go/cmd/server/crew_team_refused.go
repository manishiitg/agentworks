package server

import (
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

// crewTeamRefusals remembers, per server project, that its server refused this Mac a Team read (403 or 404). That is
// a steady answer, not an outage: a server project has no Team, or the person was removed. Asking again every few
// seconds only fills the server's log (#3760), so the Mac waits until a Team change on this Mac or ten minutes pass.
var crewTeamRefusals sync.Map

const crewTeamRefusalWait = 10 * time.Minute

func crewTeamRefusedRecently(serverProject string) bool {
	at, ok := crewTeamRefusals.Load(serverProject)
	return ok && time.Since(at.(time.Time)) < crewTeamRefusalWait
}

// noteCrewTeamRead records a Team read's answer: a refusal starts the wait, a good read ends it.
func noteCrewTeamRead(serverProject string, err error) {
	var refused *crewRemoteHTTPError
	switch {
	case err == nil:
		crewTeamRefusals.Delete(serverProject)
	case errors.As(err, &refused) && (refused.Status == http.StatusForbidden || refused.Status == http.StatusNotFound):
		if _, seen := crewTeamRefusals.Swap(serverProject, time.Now()); !seen {
			log.Printf("[CREW_TEAM] event=team_read_refused peer=%s status=%d wait=%s", serverProject, refused.Status, crewTeamRefusalWait)
		}
	}
}
