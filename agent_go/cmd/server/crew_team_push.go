package server

import (
	"log"
	"sync"
)

// A Team changed: tell its people's Macs at once over the relay connection each already keeps open, so they sync now
// instead of on their next timed check (crew_team_client.go keeps a slow check for a missed note). The note carries no
// file names or content, only the Crew and a revision.

var crewTeamPushed sync.Map // "files/<project>" or "team/<project>" -> last revision announced

// pushCrewTeamChanged follows a save of a Team's shared files (only its joined people get it).
func pushCrewTeamChanged(project string, revision uint64) {
	pushCrewTeamNote(project, "files", revision, false)
}

// pushCrewTeamRecordChanged follows a save of the Team record (invites, joins, Team on or off), so an invited person's
// Mac shows the invite and a member's Mac sees the Team change at once.
func pushCrewTeamRecordChanged(t crewTeam) {
	pushCrewTeamNote(t.ProjectID, "team", t.Revision, true)
}

func pushCrewTeamNote(project, kind string, revision uint64, everyone bool) {
	key := kind + "/" + project
	if previous, ok := crewTeamPushed.Load(key); ok && previous.(uint64) >= revision {
		return
	}
	crewTeamPushed.Store(key, revision)
	t, err := readCrewTeam(project)
	if err != nil || !everyone && !t.On {
		return
	}
	people := map[string]bool{t.OwnerID: true}
	for _, p := range t.People {
		if everyone || !p.Invited {
			people[p.UserID] = true
		}
	}
	crewMacSockets.Lock()
	targets := []*crewMacSocket{}
	for _, sock := range crewMacSockets.byDevice {
		if people[sock.owner] && sock.send != nil {
			targets = append(targets, sock)
		}
	}
	crewMacSockets.Unlock()
	sent := 0
	for _, sock := range targets {
		if sock.send(crewRelayMessage{Type: "team.changed", Project: project, Seq: int(revision)}) == nil {
			sent++
		}
	}
	log.Printf("[CREW_TEAM] project=%s event=change_pushed kind=%s revision=%d macs=%d", project, kind, revision, sent)
}
