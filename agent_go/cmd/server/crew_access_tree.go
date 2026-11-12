package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The tree under the side panel's Team and Online rows (owner, 5 Nov, approved mockup "Access Side Panel"; 11 Nov: the
// one Share row split into Team and Online): one item per thing worth saying, each a title and a muted detail, so
// nothing is cut off. Lines with nothing to say are left out. Each line says which row it sits under (Group). The
// server writes every line; the Mac app and the website show the same tree.

type crewAccessLine struct {
	Kind   string `json:"kind"`  // mac, team, team_live, dashboard, public, folder, clash
	Group  string `json:"group"` // the menu row it sits under: "team" or "online" (crewAccessLineGroup)
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Path   string `json:"path,omitempty"`   // dashboard: the document the line opens
	Ask    string `json:"ask,omitempty"`    // dashboard: none yet; the words put in the person's Main chat box
	Target string `json:"target,omitempty"` // dashboard: the Share tab the line opens ("dashboards")
	// Dashboards (dashboard line): each dashboard the viewer may open, by name; each opens that dashboard.
	Dashboards []crewAccessDashboard `json:"dashboards,omitempty"`
	Count      int                   `json:"count,omitempty"` // agents or dashboards: how many
	Tag        string                `json:"tag,omitempty"`   // dashboard: who can open them ("not shared", "your team", ...)
	Progress   *crewTeamRound        `json:"progress,omitempty"`
}

// crewAccessFacts is what the tree is made from; the Mac and the server each fill it from where they stand.
type crewAccessFacts struct {
	MacOnline bool
	TeamKnown bool
	// PeopleUnknown: the Team is on but its people could not be read yet; the line names no count rather than 0.
	PeopleUnknown bool
	SuppressLive  bool // kept people labels are not a fresh activity claim
	Posts         crewTeamActivityPosts
	Team          *crewTeam
	People        []crewTeamAgentsPerson // everyone in the Team with their in-team agents (crew_team_agents.go)
	Agents        []macSharedAgent
	LastChange    time.Time
	Clashes       []crewTeamClash
	Progress      *crewTeamRound
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

// crewAccessTeamOffTitle is the Team line while the Team is off; the menu's Team row then shows no count.
const crewAccessTeamOffTitle = "Team off"

// crewAccessLineGroup puts each line under its menu row: the people who work on the Crew with you and their shared
// folder under Team, and how everyone else reaches it (Mac online, AgentBridge, dashboards) under Online.
func crewAccessLineGroup(kind string) string {
	switch kind {
	case "team", "team_live", "folder", "clash":
		return "team"
	}
	return "online"
}

func crewAccessTree(f crewAccessFacts) []crewAccessLine {
	lines := crewAccessTreeLines(f)
	for i := range lines {
		lines[i].Group = crewAccessLineGroup(lines[i].Kind)
	}
	return lines
}

func crewAccessTreeLines(f crewAccessFacts) []crewAccessLine {
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
			lines = append(lines, crewAccessLine{Kind: "team", Title: crewAccessTeamOffTitle})
		}
		return append(lines, crewAccessPublicLine(f.Agents)...)
	}
	names := []string{}
	// The card is what the Team did, not your own agents' work (the left menu shows that; owner, 12 Nov): recent
	// decisions and schedule findings first, then teammates' agents working or waiting now.
	live := []crewAccessLine{}
	if !f.SuppressLive {
		recent := append(slices.Clone(f.Posts.Decisions), f.Posts.Findings...)
		slices.SortFunc(recent, func(a, b crewTeamActivityPost) int { return b.At.Compare(a.At) })
		for _, post := range recent {
			if time.Since(post.At) > 24*time.Hour {
				continue
			}
			title := post.Person + "'s " + post.Agent + " decided"
			if post.OwnerID == f.Viewer {
				title = "Your " + post.Agent + " decided"
			}
			detail := post.Line
			if post.Kind == "finding" {
				title = post.Agent
				detail = post.Schedule + ": " + post.Line
			}
			live = append(live, crewAccessLine{Kind: "team_live", Title: title, Detail: detail, Tag: post.Kind, Target: "activity"})
		}
	}
	for _, person := range f.People {
		if person.You || person.UserID == f.Viewer {
			continue
		}
		name := person.Name
		names = append(names, name)
		for _, agent := range person.Agents {
			// Activity must never reveal an Only me agent.
			if f.SuppressLive || !agent.InTeam || (agent.Activity != "running" && agent.Activity != "waiting") {
				continue
			}
			detail := "Working"
			if doing, ok := f.Posts.Doing[agent.ID]; ok {
				detail = doing.Line
			}
			if agent.Activity == "waiting" {
				detail = firstNonEmptyTrimmed(agent.StatusLabel, "Waiting for its owner")
			}
			live = append(live, crewAccessLine{Kind: "team_live", Title: name + "'s " + agent.Name, Detail: detail, Tag: agent.Activity, Target: "activity"})
		}
	}
	title := "Team · You"
	if len(names) > 0 {
		title += " + " + strings.Join(names, ", ")
	}
	if f.PeopleUnknown {
		title = "Team"
	}
	lines = append(lines, crewAccessLine{Kind: "team", Title: title, Count: len(names)})
	lines = append(lines, live[:min(3, len(live))]...)
	lines = append(lines, crewAccessPublicLine(f.Agents)...)
	if f.Team.Folder.shared() {
		if f.HasLocal && f.LocalError != "" {
			lines = append(lines, crewAccessLine{Kind: "folder", Title: "Sync stopped", Detail: f.LocalError, Target: "sync"})
		}
		for _, clash := range f.Clashes {
			if clash.Resolved {
				continue
			}
			lines = append(lines, crewAccessLine{Kind: "clash", Title: clash.Path + " has a clash", Detail: "Pick a copy", Path: clash.Path, Target: "sync"})
		}
	}
	return lines
}

// crewTeamFirstName uses the saved first name or derives it from a login.
func crewTeamFirstName(name string) string {
	words := strings.Fields(strings.SplitN(strings.TrimSpace(name), "@", 2)[0])
	if len(words) == 0 {
		return "Teammate"
	}
	first := words[0]
	if cut := strings.IndexFunc(first, func(r rune) bool { return r == '.' || r == '_' || unicode.IsDigit(r) }); cut >= 0 {
		first = first[:cut]
	}
	if first == "" {
		return "Teammate"
	}
	runes := []rune(first)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// crewAccessDashboardLine is the Crew's dashboards in the Online tree (owner, 5 Nov: one place for Share and the
// dashboard): who it is shared with, or, with none, an Ask to make one. Nothing creates a dashboard on its own.
// One header (owner, 9 Nov mock): "Dashboards", how many and who can open them, then each dashboard by name.
func crewAccessDashboardLine(items []crewAccessDashboard, shared string) crewAccessLine {
	if len(items) == 0 {
		return crewAccessLine{Kind: "dashboard", Group: "online", Title: "No artifacts yet · ask AI to make one", Ask: "Help me make an artifact for this Crew", Target: "dashboards"}
	}
	// Mock approved 9 Nov: a header "Dashboards  N" with who can open them as a tag, then one row per dashboard.
	return crewAccessLine{Kind: "dashboard", Group: "online", Title: "Artifacts", Count: len(items), Tag: crewAccessDashboardShared(shared), Path: items[0].Path, Target: "dashboards", Dashboards: items}
}

// crewAccessDashboard is one dashboard under the dashboard line: its name and the document it opens.
type crewAccessDashboard struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// crewAccessDashboardShared words the sharing read (crewDashboardSharing) for the one line.
func crewAccessDashboardShared(shared string) string {
	switch shared = strings.TrimSpace(shared); shared {
	case "":
		return ""
	case "Not shared":
		return "not shared"
	case "Shared with you":
		return "shared with you"
	case "Everyone":
		return "shared with everyone"
	case "your team":
		return "your team"
	}
	return "shared with " + shared
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
	// peopleRead: people came from a good /agents read (this one or an earlier one); without it the count is unknown.
	peopleRead bool
	liveRead   bool // current /agents attempt succeeded (not just kept labels)
	posts      crewTeamActivityPosts
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
				f.Posts = crewTeamActivityPostsFor(t)
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
		f.PeopleUnknown = remote.team.On && !remote.peopleRead
		f.SuppressLive = !remote.liveRead
		f.Posts = remote.posts
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
	var prev crewAccessRemote
	if v, ok := crewAccessRemoteCache.Load(key); ok {
		prev = v.(crewAccessRemote)
	}
	started := time.Now()
	// One server read; a failed one is logged and its last good result kept, so a slow server never turns the Team
	// line into "0 other people" (owner, 11 Nov screenshot) or hides clashes that are still open.
	read := func(step, route string, out any) bool {
		err := crewTeamRemoteJSON(round, owner, c, http.MethodGet, route, nil, out)
		if err != nil {
			log.Printf("[CREW_TEAM] project=%s event=access_read_failed peer=%s step=%s elapsed_ms=%d kept_last=%t reason=%q", project, peer, step, time.Since(started).Milliseconds(), !prev.at.IsZero(), err)
		}
		return err == nil
	}
	var out crewAccessRemote
	var t crewTeam
	route := "/api/crew-teams/" + url.PathEscape(peer)
	if !read("team", route, &t) {
		prev.liveRead = false
		return prev
	}
	out.team = &t
	out.people, out.peopleRead, out.clashes, out.last = prev.people, prev.peopleRead, prev.clashes, prev.last
	if t.On {
		var agents struct {
			People []crewTeamAgentsPerson `json:"people"`
		}
		if read("agents", route+"/agents", &agents) {
			out.people, out.peopleRead, out.liveRead = agents.People, true, true
		}
	}
	if t.On {
		var posts crewTeamActivityPosts
		if read("activity", route+"/activity-posts", &posts) {
			out.posts = posts
		}
	}
	if t.FolderAccess && t.Folder.ID != "" {
		folder := "?folder_id=" + url.QueryEscape(t.Folder.ID)
		var clashes struct {
			Clashes []crewTeamClash `json:"clashes"`
		}
		if read("clashes", route+"/clashes"+folder, &clashes) {
			out.clashes = clashes.Clashes
		}
		var events struct {
			Events []crewTeamEvent `json:"events"`
		}
		if read("events", route+"/events"+folder, &events) && len(events.Events) > 0 {
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
