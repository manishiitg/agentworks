package server

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"os"
	"path/filepath"
	"sync"
)

// Serializes native join/leave only, so two simultaneous joins cannot bind one
// private native context to different Crews. The existing registry is the store.
var crewTerminalJoinMu sync.Mutex

func terminalCrewForNamed(named string) (crewTeam, crewTeamRegisteredAgent, bool, error) {
	root, err := crewroot.StateRoot()
	if err != nil {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if os.IsNotExist(err) {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, nil
	}
	if err != nil {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, err
	}
	var found crewTeam
	var row crewTeamRegisteredAgent
	matched := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, e := readCrewTeam(entry.Name())
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return crewTeam{}, crewTeamRegisteredAgent{}, false, e
		}
		for _, a := range t.Agents {
			if a.Kind == "terminal" && a.Terminal != nil && a.Terminal.NamedAgentID == named {
				if matched {
					return crewTeam{}, crewTeamRegisteredAgent{}, false, fmt.Errorf("This native agent is listed in more than one Crew; leave the extra Crew first.")
				}
				found, row, matched = t, a, true
			}
		}
	}
	return found, row, matched, nil
}

// A joined terminal's old public-share inbox cannot retain that audience.
// An outgoing private ask does retain its exact reply inbox: its caller is not
// the published recipient of the saved share, so that is not a discovery grant.
func terminalCrewConversationFence(ctx context.Context, c agentConversation) error {
	for i, e := range c.Endpoints {
		if e.NamedAgentID == "" || e.TeamAgentID != "" || e.ShareID == "" || publishedAsker(c.Endpoints[1-i]) == e.UserID {
			continue
		}
		snapshot, err := readAgentMessageSnapshot(ctx)
		if err != nil {
			return admissionRefused("This agent's sharing is unavailable.")
		}
		recipient := false
		for _, sh := range snapshot.Shares {
			if sh.ID == e.ShareID && sh.OwnerUserID == e.UserID && sh.AgentID == e.NamedAgentID {
				recipient = true
				break
			}
		}
		if !recipient {
			continue
		}
		_, _, joined, err := terminalCrewForNamed(e.NamedAgentID)
		if err != nil {
			return admissionRefused("This agent's Crew membership is unavailable.")
		}
		if joined {
			return agentAdmissionRefusal{err: crewTeamReachRefusal{reason: "crew_joined"}}
		}
	}
	return nil
}
