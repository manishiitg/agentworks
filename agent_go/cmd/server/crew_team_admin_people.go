package server

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// An admin sees what everyone on this server is doing, not only people in their Teams (owner, 25 Nov, #3900):
// people working in their own projects, and Mac apps running on the server's AI account. Built from the cost ledger,
// which records every turn by person and project; it shows activity and counts only, never chat titles or text,
// so a person's private chats stay private.

type crewTeamAdminPerson struct {
	UserID   string   `json:"user_id"`
	Person   string   `json:"person"`
	Active   bool     `json:"active,omitempty"`
	Last     string   `json:"last,omitempty"`
	Done     string   `json:"done,omitempty"`
	Projects []string `json:"projects,omitempty"`
	// Mac is their Mac app's work on the server's AI account: "340K tokens · Claude".
	Mac string `json:"mac,omitempty"`
	// Online: their Mac app is connected to this server now (what it works on stays on the Mac).
	Online bool `json:"online,omitempty"`
	// Quiet: an account with no work on this server this week; the menu folds these into one line.
	Quiet bool `json:"quiet,omitempty"`
}

// crewTeamAdminActiveFor: someone whose last turn ended this recently is still at work.
const crewTeamAdminActiveFor = 5 * time.Minute

type crewTeamAdminUsage struct {
	sessions    map[string]bool
	tokens      int64
	last        time.Time
	projects    map[string]string // product id -> name
	macTokens   int64
	macPrograms map[string]bool
}

type crewTeamAdminCached struct {
	at    time.Time
	users map[string]*crewTeamAdminUsage
}

var (
	crewTeamAdminMu         sync.Mutex
	crewTeamAdminCache      crewTeamAdminCached
	crewTeamAdminRefreshing bool
)

// crewTeamAdminUsageToday reads the last week of the ledger (counts cover the last 24 hours) for the whole server. Only the first read waits; after
// that an admin's poll gets the last summary at once and an older one is read again in the background, one read at
// a time (reading on the poll made every admin's Team menu several times slower).
func crewTeamAdminUsageToday(ctx context.Context) map[string]*crewTeamAdminUsage {
	crewTeamAdminMu.Lock()
	cached := crewTeamAdminCache
	stale := time.Since(cached.at) >= crewTeamOverviewFor
	start := stale && cached.users != nil && !crewTeamAdminRefreshing
	if start {
		crewTeamAdminRefreshing = true
	}
	crewTeamAdminMu.Unlock()
	if cached.users == nil {
		users := crewTeamAdminReadUsage(ctx)
		crewTeamAdminMu.Lock()
		crewTeamAdminCache = crewTeamAdminCached{at: time.Now(), users: users}
		crewTeamAdminMu.Unlock()
		return users
	}
	if start {
		go func() {
			users := crewTeamAdminReadUsage(context.Background())
			crewTeamAdminMu.Lock()
			crewTeamAdminCache = crewTeamAdminCached{at: time.Now(), users: users}
			crewTeamAdminRefreshing = false
			crewTeamAdminMu.Unlock()
		}()
	}
	return cached.users
}

func crewTeamAdminReadUsage(ctx context.Context) map[string]*crewTeamAdminUsage {
	users := map[string]*crewTeamAdminUsage{}
	api := runningServerAPI
	if api == nil || api.costLedger == nil {
		return users
	}
	now := time.Now()
	// A week for "last active", so someone who worked two days ago is not missing; counts stay the last 24 hours.
	since := now.Add(-7 * 24 * time.Hour)
	today := now.Add(-24 * time.Hour)
	loc := costledger.DayLocation()
	_ = api.costLedger.VisitEntries(ctx, since.In(loc).Format("2006-01-02"), now.In(loc).Format("2006-01-02"), func(e costledger.Entry) error {
		user := firstNonEmptyTrimmed(e.ActorID, e.UserID)
		if user == "" || e.Timestamp.Before(since) {
			return nil
		}
		var a costledger.Aggregate
		a.AddEntry(e)
		n := crewTeamWorkTokens(a)
		u := users[user]
		if u == nil {
			u = &crewTeamAdminUsage{sessions: map[string]bool{}, projects: map[string]string{}, macPrograms: map[string]bool{}}
			users[user] = u
		}
		if e.Timestamp.After(u.last) {
			u.last = e.Timestamp
		}
		// The name is looked up once per project, not on every entry: it reads the project's files.
		if e.Activity != "mac_app" && e.ProductID != "" && u.projects[e.ProductID] == "" {
			u.projects[e.ProductID] = firstNonEmptyTrimmed(e.ProductName, crewTeamAdminProductName(e.ProductKind, e.ProductID))
		}
		if e.Timestamp.Before(today) {
			return nil
		}
		if e.Activity == "mac_app" {
			u.macTokens += n
			if e.Provider != "" {
				u.macPrograms[providerAccountProductName(e.Provider)] = true
			}
			return nil
		}
		u.tokens += n
		if e.SessionID != "" {
			u.sessions[e.SessionID] = true
		}
		return nil
	})
	return users
}

func crewTeamAdminProductName(kind, id string) string {
	if kind == "crew" {
		if _, title := crewOwnerAndTitle(id); title != "" {
			return title
		}
	}
	return ""
}

// crewTeamAdminMacsOnline is who has a Mac app connected to this server right now.
func crewTeamAdminMacsOnline() map[string]bool {
	out := map[string]bool{}
	crewMacSockets.Lock()
	defer crewMacSockets.Unlock()
	for _, sock := range crewMacSockets.byDevice {
		out[sock.owner] = true
	}
	return out
}

// crewTeamAdminPeople is everyone on this server outside the viewer's listed Teams, for an admin only (owner, 25 Nov:
// "i dont see enough people"): people with work this week, people whose Mac app is connected now, and every other
// account as quiet. The viewer, their Teams' people and work already shown in their Teams are left out.
func crewTeamAdminPeople(ctx context.Context, viewer string, teams []crewTeamLiveView) []crewTeamAdminPerson {
	inTeams, teamPeople := map[string]bool{}, map[string]bool{}
	for _, team := range teams {
		inTeams[team.ProjectID] = true
		for _, p := range team.People {
			teamPeople[p.UserID] = true
		}
	}
	now := time.Now()
	label := shareOwnerLabeler() // reads the user directory once, not once per person
	online := crewTeamAdminMacsOnline()
	usage := crewTeamAdminUsageToday(ctx)
	out := []crewTeamAdminPerson{}
	seen := map[string]bool{viewer: true}
	for user, u := range usage {
		if seen[user] {
			continue
		}
		projects := []string{}
		for id, name := range u.projects {
			if !inTeams[id] && name != "" {
				projects = append(projects, name)
			}
		}
		slices.Sort(projects)
		projects = slices.Compact(projects)
		if len(projects) == 0 && u.macTokens == 0 && teamPeople[user] {
			continue // all their work is in the viewer's Teams, shown there
		}
		seen[user] = true
		p := crewTeamAdminPerson{UserID: user, Person: firstNonEmptyTrimmed(label(user), "Someone"), Active: now.Sub(u.last) < crewTeamAdminActiveFor, Projects: projects, Online: online[user]}
		if !u.last.IsZero() {
			p.Last = u.last.UTC().Format(time.RFC3339)
		}
		if len(projects) > 0 && (len(u.sessions) > 0 || u.tokens > 0) {
			p.Done = crewTeamOverviewDoneLine(crewTeamOverviewCounts{Chats: len(u.sessions), Tokens: u.tokens})
		}
		if u.macTokens > 0 {
			programs := slices.Sorted(func(yield func(string) bool) {
				for name := range u.macPrograms {
					if !yield(name) {
						return
					}
				}
			})
			p.Mac = strings.Join(append([]string{crewTeamOverviewTokensLabel(u.macTokens) + " tokens"}, programs...), " · ")
		}
		out = append(out, p)
	}
	// The rest of the accounts: a connected Mac app, or quiet. People in the viewer's Teams already show there.
	if dir, err := loadUserDirectory(); err == nil {
		for _, user := range directoryUsers(dir) {
			if seen[user] || teamPeople[user] {
				continue
			}
			seen[user] = true
			out = append(out, crewTeamAdminPerson{UserID: user, Person: firstNonEmptyTrimmed(label(user), "Someone"), Online: online[user], Quiet: !online[user]})
		}
	}
	// Active people first, then connected Mac apps, then the most recent; quiet accounts by name.
	slices.SortFunc(out, func(a, b crewTeamAdminPerson) int {
		for _, first := range [][2]bool{{a.Active, b.Active}, {!a.Quiet, !b.Quiet}, {a.Online, b.Online}} {
			if first[0] != first[1] {
				if first[0] {
					return -1
				}
				return 1
			}
		}
		if a.Quiet {
			return strings.Compare(strings.ToLower(a.Person), strings.ToLower(b.Person))
		}
		return strings.Compare(b.Last, a.Last)
	})
	return out
}
