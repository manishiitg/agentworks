package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// Registration owns the Bridge identity; the Team only remembers its binding.
// Lock order is Team record, then mailbox store (the same order as terminal
// registration). The callback must never acquire a Team record lock.
func withTeamBridgeRegistration(ctx context.Context, project, person, chat string, register func(string) (namedMessageAgent, error)) (namedMessageAgent, error) {
	if project == "" || chat == "" {
		return register("")
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return namedMessageAgent{}, err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	t, err := readCrewTeam(project)
	if os.IsNotExist(err) {
		return register("")
	}
	if err != nil {
		return namedMessageAgent{}, err
	}
	row, found := t.registeredAgent(person, chat)
	homeMain := false
	if t.RosterSource != "" {
		// The Team's actual Main is a real agent. Other home tabs are
		// execution copies and must reuse their source's identity instead.
		source, e := readCrewTeam(t.RosterSource)
		homeMain = e == nil && found && source.OnlineHome == t.ProjectID && t.OwnerID == "team-"+source.ProjectID && person == t.OwnerID && row.Tab == macMainTab && row.ChatKey == crewOwnerMainChatKey(t.ProjectID) && crewTeamAccountActive(person)
		if !homeMain {
			return namedMessageAgent{}, fmt.Errorf("use the source agent's admitted Bridge thread")
		}
	}
	if !found {
		return register("")
	}
	if (!homeMain && !crewTeamSourceRowAdmits(t, person, row)) || ownerOfCrewProjectID(project) != t.OwnerID {
		return namedMessageAgent{}, errCrewTeamAgentNotYours
	}
	named, err := register(row.NamedAgentID)
	if err != nil {
		return namedMessageAgent{}, err
	}
	if named.UserID != person || named.ID == "" || row.NamedAgentID != "" && row.NamedAgentID != named.ID {
		return namedMessageAgent{}, fmt.Errorf("Team Bridge identity changed")
	}
	if row.NamedAgentID == named.ID {
		return named, nil
	}
	row.NamedAgentID = named.ID
	t.Agents[row.ID] = row
	t.Revision++
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), t, 0600); err != nil {
		return namedMessageAgent{}, err
	}
	log.Printf("[AGENT-MESSAGE] event=bound team=%s agent=%s chat=%s", project, named.ID, row.ID)
	publishCrewPanelChanged(project)
	go pushCrewTeamRecordChanged(t)
	return named, nil
}
