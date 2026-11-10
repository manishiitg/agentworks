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
	busy := crewRelayStreams.busyPeople(project)
	people := map[string]bool{t.OwnerID: true}
	for _, p := range t.People {
		if !p.Invited {
			people[p.UserID] = true
		}
	}
	// A person's agents that run on the server count too (owner, 10 Nov: a server agent working showed nowhere on a
	// teammate's Mac): a chat of theirs in this Crew with a turn going now.
	for person := range people {
		if busy[person] == "running" {
			continue
		}
		if records, err := defaultProductConversationRegistryStore().projectChats(context.Background(), person, crewProfileID, project); err == nil {
			for _, record := range records {
				if crewServerChatRunning(record.SessionID) {
					busy[person] = "running"
					break
				}
			}
		}
	}
	label := shareOwnerLabeler()
	crewMacSockets.Lock()
	targets, online := []*crewMacSocket{}, map[string]bool{}
	for _, sock := range crewMacSockets.byDevice {
		if people[sock.owner] && sock.send != nil {
			targets = append(targets, sock)
			online[sock.owner] = true
		}
	}
	crewMacSockets.Unlock()
	// A person whose agents work on the server is online whether or not a Mac of theirs is connected.
	for person := range people {
		if !crewTeamAgentOnMac(t, person) {
			online[person] = true
		}
	}
	for _, sock := range targets {
		state, line := crewTeamBusyLine(busy, sock.owner, label)
		everyone, _ := json.Marshal(crewTeamPeopleFor(people, busy, online, sock.owner, label))
		key := project + "/" + sock.owner
		sent := state + "|" + line + "|" + string(everyone)
		if previous, ok := crewTeamBusySent.Load(key); ok && previous.(string) == sent {
			continue
		}
		if sock.send(crewRelayMessage{Type: "team.busy", Project: project, State: state, LastLine: line, Event: everyone}) == nil {
			crewTeamBusySent.Store(key, sent)
			log.Printf("[CREW_TEAM] project=%s event=presence_pushed to=%s state=%q people=%s", project, logUsernameForUserID(sock.owner), state, everyone)
		}
	}
}

// crewTeamPresence is one other person of a Team as a Mac shows them (owner, 10 Nov: who is online, per person):
// "working", "waiting" (their agent waits for them), "online" (their Mac is connected) or "offline".
type crewTeamPresence struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func crewTeamPeopleFor(people map[string]bool, busy map[string]string, online map[string]bool, viewer string, label func(string) string) []crewTeamPresence {
	out := []crewTeamPresence{}
	for person := range people {
		if person == viewer {
			continue
		}
		state := "offline"
		switch {
		case busy[person] == "running":
			state = "working"
		case busy[person] == "waiting":
			state = "waiting"
		case online[person]:
			state = "online"
		}
		out = append(out, crewTeamPresence{Name: strings.SplitN(label(person), "@", 2)[0], State: state})
	}
	slices.SortFunc(out, func(a, b crewTeamPresence) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// crewTeamBusyLine is what one person's Mac is told: the others working ("A is working", "A and B are working"), else
// the others waiting for their person.
func crewTeamBusyLine(busy map[string]string, viewer string, label func(string) string) (string, string) {
	names := map[string][]string{}
	for person, state := range busy {
		if person != viewer {
			names[state] = append(names[state], strings.SplitN(label(person), "@", 2)[0])
		}
	}
	for _, state := range []string{"running", "waiting"} {
		list := names[state]
		if len(list) == 0 {
			continue
		}
		slices.Sort(list)
		who, verb := list[0], "is"
		if len(list) > 1 {
			who, verb = strings.Join(list[:len(list)-1], ", ")+" and "+list[len(list)-1], "are"
		}
		if state == "running" {
			return state, who + " " + verb + " working"
		}
		return state, who + " " + verb + " waiting for an answer"
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
