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
var crewTeamBusySent sync.Map // "<project>/<person>" -> last line sent

func pushCrewTeamBusy(project string) {
	t, err := readCrewTeam(project)
	if err != nil || !t.On {
		return
	}
	// One source for every screen (owner, 10 Nov): the Team's agents list, which knows each person's place (their Mac
	// or the server), whether their Mac is on and each agent's activity wherever it runs. Read as the owner, counting
	// only agents in the Team, so an Only me agent never shows anyone working.
	ctx, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()
	people := teamAgentsByPerson(ctx, t, t.OwnerID)
	label := shareOwnerLabeler()
	members := map[string]bool{}
	for _, p := range people {
		members[p.UserID] = true
	}
	crewMacSockets.Lock()
	targets := []*crewMacSocket{}
	for _, sock := range crewMacSockets.byDevice {
		if members[sock.owner] && sock.send != nil {
			targets = append(targets, sock)
		}
	}
	crewMacSockets.Unlock()
	for _, sock := range targets {
		everyone := crewTeamPeopleFor(t, people, sock.owner, label)
		state, line := crewTeamBusyLine(everyone)
		raw, _ := json.Marshal(everyone)
		key := project + "/" + sock.owner
		sent := state + "|" + line + "|" + string(raw)
		if previous, ok := crewTeamBusySent.Load(key); ok && previous.(string) == sent {
			continue
		}
		if sock.send(crewRelayMessage{Type: "team.busy", Project: project, State: state, LastLine: line, Event: raw}) == nil {
			crewTeamBusySent.Store(key, sent)
			log.Printf("[CREW_TEAM] project=%s event=presence_pushed to=%s state=%q people=%s", project, logUsernameForUserID(sock.owner), state, raw)
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

// crewTeamBusyLine is the row's spinner state and hover line: the others working ("A is working", "A and B are
// working"), else the others waiting for their person.
func crewTeamBusyLine(people []crewTeamPresence) (string, string) {
	for _, state := range []string{"working", "waiting"} {
		names := []string{}
		for _, p := range people {
			if p.State == state {
				names = append(names, p.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		who, verb := names[0], "is"
		if len(names) > 1 {
			who, verb = strings.Join(names[:len(names)-1], ", ")+" and "+names[len(names)-1], "are"
		}
		if state == "working" {
			return "running", who + " " + verb + " working"
		}
		return "waiting", who + " " + verb + " waiting for an answer"
	}
	return "", ""
}

// On the Mac: the last "who else is working" note per Crew on this Mac (the owner's Mac Crew or a member's copy).
type crewTeamBusyNote struct {
	State  string             `json:"state"`
	Line   string             `json:"line"`
	People []crewTeamPresence `json:"people,omitempty"`
	at     time.Time
}

var crewTeamBusyNotes sync.Map // local project -> crewTeamBusyNote

func noteCrewTeamBusy(project, state, line string, everyone json.RawMessage) {
	// A Crew on a server this Mac shows from that server's list (no copy here) is kept by its server id.
	local := firstNonEmptyTrimmed(crewLocalCopyOf(project), crewTeamRemoteNoteKey(project))
	note := crewTeamBusyNote{State: state, Line: line, at: time.Now()}
	_ = json.Unmarshal(everyone, &note.People)
	log.Printf("[CREW_TEAM] project=%s local=%s event=presence_received state=%q people=%d", project, local, state, len(note.People))
	if state == "" && len(note.People) == 0 {
		crewTeamBusyNotes.Delete(local)
	} else {
		crewTeamBusyNotes.Store(local, note)
	}
	// The left menu re-reads its list on a sessions notice.
	livefeed.Default.Publish(livefeed.Sessions, "")
}

// crewTeamBusyFor is the note for a Crew on this Mac, while it is fresh (a turn never reported ended stops counting).
func crewTeamBusyFor(local string) *crewTeamBusyNote {
	value, ok := crewTeamBusyNotes.Load(local)
	if !ok {
		return nil
	}
	note := value.(crewTeamBusyNote)
	// A turn never reported ended stops counting; who is online stays until the server says otherwise.
	if time.Since(note.at) > crewActivityStale {
		note.State, note.Line = "", ""
		people := make([]crewTeamPresence, len(note.People))
		for i, person := range note.People {
			if person.State == "working" || person.State == "waiting" {
				person.State = "online"
			}
			people[i] = person
		}
		note.People = people
	}
	return &note
}

// crewTeamRemoteNoteKey is the note key of a Crew that lives on a server, for that server's list on this Mac.
func crewTeamRemoteNoteKey(project string) string { return "remote:" + project }
