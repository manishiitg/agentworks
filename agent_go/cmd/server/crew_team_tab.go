package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Team tab (owner, 5 Nov, approved mockup "Single Team Tab"): one tab for the owner and every teammate, in the Mac
// app and on the website, for a Mac Crew and a Crew on the server. The server writes every row and says what the
// viewer may change; the screen only shows it. People are grouped by account, never by a name.

type crewTeamTabPerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	// Note is the muted word after the name: owner, you or invited.
	Note        string `json:"note,omitempty"`
	Active      bool   `json:"active"`
	ActiveLabel string `json:"active_label,omitempty"`
	CanRemove   bool   `json:"can_remove"`
}

type crewTeamTabAgent struct {
	// Key is the agent's chat key: In team / Only me sends it (agent-visibility).
	Key  string `json:"key"`
	Name string `json:"name"`
	// Detail is whose it is and where it works: "Manish", "tester-b · On their Mac", "you · On your Mac".
	Detail string `json:"detail"`
	InTeam bool   `json:"in_team"`
	// CanChange: the viewer chooses In team / Only me for it here. Otherwise Fixed is shown instead of the choice.
	CanChange bool   `json:"can_change"`
	Fixed     string `json:"fixed,omitempty"`
	// ChosenOnMac: the viewer's own agent on their Mac; the Mac app lists those from the Mac itself.
	ChosenOnMac bool `json:"chosen_on_mac,omitempty"`
	// Address is the send_message target for "Ask from my agent".
	Address string `json:"address,omitempty"`
}

// crewTeamTabMac is the Crew owner's Mac (a Mac Crew only; a Crew on the server has no Mac).
type crewTeamTabMac struct {
	On   bool   `json:"on"`
	Line string `json:"line"` // "On · MacBook Air"
	Note string `json:"note"` // who can change it and where
	// Yours: the viewer owns this Mac, so their Mac app shows its switch instead of the note.
	Yours bool `json:"yours"`
}

// crewTeamTabSync is the shared folder's state as this server sees it for the viewer.
type crewTeamTabSync struct {
	Line     string         `json:"line"`
	Problem  bool           `json:"problem,omitempty"`
	Last     string         `json:"last,omitempty"` // "Last change 4 min ago by tester-b: team/notes.md"
	Progress *crewTeamRound `json:"progress,omitempty"`
}

type crewTeamTabReply struct {
	Team   crewTeam            `json:"team"`
	Intro  string              `json:"intro"`
	People []crewTeamTabPerson `json:"people"`
	Agents []crewTeamTabAgent  `json:"agents"`
	Mac    *crewTeamTabMac     `json:"mac,omitempty"`
	Sync   *crewTeamTabSync    `json:"sync,omitempty"`
	// Switch is where the Team switch and the folder choice work: "server" here, or "mac" (only the owner's Mac app,
	// which shares its agents and makes team/ when it turns on).
	Switch   string `json:"switch"`
	CanLeave bool   `json:"can_leave"`
}

func (api *StreamingAPI) handleCrewTeamTab(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, false) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return
	}
	owner := ownerOfCrewProjectID(project)
	t, err := readCrewTeam(project)
	if os.IsNotExist(err) && crewRegistryOwnedByCaller(claims.UserID, crewProjectRootByID(project)) {
		t, err = crewTeam{ProjectID: project, OwnerID: owner, People: []crewTeamPerson{}}, nil
	}
	if err != nil || !crewTeamCanManage(t, claims.UserID) || t.OwnerID != owner || !crewTeamAccountActive(owner) {
		externalError(w, 403, "team_forbidden", "This Crew isn't shared with you right now. Ask its owner to add you to its Team.")
		return
	}
	t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return !crewTeamAccountActive(p.UserID) })
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, crewTeamTabFor(r.Context(), t, claims.UserID))
}

func crewTeamTabFor(ctx context.Context, t crewTeam, viewer string) crewTeamTabReply {
	view := t.view(viewer)
	view.Lent = crewTeamLentNow(ctx, t)
	twin := crewRelayTwin(t.ProjectID)
	ownerName := crewPersonShortName(t.OwnerID)
	out := crewTeamTabReply{Team: view, People: []crewTeamTabPerson{}, Agents: []crewTeamTabAgent{}, Switch: "server",
		CanLeave: viewer != t.OwnerID && t.member(viewer),
		Intro:    "People work with you here. Everything is kept on the server."}
	if twin {
		out.Switch = "mac"
		out.Intro = "People work with you here. The shared folder is kept on the server. Messages to Mac agents wait while the Mac is off and arrive when it is back."
		mac := &crewTeamTabMac{Yours: viewer == t.OwnerID}
		name := ""
		if b, err := crewroot.ReadSync(t.ProjectID); err == nil && b != nil && b.Mode == "relay" && b.Side == "server" {
			mac.On = crewMacSocketFor(t.OwnerID, t.ProjectID, b.PairID) != nil
			name = b.MacName
		}
		mac.Line = map[bool]string{true: "On", false: "Off"}[mac.On] + " · " + firstNonEmptyTrimmed(name, ownerName+"'s Mac")
		mac.Note = "Change these from the Mac app."
		if !mac.Yours {
			mac.Note = fmt.Sprintf("Only %s can change this, from their Mac app.", ownerName)
		}
		out.Mac = mac
	}
	if !t.On {
		return out
	}
	byPerson := teamAgentsByPerson(ctx, t, viewer)
	seen := map[string]crewTeamAgentsPerson{}
	for _, p := range byPerson {
		seen[p.UserID] = p
	}
	person := func(id, note string, removable bool) {
		p := seen[id]
		out.People = append(out.People, crewTeamTabPerson{UserID: id, Name: crewPersonShortName(id), Note: note, Active: p.Active, ActiveLabel: p.ActiveLabel,
			CanRemove: removable && view.CanManage && id != viewer})
	}
	person(t.OwnerID, "owner", false) // the owner is never removed
	for _, p := range t.People {
		note := ""
		switch {
		case p.Invited:
			note = "invited"
		case p.UserID == viewer:
			note = "you" // they leave with Leave Team, not a remove button
		}
		person(p.UserID, note, true)
	}
	for _, p := range byPerson {
		mine := p.UserID == viewer
		for _, a := range p.Agents {
			detail := []string{p.Name}
			if mine {
				detail[0] = "you"
			}
			if a.Place == "mac" && !p.Owner {
				detail = append(detail, map[bool]string{true: "On your Mac", false: "On their Mac"}[mine])
			}
			if a.StatusLabel != "" {
				detail = append(detail, a.StatusLabel)
			}
			row := crewTeamTabAgent{Key: a.ChatKey, Name: a.Name, Detail: strings.Join(detail, " · "), InTeam: a.InTeam}
			switch {
			case mine && p.Owner && twin:
				// A Mac Crew owner's agents are In team or Only me on their Mac (crew_mac_shared_agents.go).
				row.ChosenOnMac, row.Fixed = true, "In team · change it from your Mac app"
			case mine:
				row.CanChange = true
			default:
				row.Fixed, row.Address = "In team · theirs to change", a.Address
			}
			out.Agents = append(out.Agents, row)
		}
	}
	out.Sync = crewTeamTabSyncFor(t, viewer)
	return out
}

// crewTeamTabSyncFor is the shared folder's state for the viewer from this server: a teammate working on the website
// has their own copy here (crew_team_server_copy.go), so its real state is shown; everyone else sees the folder kept
// here. A Mac shows its own copy's state instead (TeamSyncPanel reads it from the Mac).
func crewTeamTabSyncFor(t crewTeam, viewer string) *crewTeamTabSync {
	if !t.On || !t.edits(viewer) {
		return nil
	}
	s, _ := readCrewTeamFileState(t.ProjectID)
	files := 0
	for _, v := range s.Versions {
		if v.File != nil {
			files++
		}
	}
	clashes := 0
	for _, c := range s.Clashes {
		if !c.Resolved {
			clashes++
		}
	}
	clashLine := "no clashes"
	if clashes > 0 {
		clashLine = fmt.Sprintf("%d %s", clashes, plural(clashes, "clash", "clashes"))
	}
	out := &crewTeamTabSync{Problem: clashes > 0}
	if n := len(s.Events); n > 0 {
		e := s.Events[n-1]
		if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
			out.Last = fmt.Sprintf("Last change %s by %s: %s", crewAccessAgo(at), crewPersonShortName(firstNonEmptyTrimmed(e.Author, e.Actor)), e.Path)
		}
	}
	if _, onMac := t.memberMac(viewer); viewer != t.OwnerID && !onMac && crewRelayTwin(t.ProjectID) && t.Folder.ID != "" {
		if dir, err := crewTeamServerCopyDir(viewer, t.ProjectID, t.Folder.ID); err == nil {
			if p := crewTeamRoundFor(dir); p != nil {
				out.Line, out.Progress = crewAccessProgressLine(*p), p
				return out
			}
			if b, err := readCrewTeamLocal(dir); err == nil {
				line, problem := crewTeamCopyLine(b)
				out.Line, out.Problem = line+" · "+clashLine, out.Problem || problem
				return out
			}
			out.Line = "Your copy is made when you first use this Crew · " + clashLine
			return out
		}
	}
	out.Line = fmt.Sprintf("Kept on the server · %d %s · %s", files, plural(files, "file", "files"), clashLine)
	return out
}

// crewTeamCopyLine is one copy's sync state in plain words, and whether it is a problem.
func crewTeamCopyLine(b crewTeamLocal) (string, bool) {
	files := fmt.Sprintf("%d %s", len(b.Files), plural(len(b.Files), "file", "files"))
	ago := ""
	if at, err := time.Parse(time.RFC3339Nano, b.LastSynced); err == nil {
		ago = crewAccessAgo(at)
	}
	switch {
	case b.Error != "":
		return "Can't sync: " + b.Error + " · trying again soon", true
	case b.Status == "up_to_date":
		return strings.TrimSuffix("Up to date · checked "+ago, " · checked ") + " · " + files, false
	case !b.Downloaded:
		return "Getting the shared files · " + files + " so far", false
	case ago != "":
		return "Changes waiting to send · last synced " + ago + " · " + files, false
	}
	return "Changes waiting to send · " + files, false
}
