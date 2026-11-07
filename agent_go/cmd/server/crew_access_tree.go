package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Share tree under the side panel's Share row (owner, 5 Nov, approved mockup "Access Side Panel"): one item per
// thing worth saying, each a title and a muted detail, so nothing is cut off. Lines with nothing to say are left out.
// The server writes every line; the Mac app and the website show the same tree.

type crewAccessPerson struct {
	UserID      string `json:"user_id"` // screens find the person's tabs by account, never by name
	Name        string `json:"name"`
	Online      bool   `json:"online"`
	OnlineLabel string `json:"online_label,omitempty"` // the dot's words, from the server
	AgentsLabel string `json:"agents_label,omitempty"` // "8 agents": the person's chip in the tab strip names them
	StatusLabel string `json:"status_label,omitempty"`
}

type crewAccessLine struct {
	Kind     string             `json:"kind"` // mac, team, where, agents, dashboard, public, folder, clash
	Title    string             `json:"title"`
	Detail   string             `json:"detail,omitempty"`
	Path     string             `json:"path,omitempty"`   // dashboard: the document the line opens
	Ask      string             `json:"ask,omitempty"`    // dashboard: none yet; the words put in the person's Main chat box
	Target   string             `json:"target,omitempty"` // dashboard: the Share tab the line opens ("dashboards")
	People   []crewAccessPerson `json:"people,omitempty"`
	Progress *crewTeamRound     `json:"progress,omitempty"`
}

// crewAccessFacts is what the tree is made from; the Mac and the server each fill it from where they stand.
type crewAccessFacts struct {
	MacOnline  bool
	TeamKnown  bool
	Team       *crewTeam
	People     []crewTeamAgentsPerson // everyone in the Team with their in-team agents (crew_team_agents.go)
	Agents     []macSharedAgent
	LastChange time.Time
	Clashes    []crewTeamClash
	Progress   *crewTeamRound
	// This Mac's own copy of the shared folder (absent on the website): its last good round, files and last error.
	LocalSynced time.Time
	LocalFiles  int
	LocalError  string
	HasLocal    bool
	Viewer      string
	// ServerCrew: a Crew that lives on the server (not a Mac Crew or its online copy): it has no Mac line.
	ServerCrew bool
	// Where the viewer's own agents work, when they are a Team member: "mac" or "web" (crew_team_where.go).
	Where string
	// Member: the viewer is a teammate, not the Crew's owner; the owner's Mac and its agent choices are not theirs.
	Member bool
}

func crewAccessTree(f crewAccessFacts) []crewAccessLine {
	lines := []crewAccessLine{}
	teamOn := f.TeamKnown && f.Team != nil && f.Team.On
	// Offline is already shown on the Share row; it needs no duplicate child.
	switch {
	case f.ServerCrew || f.Member:
		// A Crew on the server has no Mac (owner, 5 Nov), and a teammate's tree is about their Team: it starts there.
	case f.MacOnline:
		// One line, no detail (owner, 5 Nov).
		lines = append(lines, crewAccessLine{Kind: "mac", Title: "Mac online"})
	}
	if !f.TeamKnown {
		return append(lines, crewAccessPublicLine(f.Agents)...)
	}
	if f.Member {
		f.Agents = nil // the owner's Mac choices; the team line lists everyone's agents
	}
	if !teamOn {
		if f.MacOnline || f.ServerCrew {
			lines = append(lines, crewAccessLine{Kind: "team", Title: "Team off", Detail: "Turn on to work on this Crew with people"})
		}
		return append(lines, crewAccessPublicLine(f.Agents)...)
	}
	people := []crewAccessPerson{}
	for _, p := range f.People {
		// The server marks the viewer's own row ("you"); on the Mac its ids are the server's, not this Mac's.
		if p.You || p.UserID == f.Viewer {
			continue
		}
		status := ""
		for _, a := range p.Agents {
			if a.StatusLabel != "" {
				status = a.StatusLabel
			}
		}
		// The name the server gave (this Mac does not know the server's people), cut to its short form.
		name := strings.SplitN(strings.TrimSpace(firstNonEmptyTrimmed(p.Name, crewPersonShortName(p.UserID))), "@", 2)[0]
		agents := ""
		if n := len(p.Agents); n > 0 {
			agents = fmt.Sprintf("%d %s", n, plural(n, "agent", "agents"))
		}
		people = append(people, crewAccessPerson{UserID: p.UserID, Name: name, Online: p.Active, OnlineLabel: p.ActiveLabel, AgentsLabel: agents, StatusLabel: status})
	}
	// The viewer is not one of the people listed (owner, 5 Nov): their own agents have the lines below.
	lines = append(lines, crewAccessLine{Kind: "team", Title: fmt.Sprintf("Team · %d other %s", len(people), plural(len(people), "person", "people")), People: people})
	// One working copy per member (owner, 5 Nov): where this member's own agents work.
	switch f.Where {
	case "mac":
		lines = append(lines, crewAccessLine{Kind: "where", Title: "On your Mac", Detail: "Your agents here work on your Mac"})
	}
	// Working on the website says itself (owner, 6 Nov: "if a user opens on web it is working on web"): no line for it.
	// What is shared with the team, by name (owner, 5 Nov: "Shared with the team" is enough).
	inTeam := []string{}
	for _, a := range f.Agents {
		if crewTeamInTeam(a) {
			inTeam = append(inTeam, firstNonEmptyTrimmed(a.Name, a.Tab))
		}
	}
	if len(inTeam) > 0 {
		lines = append(lines, crewAccessLine{Kind: "agents", Title: "Shared with the team", Detail: strings.Join(inTeam, ", ")})
	}
	lines = append(lines, crewAccessPublicLine(f.Agents)...)
	if f.Team.Folder.shared() {
		detail := "Nothing synced yet"
		switch {
		case f.Progress != nil:
			detail = crewAccessProgressLine(*f.Progress)
		case f.HasLocal && f.LocalError != "":
			detail = "Can't sync: " + f.LocalError
		case f.HasLocal && !f.LocalSynced.IsZero():
			detail = fmt.Sprintf("Up to date · checked %s · %d %s", crewAccessAgo(f.LocalSynced), f.LocalFiles, plural(f.LocalFiles, "file", "files"))
		case !f.LastChange.IsZero():
			detail = "Last change " + crewAccessAgo(f.LastChange)
		}
		if f.Progress == nil {
			if open := crewTeamOpenClashPaths(f.Clashes); len(open) == 0 {
				detail += " · no clashes"
			} else {
				detail += " · " + crewTeamClashLine(open)
			}
		}
		lines = append(lines, crewAccessLine{Kind: "folder", Title: "Shared: " + f.Team.Folder.display(), Detail: detail, Progress: f.Progress})
		for i, c := range f.Clashes {
			if i == 3 {
				break
			}
			detail := c.Path + " · " + c.WinningAuthor + " and " + firstNonEmptyTrimmed(c.LosingAuthor, "an earlier version")
			if c.Merged {
				detail += fmt.Sprintf(" · merged; %d %s changed by both kept the later version", c.Hunks, plural(c.Hunks, "part", "parts"))
			}
			lines = append(lines, crewAccessLine{Kind: "clash", Title: fmt.Sprintf("%d %s", len(f.Clashes), plural(len(f.Clashes), "clash", "clashes")), Detail: detail})
		}
	}
	return lines
}

// crewAccessDashboardLine is the Crew's dashboard in the Share tree (owner, 5 Nov: one place for Share and the
// dashboard): who it is shared with, or, with none, an Ask to make one. Nothing creates a dashboard on its own.
func crewAccessDashboardLine(paths []string, shared string) crewAccessLine {
	if len(paths) == 0 {
		return crewAccessLine{Kind: "dashboard", Title: "No dashboard", Detail: "Ask AI to make one", Ask: "Help me make a dashboard for this Crew", Target: "dashboards"}
	}
	title := "Dashboard"
	if len(paths) > 1 {
		title = fmt.Sprintf("%d dashboards", len(paths))
	}
	return crewAccessLine{Kind: "dashboard", Title: title, Detail: shared, Path: paths[0], Target: "dashboards"}
}

// crewAccessWithDashboard puts the dashboard line after "Shared with the team" (or after the Mac and Team lines when
// there is no such line), before Public, the shared folder and clashes.
func crewAccessWithDashboard(tree []crewAccessLine, line crewAccessLine) []crewAccessLine {
	at := 0
	for i, l := range tree {
		switch l.Kind {
		case "mac", "team", "where", "agents":
			at = i + 1
		}
	}
	out := append([]crewAccessLine{}, tree[:at]...)
	out = append(out, line)
	return append(out, tree[at:]...)
}

func crewAccessPublicLine(agents []macSharedAgent) []crewAccessLine {
	names := []string{}
	for _, a := range agents {
		if !a.Off && a.AgentBridgePublic {
			names = append(names, firstNonEmptyTrimmed(a.Name, a.Tab))
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []crewAccessLine{{Kind: "public", Title: "Public on AgentBridge", Detail: strings.Join(names, ", ")}}
}

func crewAccessProgressLine(p crewTeamRound) string {
	verb := "Sending"
	if p.Direction == "download" {
		verb = "Receiving"
	}
	return fmt.Sprintf("%s %d %s · %s of %s", verb, p.Files, plural(p.Files, "file", "files"), crewAccessMB(p.BytesDone), crewAccessMB(p.BytesTotal))
}

func crewAccessMB(n int64) string {
	switch {
	case n >= 10<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", max(1, n>>10))
	}
}

func crewAccessAgo(at time.Time) string {
	d := time.Since(at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return at.Local().Format("2 Jan")
	}
}

// crewAccessRemoteCache keeps the Mac's last reads of the online copy for a short while (the Access row polls often).
var crewAccessRemoteCache sync.Map

type crewAccessRemote struct {
	at      time.Time
	team    *crewTeam
	people  []crewTeamAgentsPerson
	clashes []crewTeamClash
	last    time.Time
}

// crewAccessTreeFor builds the tree where this server stands, like crewAccessSummaryFor.
func (api *StreamingAPI) crewAccessTreeFor(ctx context.Context, claims *UserClaims, project string, b *crewroot.SyncBinding) []crewAccessLine {
	owner := claims.UserID
	f := crewAccessFacts{Agents: readMacSharedAgents(project), Viewer: owner, Where: crewTeamWhereFor(project, owner)}
	if !IsLocalMode() {
		// The website: this server holds the Team and the shared folder of the Crew's online copy.
		f.MacOnline = b != nil && b.Mode == "relay"
		f.ServerCrew = !crewRelayTwin(project)
		f.TeamKnown = true // no Team record means the Team is off
		if t, err := readCrewTeam(project); err == nil {
			f.Team = &t
			f.Member = t.OwnerID != owner
			if t.On {
				f.People = teamAgentsByPerson(ctx, t, owner)
			}
			// A teammate on the website has their own copy of the shared folder here (crew_team_server_copy.go).
			if _, onMac := t.memberMac(owner); f.Member && !onMac && t.Folder.ID != "" && crewRelayTwin(project) {
				if dir, err := crewTeamServerCopyDir(owner, project, t.Folder.ID); err == nil {
					f.Progress = crewTeamRoundFor(dir)
					if local, err := readCrewTeamLocal(dir); err == nil {
						f.HasLocal, f.LocalFiles, f.LocalError = true, len(local.Files), local.Error
						f.LocalSynced, _ = time.Parse(time.RFC3339Nano, local.LastSynced)
					}
				}
			}
			if s, err := readCrewTeamFileState(project); err == nil {
				if n := len(s.Events); n > 0 {
					f.LastChange, _ = time.Parse(time.RFC3339Nano, s.Events[n-1].At)
				}
				for i := len(s.Clashes) - 1; i >= 0; i-- {
					if c := s.Clashes[i]; !c.Resolved {
						c.WinningAuthor, c.LosingAuthor = shareOwnerLabel(c.WinningAuthor), shareOwnerLabel(c.LosingAuthor)
						f.Clashes = append(f.Clashes, c)
					}
				}
			}
		}
		return crewAccessTree(f)
	}
	f.MacOnline = b != nil && b.Side == "local" && b.Mode == "relay"
	if !f.MacOnline {
		f.TeamKnown = true
		return crewAccessTree(f)
	}
	c, err := desktopConnectionFor(owner, b.ConnectionID)
	if err != nil {
		return crewAccessTree(f)
	}
	remote := api.crewAccessRemoteRead(ctx, owner, c, project, b.PeerProjectID)
	if remote.team != nil {
		f.TeamKnown, f.Team, f.People, f.Clashes, f.LastChange = true, remote.team, remote.people, remote.clashes, remote.last
		if dir, err := crewTeamLocalDir(owner, c, b.PeerProjectID, remote.team.Folder.ID); err == nil {
			f.Progress = crewTeamRoundFor(dir)
			if local, err := readCrewTeamLocal(dir); err == nil {
				f.HasLocal, f.LocalFiles, f.LocalError = true, len(local.Files), local.Error
				f.LocalSynced, _ = time.Parse(time.RFC3339Nano, local.LastSynced)
			}
		}
	}
	return crewAccessTree(f)
}

func (api *StreamingAPI) crewAccessRemoteRead(ctx context.Context, owner string, c desktopConnection, project, peer string) crewAccessRemote {
	key := project + "\x00" + peer
	if v, ok := crewAccessRemoteCache.Load(key); ok && time.Since(v.(crewAccessRemote).at) < 20*time.Second {
		return v.(crewAccessRemote)
	}
	round, done := context.WithTimeout(ctx, 6*time.Second)
	defer done()
	var out crewAccessRemote
	var t crewTeam
	route := "/api/crew-teams/" + url.PathEscape(peer)
	if crewTeamRemoteJSON(round, owner, c, http.MethodGet, route, nil, &t) != nil {
		if v, ok := crewAccessRemoteCache.Load(key); ok {
			return v.(crewAccessRemote) // keep the last good read rather than saying something wrong
		}
		return out
	}
	out.team = &t
	if t.On {
		var agents struct {
			People []crewTeamAgentsPerson `json:"people"`
		}
		if crewTeamRemoteJSON(round, owner, c, http.MethodGet, route+"/agents", nil, &agents) == nil {
			out.people = agents.People
		}
	}
	if t.FolderAccess && t.Folder.ID != "" {
		folder := "?folder_id=" + url.QueryEscape(t.Folder.ID)
		var clashes struct {
			Clashes []crewTeamClash `json:"clashes"`
		}
		if crewTeamRemoteJSON(round, owner, c, http.MethodGet, route+"/clashes"+folder, nil, &clashes) == nil {
			out.clashes = clashes.Clashes
		}
		var events struct {
			Events []crewTeamEvent `json:"events"`
		}
		if crewTeamRemoteJSON(round, owner, c, http.MethodGet, route+"/events"+folder, nil, &events) == nil && len(events.Events) > 0 {
			out.last, _ = time.Parse(time.RFC3339Nano, events.Events[0].At)
		}
	}
	out.at = time.Now()
	crewAccessRemoteCache.Store(key, out)
	return out
}

// dropCrewAccessCache makes the next Access read fresh after a Team change on this Mac.
func dropCrewAccessCache(project, peer string) {
	crewAccessRemoteCache.Delete(project + "\x00" + peer)
}
