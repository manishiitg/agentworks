package server

// Team clashes (owner, 5 Nov): when two people change the same lines of a shared file, the later version stays in the
// file and the other is kept in history, as before. Both people are told once, the sync line names the file until it
// is sorted out, and either person's agent can read both versions and resolve it (team_sync clashes / resolve, the
// same over MCP): keep the current version, put the other one back, or write a merged text. Resolving is a normal new
// revision, so every copy downloads it.

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// crewTeamClashNotice is one clash just recorded, for the notification to both people.
type crewTeamClashNotice struct {
	clash   crewTeamClash
	removed bool // the losing side was a stale delete; the winner's edit stays
}

// notifyCrewTeamClashes tells the two people of each clash, once per clash (never per sync round), in plain words with
// display names. The owner is told only when they are one of the two.
func notifyCrewTeamClashes(project string, team crewTeam, notices []crewTeamClashNotice) {
	if len(notices) == 0 {
		return
	}
	title := "Clash in " + firstNonEmptyTrimmed(team.Name, "your team's shared folder")
	route := "/crew/projects/" + url.PathEscape(project)
	job := notifyJob{event: "team_clash"}
	for _, n := range notices {
		c := n.clash
		if _, seen := notifyAsked.LoadOrStore(fmt.Sprintf("team_clash\x00%s\x00%s\x00%d", project, c.Path, c.WinningRevision), time.Now()); seen {
			continue
		}
		winner, loser := c.WinningAuthor, c.LosingAuthor
		add := func(user, body string) {
			if user != "" {
				job.direct = append(job.direct, notifyDirect{user: user, msg: notifyMessage{Title: title, Body: body, Route: route}})
			}
		}
		w, l := crewPersonShortName(winner), crewPersonShortName(loser)
		switch {
		case winner == loser || loser == "":
			add(winner, fmt.Sprintf("%s was changed in two places at once. The later version is in the file; the earlier one is kept. Ask your agent to sort it out.", c.Path))
		case n.removed:
			add(loser, fmt.Sprintf("You removed %s while %s changed it. %s's version stays in the file. Ask your agent to sort it out.", c.Path, w, w))
			add(winner, fmt.Sprintf("%s removed %s while you changed it. Your version stays in the file. Ask your agent to sort it out.", l, c.Path))
		default:
			add(loser, fmt.Sprintf("You and %s both changed %s. %s's version is in the file; yours is kept. Ask your agent to sort it out.", w, c.Path, w))
			add(winner, fmt.Sprintf("You and %s both changed %s. Your version is in the file; %s's is kept. Ask your agent to sort it out.", l, c.Path, l))
		}
		log.Printf("[CREW_TEAM_SYNC] clash_notified project=%s path=%q revision=%d winner=%s loser=%s", project, c.Path, c.WinningRevision, winner, loser)
	}
	if len(job.direct) > 0 {
		notifyEnqueue(job)
	}
}

// crewTeamOpenClashPaths are the files with an unresolved clash, newest first, each once.
func crewTeamOpenClashPaths(clashes []crewTeamClash) []string {
	paths := []string{}
	for i := len(clashes) - 1; i >= 0; i-- {
		if c := clashes[i]; !c.Resolved && !containsExactString(paths, c.Path) {
			paths = append(paths, c.Path)
		}
	}
	return paths
}

// crewTeamClashLine is the sync line's clash part: "1 clash to sort out: notes.md", or "" with none open.
func crewTeamClashLine(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	shown := paths
	more := ""
	if len(shown) > 3 {
		shown, more = shown[:3], fmt.Sprintf(" and %d more", len(paths)-3)
	}
	return fmt.Sprintf("%d %s to sort out: %s%s", len(paths), plural(len(paths), "clash", "clashes"), strings.Join(shown, ", "), more)
}

// crewTeamClashSide is one side of a clash for an agent: who, which revision, and its text (text files up to 2 MiB).
type crewTeamClashSide struct {
	Who      string `json:"who"`
	Revision uint64 `json:"revision"`
	Text     string `json:"text,omitempty"`
	Removed  bool   `json:"removed,omitempty"`
	NotText  bool   `json:"not_text,omitempty"`
}

// crewTeamClashView is an open clash as the Sync panel and agents read it. ID is the clash's winning revision.
type crewTeamClashView struct {
	crewTeamClash
	ID      string             `json:"id"`
	When    string             `json:"when,omitempty"`
	Current *crewTeamClashSide `json:"current,omitempty"`
	Other   *crewTeamClashSide `json:"other,omitempty"`
}

// crewTeamClashRemoved: the clash's losing side was a stale delete (the winner's edit was kept at that revision).
func (s crewTeamFileState) crewTeamClashRemoved(c crewTeamClash) bool {
	for _, e := range s.Events {
		if e.Path == c.Path && e.Revision == c.WinningRevision {
			return e.Kind == "kept"
		}
	}
	return false
}

// crewTeamClashViewOf adds when it happened and, with text, both versions: the file as it is now and the other
// person's version from history. Only the shared folder's own versions are read.
func crewTeamClashViewOf(store string, s crewTeamFileState, c crewTeamClash, withText bool) crewTeamClashView {
	v := crewTeamClashView{crewTeamClash: c, ID: strconv.FormatUint(c.WinningRevision, 10)}
	for _, e := range s.Events {
		if e.Path == c.Path && e.Revision == c.WinningRevision {
			v.When = e.At
		}
	}
	if !withText {
		return v
	}
	read := func(side *crewTeamClashSide, version *crewTeamFileVersion) {
		if version == nil {
			side.Removed = true
			return
		}
		var buf bytes.Buffer
		if version.Size > crewTeamMergeMaxBytes || crewTeamAssemble(store, *version, &buf) != nil || !crewTeamMergeText(buf.Bytes()) {
			side.NotText = true
			return
		}
		side.Text = buf.String()
	}
	current := s.Versions[c.Path]
	v.Current = &crewTeamClashSide{Who: crewPersonShortName(current.Author), Revision: current.Revision}
	var now *crewTeamFileVersion
	if current.File != nil {
		now = &crewTeamFileVersion{Hash: current.File.Hash, Size: current.File.Size, Exec: current.File.Executable, Chunks: current.Chunks}
	}
	read(v.Current, now)
	other := c.LosingAuthor
	v.Other = &crewTeamClashSide{Who: crewPersonShortName(other), Revision: c.LosingRevision}
	if s.crewTeamClashRemoved(c) {
		v.Other.Removed = true
	} else {
		read(v.Other, s.versionAt(c.Path, c.LosingRevision))
	}
	return v
}

// crewTeamOpenClash finds the open clash for a path: the one with this id (winning revision), or the newest.
func (s crewTeamFileState) crewTeamOpenClash(path string, id uint64) (crewTeamClash, bool) {
	for i := len(s.Clashes) - 1; i >= 0; i-- {
		if c := s.Clashes[i]; c.Path == path && !c.Resolved && (id == 0 || c.WinningRevision == id) {
			return c, true
		}
	}
	return crewTeamClash{}, false
}
