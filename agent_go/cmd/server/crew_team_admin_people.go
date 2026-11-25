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

// crewTeamAdminUsageToday is the last 24 hours of the ledger for the whole server. Only the first read waits; after
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
	since := now.Add(-24 * time.Hour)
	loc := costledger.DayLocation()
	_ = api.costLedger.VisitEntries(ctx, since.In(loc).Format("2006-01-02"), now.In(loc).Format("2006-01-02"), func(e costledger.Entry) error {
		user := firstNonEmptyTrimmed(e.ActorID, e.UserID)
		if user == "" || e.Timestamp.Before(since) {
			return nil
		}
		var a costledger.Aggregate
		a.AddEntry(e)
		n := int64(a.InputTokens + a.CompletionTokens)
		u := users[user]
		if u == nil {
			u = &crewTeamAdminUsage{sessions: map[string]bool{}, projects: map[string]string{}, macPrograms: map[string]bool{}}
			users[user] = u
		}
		if e.Timestamp.After(u.last) {
			u.last = e.Timestamp
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
		// The name is looked up once per project, not on every entry: it reads the project's files.
		if e.ProductID != "" && u.projects[e.ProductID] == "" {
			u.projects[e.ProductID] = firstNonEmptyTrimmed(e.ProductName, crewTeamAdminProductName(e.ProductKind, e.ProductID))
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

// crewTeamAdminPeople is everyone with work on this server in the last 24 hours outside the viewer's listed Teams,
// for an admin only. The viewer and work already shown in their Teams are left out.
func crewTeamAdminPeople(ctx context.Context, viewer string, teams []crewTeamLiveView) []crewTeamAdminPerson {
	inTeams := map[string]bool{}
	for _, team := range teams {
		inTeams[team.ProjectID] = true
	}
	now := time.Now()
	label := shareOwnerLabeler() // reads the user directory once, not once per person
	out := []crewTeamAdminPerson{}
	for user, u := range crewTeamAdminUsageToday(ctx) {
		if user == viewer {
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
		if len(projects) == 0 && u.macTokens == 0 {
			continue
		}
		p := crewTeamAdminPerson{UserID: user, Person: firstNonEmptyTrimmed(label(user), "Someone"), Active: now.Sub(u.last) < crewTeamAdminActiveFor, Projects: projects}
		if !u.last.IsZero() {
			p.Last = u.last.UTC().Format(time.RFC3339)
		}
		if len(projects) > 0 {
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
	// Active people first, then the most recent.
	slices.SortFunc(out, func(a, b crewTeamAdminPerson) int {
		if a.Active != b.Active {
			if a.Active {
				return -1
			}
			return 1
		}
		return strings.Compare(b.Last, a.Last)
	})
	return out
}
