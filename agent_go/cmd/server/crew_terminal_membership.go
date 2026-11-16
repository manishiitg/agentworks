package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Serializes native join/leave only, so two simultaneous joins cannot bind one
// private native context to different Crews. The existing registry is the store.
var crewTerminalJoinMu sync.Mutex

// A request-local index, not a persisted grant or cross-request cache. One
// filesystem scan is shared by all shares/participants in that operation.
func terminalCrewMemberships() (map[string]string, error) {
	root, err := crewroot.StateRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	joined := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, err := readCrewTeam(entry.Name())
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, row := range t.Agents {
			if row.Kind != "terminal" || row.Terminal == nil {
				continue
			}
			id := row.NamedAgentID
			if _, exists := joined[id]; exists {
				joined[id] = ""
			} else {
				joined[id] = t.ProjectID
			}
		}
	}
	return joined, nil
}

func terminalCrewForNamed(named string) (crewTeam, crewTeamRegisteredAgent, bool, error) {
	joined, err := terminalCrewMemberships()
	if err != nil {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, err
	}
	project, found := joined[named]
	if !found {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, nil
	}
	if project == "" {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, fmt.Errorf("This native agent is listed in more than one Crew; leave the extra Crew first.")
	}
	t, err := readCrewTeam(project)
	if err != nil {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, err
	}
	for _, row := range t.Agents {
		if row.Kind == "terminal" && row.Terminal != nil && row.NamedAgentID == named {
			return t, row, true, nil
		}
	}
	return crewTeam{}, crewTeamRegisteredAgent{}, false, nil
}

// A joined terminal's old public-share inbox cannot retain that audience.
// An outgoing private ask does retain its exact reply inbox: its caller is not
// the published recipient of the saved share, so that is not a discovery grant.
func terminalCrewConversationFence(ctx context.Context, c agentConversation) error {
	var shares []agentShare
	var joined map[string]string
	loaded := false
	for i, e := range c.Endpoints {
		if e.NamedAgentID == "" || e.TeamAgentID != "" || e.ShareID == "" || publishedAsker(c.Endpoints[1-i]) == e.UserID {
			continue
		}
		if !loaded {
			snapshot, err := readAgentMessageSnapshot(ctx)
			if err != nil {
				return admissionRefused("This agent's sharing is unavailable.")
			}
			shares, loaded = snapshot.Shares, true
		}
		recipient := false
		for _, sh := range shares {
			if sh.ID == e.ShareID && sh.OwnerUserID == e.UserID && sh.AgentID == e.NamedAgentID {
				recipient = true
				break
			}
		}
		if !recipient {
			continue
		}
		if joined == nil {
			var err error
			joined, err = terminalCrewMemberships()
			if err != nil {
				return admissionRefused("This agent's Crew membership is unavailable.")
			}
		}
		if _, exists := joined[e.NamedAgentID]; exists {
			return agentAdmissionRefusal{err: crewTeamReachRefusal{reason: "crew_joined"}}
		}
	}
	return nil
}
