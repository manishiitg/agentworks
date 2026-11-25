package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// The Team overview says what a Team is doing now and what it got done, from what really happened: its agents'
// chats, Team file changes, commits in the project folder, decisions and tokens used (#3900). It is built only
// from rows the viewer already sees in the live Team view; chat titles follow the same Live rule as previews and
// chat text never appears.

type crewTeamOverviewCounts struct {
	Chats     int   `json:"chats"`
	Files     int   `json:"files"`
	Commits   int   `json:"commits"`
	Decisions int   `json:"decisions"`
	Tokens    int64 `json:"tokens"`
}

type crewTeamOverviewNow struct {
	Agent  string `json:"agent"`
	Person string `json:"person"`
	Title  string `json:"title,omitempty"`
	Team   string `json:"team,omitempty"`
}

type crewTeamOverviewItem struct {
	Kind   string    `json:"kind"`
	Person string    `json:"person"`
	Line   string    `json:"line"`
	At     time.Time `json:"at"`
}

type crewTeamOverviewChat struct {
	Title    string    `json:"title,omitempty"`
	Agent    string    `json:"agent"`
	Person   string    `json:"person"`
	Messages int       `json:"messages,omitempty"`
	At       time.Time `json:"at"`
	ownerID  string
	session  string
}

type crewTeamOverviewPath struct {
	Path  string `json:"path"`
	Saves int    `json:"saves"`
}

type crewTeamOverviewFiles struct {
	Person string                 `json:"person"`
	Folder string                 `json:"folder"`
	Line   string                 `json:"line"`
	Files  int                    `json:"files"`
	Saves  int                    `json:"saves"`
	Paths  []crewTeamOverviewPath `json:"paths,omitempty"`
	At     time.Time              `json:"at"`
	actor  string
}

type crewTeamOverviewCommit struct {
	Title  string    `json:"title"`
	Author string    `json:"author"`
	Repo   string    `json:"repo,omitempty"`
	At     time.Time `json:"at"`
}

type crewTeamOverviewAgentTokens struct {
	Agent  string `json:"agent"`
	Tokens int64  `json:"tokens"`
}

type crewTeamOverviewTokens struct {
	Person string                        `json:"person"`
	Tokens int64                         `json:"tokens"`
	Agents []crewTeamOverviewAgentTokens `json:"agents,omitempty"`
	userID string
}

// One person's part of a Team (or, merged by the monitor, of all the viewer's Teams).
type crewTeamOverviewPerson struct {
	UserID  string                 `json:"user_id"`
	Person  string                 `json:"person"`
	Network string                 `json:"network,omitempty"`
	Working int                    `json:"working"`
	Now     []crewTeamOverviewNow  `json:"now"`
	Teams   []string               `json:"teams"`
	Counts  crewTeamOverviewCounts `json:"counts"`
	Done    string                 `json:"done,omitempty"`
	Last    *crewTeamOverviewItem  `json:"last,omitempty"`
}

type crewTeamOverview struct {
	Period    string                   `json:"period"`
	Label     string                   `json:"label"`
	Now       []crewTeamOverviewNow    `json:"now"`
	NowMore   int                      `json:"now_more,omitempty"`
	Counts    crewTeamOverviewCounts   `json:"counts"`
	Done      string                   `json:"done,omitempty"`
	Last      *crewTeamOverviewItem    `json:"last,omitempty"`
	Chats     []crewTeamOverviewChat   `json:"chats,omitempty"`
	Files     []crewTeamOverviewFiles  `json:"files,omitempty"`
	Commits   []crewTeamOverviewCommit `json:"commits,omitempty"`
	Decisions []crewTeamOverviewItem   `json:"decisions,omitempty"`
	Tokens    []crewTeamOverviewTokens `json:"tokens,omitempty"`
	People    []crewTeamOverviewPerson `json:"people"`
}

// Periods are rolling windows, so they mean the same on every server whatever its time zone.
func crewTeamOverviewWindow(period string) (string, string, time.Duration) {
	if period == "week" {
		return "week", "last 7 days", 7 * 24 * time.Hour
	}
	return "day", "last 24 hours", 24 * time.Hour
}

type crewTeamOverviewCached struct {
	at  time.Time
	out crewTeamOverview
}

var crewTeamOverviewCache sync.Map // project\x00viewer\x00period\x00detail -> crewTeamOverviewCached

// The menu polls often; the overview's inputs (history files, the cost ledger, git) are read at most every 30 s.
const crewTeamOverviewFor = 30 * time.Second

// crewTeamOverviewOf builds the overview of team for viewer from its live view. detail adds the lists the
// Overview page shows; the menu needs only the counts, the newest item and who is working.
func crewTeamOverviewOf(ctx context.Context, t crewTeam, live crewTeamLiveView, viewer, period string, detail bool) crewTeamOverview {
	period, label, span := crewTeamOverviewWindow(period)
	key := strings.Join([]string{t.ProjectID, viewer, period, fmt.Sprint(detail)}, "\x00")
	now := time.Now()
	out := crewTeamOverview{Period: period, Label: label, Now: []crewTeamOverviewNow{}, People: []crewTeamOverviewPerson{}}
	for _, row := range live.Agents {
		out.Now = append(out.Now, crewTeamOverviewNow{Agent: row.Name, Person: row.Person, Title: row.Title})
	}
	if v, ok := crewTeamOverviewCache.Load(key); ok && now.Sub(v.(crewTeamOverviewCached).at) < crewTeamOverviewFor {
		cached := v.(crewTeamOverviewCached).out
		cached.Now, cached.NowMore = out.Now, 0
		return crewTeamOverviewFinish(cached, live, detail)
	}
	since := now.Add(-span)
	labels := crewTeamPersonLabels(t)
	name := func(user string) string {
		if user == viewer {
			return "You"
		}
		return firstNonEmptyTrimmed(labels[user], shareOwnerLabel(user))
	}
	chats := crewTeamOverviewChats(ctx, t, live, since)
	files := crewTeamOverviewFileChanges(t, since, name)
	commits := crewTeamOverviewCommits(ctx, t.ProjectID, since)
	decisions := []crewTeamOverviewItem{}
	decisionOwners := []string{}
	_, posts, _ := crewTeamRequestPosts(ctx, t, viewer)
	for _, post := range posts {
		if post.Private || post.At.Before(since) || strings.TrimSpace(post.Line) == "" {
			continue
		}
		decisions = append(decisions, crewTeamOverviewItem{Kind: "decision", Person: name(post.OwnerID), Line: crewTeamMessageLine(post.Line), At: post.At})
		decisionOwners = append(decisionOwners, post.OwnerID)
	}
	tokens := crewTeamOverviewTokensFor(ctx, t.ProjectID, since, chats, name)

	out.Counts = crewTeamOverviewCounts{Chats: len(chats), Commits: len(commits), Decisions: len(decisions)}
	for _, group := range files {
		out.Counts.Files += group.Files
	}
	for _, row := range tokens {
		out.Counts.Tokens += row.Tokens
	}
	items := []crewTeamOverviewItem{}
	for _, c := range chats {
		items = append(items, crewTeamOverviewItem{Kind: "chat", Person: c.Person, Line: crewTeamOverviewChatLine(c), At: c.At})
	}
	for _, f := range files {
		items = append(items, crewTeamOverviewItem{Kind: "files", Person: f.Person, Line: f.Line, At: f.At})
	}
	for _, c := range commits {
		items = append(items, crewTeamOverviewItem{Kind: "commit", Person: c.Author, Line: "committed “" + c.Title + "”", At: c.At})
	}
	for _, d := range decisions {
		items = append(items, crewTeamOverviewItem{Kind: "decision", Person: d.Person, Line: "decided “" + d.Line + "”", At: d.At})
	}
	if newest := crewTeamOverviewNewest(items); newest != nil {
		out.Last = newest
	}

	// People: everyone in the Team, with what they and their agents did.
	people := map[string]*crewTeamOverviewPerson{}
	person := func(user string) *crewTeamOverviewPerson {
		if people[user] == nil {
			people[user] = &crewTeamOverviewPerson{UserID: user, Person: name(user), Now: []crewTeamOverviewNow{}, Teams: []string{live.Name}}
		}
		return people[user]
	}
	for _, user := range append([]string{t.OwnerID}, crewTeamPeopleIDs(t)...) {
		person(user)
	}
	for _, row := range live.Agents {
		p := person(row.OwnerID)
		p.Working++
		p.Now = append(p.Now, crewTeamOverviewNow{Agent: row.Name, Person: p.Person, Title: row.Title, Team: live.Name})
	}
	personItems := map[string][]crewTeamOverviewItem{}
	for _, c := range chats {
		person(c.ownerID).Counts.Chats++
		personItems[c.ownerID] = append(personItems[c.ownerID], crewTeamOverviewItem{Kind: "chat", Person: c.Person, Line: crewTeamOverviewChatLine(c), At: c.At})
	}
	for _, f := range files {
		person(f.actor).Counts.Files += f.Files
		personItems[f.actor] = append(personItems[f.actor], crewTeamOverviewItem{Kind: "files", Person: f.Person, Line: f.Line, At: f.At})
	}
	for i, d := range decisions {
		person(decisionOwners[i]).Counts.Decisions++
		personItems[decisionOwners[i]] = append(personItems[decisionOwners[i]], crewTeamOverviewItem{Kind: "decision", Person: d.Person, Line: "decided “" + d.Line + "”", At: d.At})
	}
	for _, row := range tokens {
		person(row.userID).Counts.Tokens += row.Tokens
	}
	for user, p := range people {
		p.Last = crewTeamOverviewNewest(personItems[user])
		out.People = append(out.People, *p)
	}
	crewTeamOverviewSortPeople(out.People)

	if detail {
		out.Chats = chats[:min(len(chats), 50)]
		out.Files = files
		out.Commits = commits[:min(len(commits), 50)]
		out.Decisions = decisions
		out.Tokens = tokens
	}
	crewTeamOverviewCache.Store(key, crewTeamOverviewCached{at: now, out: out})
	return crewTeamOverviewFinish(out, live, detail)
}

// crewTeamOverviewFinish applies the parts that must be current on every read: who is working now.
func crewTeamOverviewFinish(out crewTeamOverview, live crewTeamLiveView, detail bool) crewTeamOverview {
	working := map[string][]crewTeamOverviewNow{}
	for _, row := range live.Agents {
		working[row.OwnerID] = append(working[row.OwnerID], crewTeamOverviewNow{Agent: row.Name, Person: row.Person, Title: row.Title, Team: live.Name})
	}
	people := slices.Clone(out.People)
	for i := range people {
		people[i].Now = append([]crewTeamOverviewNow{}, working[people[i].UserID]...)
		people[i].Working = len(people[i].Now)
		people[i].Done = crewTeamOverviewDoneLine(people[i].Counts)
	}
	out.People = people
	if !detail && len(out.Now) > 2 {
		out.NowMore = len(out.Now) - 2
		out.Now = out.Now[:2]
	}
	out.Done = crewTeamOverviewDoneLine(out.Counts)
	return out
}

func crewTeamPeopleIDs(t crewTeam) []string {
	out := []string{}
	for _, p := range t.People {
		if p.UserID != t.OwnerID {
			out = append(out, p.UserID)
		}
	}
	return out
}

// Working people first, then the most recent work, then by name.
func crewTeamOverviewSortPeople(people []crewTeamOverviewPerson) {
	slices.SortFunc(people, func(a, b crewTeamOverviewPerson) int {
		if (a.Working > 0) != (b.Working > 0) {
			if a.Working > 0 {
				return -1
			}
			return 1
		}
		var at, bt time.Time
		if a.Last != nil {
			at = a.Last.At
		}
		if b.Last != nil {
			bt = b.Last.At
		}
		if c := bt.Compare(at); c != 0 {
			return c
		}
		return strings.Compare(a.Person, b.Person)
	})
}

func crewTeamOverviewNewest(items []crewTeamOverviewItem) *crewTeamOverviewItem {
	var newest *crewTeamOverviewItem
	for i := range items {
		if newest == nil || items[i].At.After(newest.At) {
			newest = &items[i]
		}
	}
	if newest == nil {
		return nil
	}
	item := *newest
	return &item
}

func crewTeamOverviewChatLine(c crewTeamOverviewChat) string {
	if c.Title == "" {
		return "worked with " + c.Agent
	}
	return "worked on “" + c.Title + "” with " + c.Agent
}

// One plain line of counts, leaving out what is zero: "9 chats · 1,312 files · 3 commits · 1 decision · 2.4M tokens".
func crewTeamOverviewDoneLine(c crewTeamOverviewCounts) string {
	parts := []string{}
	add := func(n int, one, many string) {
		if n == 1 {
			parts = append(parts, "1 "+one)
		} else if n > 1 {
			parts = append(parts, crewTeamOverviewNumber(int64(n))+" "+many)
		}
	}
	add(c.Chats, "chat", "chats")
	add(c.Files, "file", "files")
	add(c.Commits, "commit", "commits")
	add(c.Decisions, "decision", "decisions")
	if c.Tokens > 0 {
		parts = append(parts, crewTeamOverviewTokensLabel(c.Tokens)+" tokens")
	}
	return strings.Join(parts, " · ")
}

func crewTeamOverviewNumber(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func crewTeamOverviewTokensLabel(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	}
	return fmt.Sprint(n)
}

// crewTeamOverviewChats lists the chats the viewer's visible agents worked in during the period. A server chat comes
// from its owner's chat history; an agent whose chats live on a Mac counts its latest chat from the shared preview.
func crewTeamOverviewChats(ctx context.Context, t crewTeam, live crewTeamLiveView, since time.Time) []crewTeamOverviewChat {
	out := []crewTeamOverviewChat{}
	rows := append(slices.Clone(live.Agents), live.Idle...)
	byOwner := map[string]map[string]crewTeamLiveAgent{}
	for _, row := range rows {
		if row.ChatKey == "" {
			continue
		}
		if byOwner[row.OwnerID] == nil {
			byOwner[row.OwnerID] = map[string]crewTeamLiveAgent{}
		}
		byOwner[row.OwnerID][row.ChatKey] = row
	}
	for owner, keys := range byOwner {
		found := map[string]bool{}
		for _, s := range crewTeamChatSessions(ctx, owner, t.ProjectID, func(key string) bool { _, ok := keys[key]; return ok }) {
			at, err := time.Parse(time.RFC3339Nano, firstNonEmptyTrimmed(s.LastMessageAt, s.UpdatedAt))
			if err != nil || at.Before(since) {
				continue
			}
			row := keys[s.key]
			found[s.key] = true
			chat := crewTeamOverviewChat{Agent: row.Name, Person: row.Person, Messages: s.MessageCount, At: at, ownerID: owner, session: s.SessionID}
			// The title follows the agent's Live rule, the same as its preview: a hidden agent's chat is only counted.
			if crewTeamOverviewTitleShared(t, row) {
				chat.Title = crewTeamMessageLine(s.Title)
			}
			out = append(out, chat)
		}
		for key, row := range keys {
			if found[key] {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, row.LastMessageAt)
			if err != nil || at.Before(since) {
				continue
			}
			out = append(out, crewTeamOverviewChat{Title: row.Title, Agent: row.Name, Person: row.Person, At: at, ownerID: owner})
		}
	}
	slices.SortFunc(out, func(a, b crewTeamOverviewChat) int { return b.At.Compare(a.At) })
	return out
}

// A chat title is shared when its agent is the viewer's own or shares its chat Live with the Team.
func crewTeamOverviewTitleShared(t crewTeam, row crewTeamLiveAgent) bool {
	if row.You {
		return true
	}
	registered, ok := t.Agents[row.ID]
	return ok && registered.InTeam && registered.Kind != "terminal" && registered.Rule.ChatShare == crewChatShareLive
}

type crewTeamChatSession struct {
	ChatHistorySession
	key string
}

// crewTeamChatSessions is every saved chat session of owner's chats in project that include admits.
func crewTeamChatSessions(ctx context.Context, owner, project string, include func(key string) bool) []crewTeamChatSession {
	out := []crewTeamChatSession{}
	path := productConversationRegistryPath(owner)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, path)
	mu.Unlock()
	if err != nil {
		return out
	}
	indexes := map[string]chatHistoryIndex{}
	seen := map[string]bool{}
	for _, record := range doc.Entries {
		if record.ProfileID != crewProfileID || crewResourceProjectID(record.ConversationKey) != project || !include(record.ConversationKey) {
			continue
		}
		conv, ok := workProjectChatHistoryConversationPath(owner, record.WorkspacePath, record.SessionID, time.Now())
		if !ok {
			conv = chatHistoryConversationPath(owner, record.SessionID, time.Now())
		}
		indexPath := chatHistoryIndexWorkspacePath(owner, conv)
		index, loaded := indexes[indexPath]
		if !loaded {
			index = newChatHistoryIndex()
			if data, exists, e := readFileFromWorkspace(ctx, indexPath); e == nil && exists {
				_ = json.Unmarshal([]byte(data), &index)
			}
			indexes[indexPath] = index
		}
		for _, entry := range index.Entries {
			session := entry.Session
			if record.SessionID == "" || session.SessionID != record.SessionID || seen[session.SessionID] {
				continue
			}
			seen[session.SessionID] = true
			if session.Title == "" {
				session.Title = record.Title
			}
			out = append(out, crewTeamChatSession{ChatHistorySession: session, key: record.ConversationKey})
		}
	}
	return out
}

// crewTeamOverviewFileChanges groups the Team file changes since the time by person and folder, so a thousand files
// copied in at once are one line. Repeated saves of one file count once.
func crewTeamOverviewFileChanges(t crewTeam, since time.Time, name func(string) string) []crewTeamOverviewFiles {
	out := []crewTeamOverviewFiles{}
	l, err := openCrewTeamLedger(t.ProjectID, false)
	if err != nil {
		return out
	}
	defer l.db.Close()
	rows, err := l.db.Query(`SELECT path,actor,at FROM events WHERE folder=? AND at>=? AND kind NOT IN ('shared','kept') ORDER BY ordinal`, l.folder, since.UTC().Format(time.RFC3339))
	if err != nil {
		return out
	}
	defer rows.Close()
	type group struct {
		out          crewTeamOverviewFiles
		saves        map[string]int
		first, final time.Time
	}
	groups := map[string]*group{}
	for rows.Next() {
		var p, actor, at string
		if rows.Scan(&p, &actor, &at) != nil || !t.Folder.holds(p) {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			continue
		}
		folder := crewTeamOverviewFolder(p)
		k := actor + "\x00" + folder
		g := groups[k]
		if g == nil {
			g = &group{out: crewTeamOverviewFiles{Person: name(actor), Folder: folder, actor: actor}, saves: map[string]int{}, first: when}
			groups[k] = g
		}
		g.saves[p]++
		g.out.Saves++
		g.final = when
	}
	for _, g := range groups {
		g.out.Files, g.out.At = len(g.saves), g.final
		for p, n := range g.saves {
			g.out.Paths = append(g.out.Paths, crewTeamOverviewPath{Path: p, Saves: n})
		}
		slices.SortFunc(g.out.Paths, func(a, b crewTeamOverviewPath) int {
			if a.Saves != b.Saves {
				return b.Saves - a.Saves
			}
			return strings.Compare(a.Path, b.Path)
		})
		g.out.Paths = g.out.Paths[:min(len(g.out.Paths), 50)]
		where := g.out.Folder
		if where == "" {
			where = "the Team folder"
		}
		switch {
		case g.out.Files == 1:
			g.out.Line = "changed " + g.out.Paths[0].Path
		case g.out.Files >= 20 && g.final.Sub(g.first) < 2*time.Minute:
			g.out.Line = "changed " + crewTeamOverviewNumber(int64(g.out.Files)) + " files at once in " + where
		default:
			g.out.Line = "changed " + crewTeamOverviewNumber(int64(g.out.Files)) + " files in " + where
		}
		out = append(out, g.out)
	}
	slices.SortFunc(out, func(a, b crewTeamOverviewFiles) int { return b.At.Compare(a.At) })
	return out
}

// The folder a change is grouped under: the first two folders of its path ("team/assets/").
func crewTeamOverviewFolder(p string) string {
	dir := path.Dir(filepath.ToSlash(p))
	if dir == "." || dir == "/" {
		return ""
	}
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	return strings.Join(parts[:min(len(parts), 2)], "/") + "/"
}

// crewTeamOverviewCommits reads the commits made since the time in the project folder's git repositories: the
// folder itself and the folders directly inside it. Nothing is fetched; it is the history already on this machine.
func crewTeamOverviewCommits(ctx context.Context, project string, since time.Time) []crewTeamOverviewCommit {
	out := []crewTeamOverviewCommit{}
	root, err := crewTeamProjectFilesRoot(project)
	if err != nil {
		return out
	}
	repos := []string{}
	if crewTeamOverviewIsRepo(root) {
		repos = append(repos, root)
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && crewTeamOverviewIsRepo(filepath.Join(root, e.Name())) && len(repos) < 10 {
				repos = append(repos, filepath.Join(root, e.Name()))
			}
		}
	}
	for _, repo := range repos {
		run, cancel := context.WithTimeout(ctx, 3*time.Second)
		data, err := exec.CommandContext(run, "git", "-C", repo, "log", "-n", "200", "--since="+since.UTC().Format(time.RFC3339), "--format=%an%x1f%aI%x1f%s").Output()
		cancel()
		if err != nil {
			continue
		}
		name := filepath.Base(repo)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			f := strings.SplitN(line, "\x1f", 3)
			if len(f) != 3 {
				continue
			}
			at, err := time.Parse(time.RFC3339, f[1])
			if err != nil {
				continue
			}
			out = append(out, crewTeamOverviewCommit{Title: crewTeamMessageLine(f[2]), Author: f[0], Repo: name, At: at})
		}
	}
	slices.SortFunc(out, func(a, b crewTeamOverviewCommit) int { return b.At.Compare(a.At) })
	return out
}

func crewTeamOverviewIsRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// crewTeamOverviewTokensFor adds up the tokens this server recorded for the project since the time, by the person
// who started the work and by agent (through the chat each session belongs to).
func crewTeamOverviewTokensFor(ctx context.Context, project string, since time.Time, chats []crewTeamOverviewChat, name func(string) string) []crewTeamOverviewTokens {
	out := []crewTeamOverviewTokens{}
	api := runningServerAPI
	if api == nil || api.costLedger == nil {
		return out
	}
	agentOf := map[string]string{}
	for _, c := range chats {
		if c.session != "" {
			agentOf[c.session] = c.Agent
		}
	}
	loc := costledger.DayLocation()
	byPerson := map[string]*crewTeamOverviewTokens{}
	byAgent := map[string]map[string]int64{}
	_ = api.costLedger.VisitEntries(ctx, since.In(loc).Format("2006-01-02"), time.Now().In(loc).Format("2006-01-02"), func(e costledger.Entry) error {
		if e.ProductKind != "crew" || e.ProductID != project || e.Timestamp.Before(since) {
			return nil
		}
		var a costledger.Aggregate
		a.AddEntry(e)
		tokens := int64(a.InputTokens + a.CompletionTokens)
		if tokens <= 0 {
			return nil
		}
		user := firstNonEmptyTrimmed(e.ActorID, e.UserID)
		if byPerson[user] == nil {
			byPerson[user] = &crewTeamOverviewTokens{Person: name(user), userID: user}
			byAgent[user] = map[string]int64{}
		}
		byPerson[user].Tokens += tokens
		byAgent[user][firstNonEmptyTrimmed(agentOf[e.SessionID], "Other work")] += tokens
		return nil
	})
	for user, row := range byPerson {
		for agent, n := range byAgent[user] {
			row.Agents = append(row.Agents, crewTeamOverviewAgentTokens{Agent: agent, Tokens: n})
		}
		slices.SortFunc(row.Agents, func(a, b crewTeamOverviewAgentTokens) int { return int(b.Tokens - a.Tokens) })
		out = append(out, *row)
	}
	slices.SortFunc(out, func(a, b crewTeamOverviewTokens) int { return int(b.Tokens - a.Tokens) })
	return out
}

// GET /api/crew-teams/{project}/overview?period=day|week: the Overview page, with its lists.
func (api *StreamingAPI) handleCrewTeamOverview(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, false) {
		externalError(w, 403, "team_forbidden", "Open this Team from your own AgentWorks account.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamReachOpen(t, claims.UserID) {
		externalError(w, 404, "team_not_found", "This Team isn't open to you.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	live := crewTeamLiveFor(r.Context(), t, teamAgentsByPerson(r.Context(), t, claims.UserID), claims.UserID)
	externalJSON(w, crewTeamOverviewOf(r.Context(), t, live, claims.UserID, r.URL.Query().Get("period"), true))
}
