package server

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
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
	// The website's open Team tab reads again now (teamApi.ts watchTeamTab).
	publishCrewPanelChanged(project)
	crewTeamWakeSoon() // server working copies follow the same change notice as Macs
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

// A Team's agents started or stopped a turn: tell each of its people's Macs who else is working there now, so the left
// menu shows it on the project's row even when the project is closed (owner, 10 Nov). The note carries only names and
// a state, never what anyone typed; the state is "running" when anyone else is working, "waiting" when anyone else's
// agent waits for them, else "".
// crewTeamBusySentKey is one Mac's last note: a person with two Macs gets it on each (keyed by person alone, the
// second Mac was skipped as "already sent").
func crewTeamBusySentKey(project string, sock *crewMacSocket) string {
	return project + "/" + sock.owner + "/" + sock.device
}

var crewTeamBusySent sync.Map // crewTeamBusySentKey -> last line sent

func pushCrewTeamBusy(project string) {
	publishCrewPanelChanged(project) // someone started or stopped: the website's open Team tab reads again now
	crewMacSockets.Lock()
	connected := make([]*crewMacSocket, 0, len(crewMacSockets.byDevice))
	for _, sock := range crewMacSockets.byDevice {
		if sock.send != nil {
			connected = append(connected, sock)
		}
	}
	crewMacSockets.Unlock()
	if len(connected) == 0 {
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.On {
		return
	}
	members := map[string]bool{t.OwnerID: true}
	for _, p := range t.People {
		if !p.Invited && crewTeamAccountActive(p.UserID) {
			members[p.UserID] = true
		}
	}
	targets := []*crewMacSocket{}
	for _, sock := range connected {
		if members[sock.owner] {
			targets = append(targets, sock)
		}
	}
	if len(targets) == 0 {
		return
	}
	// Resolve activity only when someone can receive it. Keep the Team's list as
	// the single presence source, excluding Only me agents from others' activity.
	ctx, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()
	people := teamAgentsByPerson(ctx, t, t.OwnerID)
	label := shareOwnerLabeler()
	for _, sock := range targets {
		everyone := crewTeamPeopleFor(t, people, sock.owner, label)
		raw, _ := json.Marshal(everyone)
		key := crewTeamBusySentKey(project, sock)
		sent := string(raw)
		if previous, ok := crewTeamBusySent.Load(key); ok && previous.(string) == sent {
			continue
		}
		if sock.send(crewRelayMessage{Type: "team.busy", Project: project, Event: raw}) == nil {
			crewTeamBusySent.Store(key, sent)
			log.Printf("[CREW_TEAM] project=%s event=presence_pushed to=%s device=%s people=%d", project, logUsernameForUserID(sock.owner), sock.device, len(everyone))
		}
	}
}

// crewTeamPresence is one other person of a Team as a Mac shows them. A person works on a project in one place, their
// Mac or the server (owner, 10 Nov); State is "working", "waiting" (an agent of theirs waits for them), "online" or
// "offline", and Line says their place in plain words.
type crewTeamPresence struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Line  string `json:"line"`
}

func crewTeamPeopleFor(t crewTeam, people []crewTeamAgentsPerson, viewer string, label func(string) string) []crewTeamPresence {
	out := []crewTeamPresence{}
	for _, p := range people {
		if p.UserID == viewer {
			continue
		}
		onMac := crewTeamAgentOnMac(t, p.UserID)
		working, waiting, answers := false, false, false
		for _, a := range p.Agents {
			if !a.InTeam {
				continue
			}
			working = working || a.Activity == "running"
			waiting = waiting || a.Activity == "waiting"
			answers = answers || a.PlaceServer
		}
		line, available := "server · always available", true
		if onMac {
			switch {
			case p.MacOn:
				line = "Mac · on"
			case answers:
				line = "Mac · off · agents answer from the server"
			default:
				line, available = "Mac · off", false
			}
		}
		state := "offline"
		switch {
		case working:
			state = "working"
		case waiting:
			state = "waiting"
		case available:
			state = "online"
		}
		out = append(out, crewTeamPresence{Name: strings.SplitN(firstNonEmptyTrimmed(p.Name, label(p.UserID)), "@", 2)[0], State: state, Line: line})
	}
	slices.SortFunc(out, func(a, b crewTeamPresence) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// A presence change invalidates the existing views; the sidebar keeps no second
// Team busy/people model. Agent chips and the Team page read their ordinary data.
func noteCrewTeamBusy(project string) {
	local := crewLocalCopyOf(project)
	livefeed.Default.Publish(livefeed.Sessions, "")
	if local != "" {
		dropCrewAccessCache(local, project)
		publishCrewPanelChanged(local)
	}
}
