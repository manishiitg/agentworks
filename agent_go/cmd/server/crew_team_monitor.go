package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type crewTeamMonitorConnection struct {
	Connection string             `json:"connection"`
	Network    string             `json:"network,omitempty"`
	NetworkID  string             `json:"network_id,omitempty"`
	Teams      []crewTeamLiveView `json:"teams"`
	// Others is, for an admin of that server only, everyone working there outside the viewer's Teams.
	Others []crewTeamAdminPerson `json:"others,omitempty"`
	Error  string                `json:"error,omitempty"`
}
type crewTeamMonitorReply struct {
	Connections []crewTeamMonitorConnection `json:"connections"`
	Notice      string                      `json:"notice,omitempty"`
	// People is the "By person" view: everyone in the viewer's Teams, across servers, with what they did.
	People []crewTeamOverviewPerson `json:"people"`
	// Tokens is the tokens used in the last 24 hours across all the listed Teams.
	Tokens int64 `json:"tokens"`
	// Summary is the menu's top line: "3 working now · 4 Teams active today · 2.4M tokens".
	Summary string `json:"summary,omitempty"`
}

// Team membership, not the own-project sidebar, is the monitor's inventory.
func crewTeamMonitorFor(ctx context.Context, viewer, period string) ([]crewTeamLiveView, error) {
	out := []crewTeamLiveView{}
	root, err := crewroot.StateRoot()
	if err != nil {
		return out, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	teams := []crewTeam{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		team, err := readCrewTeam(entry.Name())
		if err != nil || !crewTeamReachOpen(team, viewer) {
			continue
		}
		teams = append(teams, team)
	}
	// Independent admitted Teams do not wait in series behind one slow Mac.
	out = make([]crewTeamLiveView, len(teams))
	var wg sync.WaitGroup
	for i, team := range teams {
		wg.Add(1)
		go func(i int, team crewTeam) {
			defer wg.Done()
			out[i] = crewTeamLiveFor(ctx, team, teamAgentsByPerson(ctx, team, viewer), viewer)
			overview := crewTeamOverviewOf(ctx, team, out[i], viewer, period, false)
			out[i].Overview = &overview
			// The overview's newest item (a file change, a chat, a commit) is also work: after a restart no agent may have
			// worked yet, and the Team still shows when it last did.
			if overview.Last != nil && overview.Last.At.After(out[i].lastWorkedAt) {
				out[i].lastWorkedAt = overview.Last.At
			}
			out[i].Work = crewTeamMonitorWorkFor(out[i], time.Now())
		}(i, team)
	}
	wg.Wait()
	return out, nil
}

func (api *StreamingAPI) handleCrewTeamMonitor(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !crewTeamClaims(claims, "", false) || claims.AccessToken != nil || claims.Scope != "" {
		externalError(w, 403, "team_forbidden", "Open your Teams from your own AgentWorks account.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// The Team menu reads today; the Teams overview page also reads the last 7 days (period=week).
	period, _, _ := crewTeamOverviewWindow(r.URL.Query().Get("period"))
	teams, err := crewTeamMonitorFor(r.Context(), claims.UserID, period)
	if err != nil {
		externalError(w, 503, "team_unavailable", "Your Teams could not be loaded. Try again.")
		return
	}
	local := crewTeamMonitorConnection{Connection: "local", Teams: teams}
	if !IsLocalMode() && claimsIsAdmin(claims) {
		local.Others = crewTeamAdminPeople(r.Context(), claims.UserID, teams)
	}
	out := crewTeamMonitorReply{Connections: []crewTeamMonitorConnection{local}}
	failed := []string{}
	if IsLocalMode() {
		out.Connections[0].Network = "This Mac"
		connections, err := readDesktopConnections(claims.UserID)
		if err != nil {
			externalError(w, 503, "team_unavailable", "Your connected Teams could not be loaded. Try again.")
			return
		}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, connection := range connections {
			wg.Add(1)
			go func(c desktopConnection) {
				defer wg.Done()
				result := crewTeamMonitorConnection{Connection: c.ID, Network: crewTeamMonitorServerName(c), NetworkID: c.Server, Teams: []crewTeamLiveView{}}
				// A recent good read answers this poll at once and the server is read again in the background: one server
				// that doesn't answer held every poll for its whole wait (17-20 s) and slowed the whole app.
				if teams, ok := crewTeamMonitorLastGood(claims.UserID, c.ID, period, true); ok {
					result.Teams = teams
					crewTeamMonitorRefresh(claims.UserID, c, period, crewTeamMonitorRefreshWait)
				} else if reason, failed := crewTeamMonitorRecentFailure(claims.UserID, c.ID, period); failed {
					result.Error = reason
					crewTeamMonitorRefresh(claims.UserID, c, period, crewTeamMonitorFirstWait)
				} else {
					teams, err := crewTeamMonitorRead(r.Context(), claims.UserID, c, period, crewTeamMonitorFirstWait)
					if err != nil {
						result.Error = err.Error()
					} else {
						result.Teams = teams
					}
				}
				if v, ok := crewTeamMonitorOthers.Load(crewTeamMonitorKey(claims.UserID, c.ID, period)); ok {
					result.Others = v.([]crewTeamAdminPerson)
				}
				if result.Error != "" {
					label := result.Network
					if label == "" {
						label = "A saved server"
					}
					mu.Lock()
					failed = append(failed, label+": "+result.Error)
					mu.Unlock()
				}
				mu.Lock()
				out.Connections = append(out.Connections, result)
				mu.Unlock()
			}(connection)
		}
		wg.Wait()
	}
	for _, source := range out.Connections {
		outcome := "listed"
		if source.Error != "" {
			outcome = "unavailable"
		}
		log.Printf("[TEAM_MONITOR] connection=%s viewer=%s teams=%d dropped_no_live=0 outcome=%s", source.Connection, claims.UserID, len(source.Teams), outcome)
	}
	// Name each server and its real reason; a count alone is not actionable.
	slices.Sort(failed)
	out.Notice = strings.Join(failed, " · ")
	out.People, out.Tokens = crewTeamMonitorPeople(out.Connections)
	out.Summary = crewTeamMonitorSummary(out.Connections, out.Tokens, period)
	externalJSON(w, out)
}

// The first read of a server waits briefly; refreshes behind a recent good read may take longer.
var (
	crewTeamMonitorFirstWait   = 5 * time.Second
	crewTeamMonitorRefreshWait = 30 * time.Second
)

var crewTeamMonitorRefreshing sync.Map // viewer\x00connection -> refresh in progress

// A server that just failed is reported at once and retried in the background, not waited on by every poll.
const crewTeamMonitorFailureFor = 30 * time.Second

type crewTeamMonitorFailed struct {
	at     time.Time
	reason string
}

var crewTeamMonitorFailures sync.Map // viewer\x00connection -> crewTeamMonitorFailed

// crewTeamMonitorKey keys what the monitor remembers per viewer, saved server and period.
func crewTeamMonitorKey(viewer, connection, period string) string {
	return viewer + "\x00" + connection + "\x00" + period
}

func crewTeamMonitorRecentFailure(viewer, connection, period string) (string, bool) {
	v, ok := crewTeamMonitorFailures.Load(crewTeamMonitorKey(viewer, connection, period))
	if !ok || time.Since(v.(crewTeamMonitorFailed).at) > crewTeamMonitorFailureFor {
		return "", false
	}
	return v.(crewTeamMonitorFailed).reason, true
}

// crewTeamMonitorRefresh reads one server again in the background, one read per server at a time.
func crewTeamMonitorRefresh(viewer string, c desktopConnection, period string, wait time.Duration) {
	key := crewTeamMonitorKey(viewer, c.ID, period)
	if _, busy := crewTeamMonitorRefreshing.LoadOrStore(key, true); busy {
		return
	}
	go func() {
		defer crewTeamMonitorRefreshing.Delete(key)
		if _, err := crewTeamMonitorRead(context.Background(), viewer, c, period, wait); err != nil {
			// A server that keeps failing drops out once its last good read is older than crewTeamMonitorLastGoodFor.
			if _, _, _, transient := crewTeamMonitorFailure(err); !transient {
				crewTeamMonitorGoodReads.Delete(key)
			}
		}
	}()
}

// crewTeamMonitorRead reads one saved server's Teams and remembers a good read.
func crewTeamMonitorRead(ctx context.Context, viewer string, c desktopConnection, period string, wait time.Duration) ([]crewTeamLiveView, error) {
	key := crewTeamMonitorKey(viewer, c.ID, period)
	round, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var reply crewTeamMonitorReply
	if err := crewTeamRemoteJSON(round, viewer, c, http.MethodGet, "/api/crew-team-monitor?period="+period, nil, &reply); err != nil {
		status, code, reason, _ := crewTeamMonitorFailure(err)
		log.Printf("[TEAM_MONITOR] connection=%s viewer=%s outcome=failed status=%d code=%q reason=%q wait=%s", c.ID, viewer, status, code, reason, wait)
		crewTeamMonitorFailures.Store(key, crewTeamMonitorFailed{at: time.Now(), reason: reason})
		return nil, &crewTeamMonitorReadError{reason: reason, cause: err}
	}
	teams := []crewTeamLiveView{}
	others := []crewTeamAdminPerson{}
	for _, source := range reply.Connections {
		others = append(others, source.Others...)
		for _, live := range source.Teams {
			crewTeamLiveLocalSelf(&live, c)
			live.Connection = c.ID
			teams = append(teams, live)
		}
	}
	crewTeamMonitorGoodReads.Store(key, crewTeamMonitorGood{at: time.Now(), teams: teams})
	crewTeamMonitorOthers.Store(key, others)
	crewTeamMonitorFailures.Delete(key)
	return teams, nil
}

type crewTeamMonitorReadError struct {
	reason string
	cause  error
}

func (e *crewTeamMonitorReadError) Error() string { return e.reason }
func (e *crewTeamMonitorReadError) Unwrap() error { return e.cause }

// crewTeamMonitorFailure turns one failed remote monitor read into its status,
// code, a plain reason and whether it is the kind that clears on the next poll.
func crewTeamMonitorFailure(err error) (status int, code, reason string, transient bool) {
	var httpErr *crewRemoteHTTPError
	var transport *crewRemoteTransportError
	var syntax *json.SyntaxError
	switch {
	case errors.As(err, &syntax):
		// An older server answers a route it doesn't have with its web page (#3900).
		return 0, "not_json", "needs an update to show Teams", false
	case errors.As(err, &transport):
		return 0, "transport", "didn't answer", true
	case errors.As(err, &httpErr):
		status, code = httpErr.Status, httpErr.Code
		switch {
		case status == http.StatusUnauthorized:
			return status, code, "sign in again", false
		case status == http.StatusForbidden:
			return status, code, "Teams aren't open to this sign-in", false
		case status == http.StatusNotFound:
			return status, code, "needs an update to show Teams", false
		case status >= 500:
			return status, code, "Teams didn't load", true
		}
		return status, code, "Teams didn't load", false
	}
	// 502/504 come back as plain errors from crewTeamRemoteDo; decode errors too.
	return 0, "", "Teams didn't load", true
}

const crewTeamMonitorLastGoodFor = 2 * time.Minute

type crewTeamMonitorGood struct {
	at    time.Time
	teams []crewTeamLiveView
}

var crewTeamMonitorGoodReads sync.Map // viewer\x00connection -> crewTeamMonitorGood

var crewTeamMonitorOthers sync.Map // crewTeamMonitorKey -> []crewTeamAdminPerson from that server's last good read

func crewTeamMonitorLastGood(viewer, connection, period string, transient bool) ([]crewTeamLiveView, bool) {
	if !transient {
		return nil, false
	}
	v, ok := crewTeamMonitorGoodReads.Load(crewTeamMonitorKey(viewer, connection, period))
	if !ok || time.Since(v.(crewTeamMonitorGood).at) > crewTeamMonitorLastGoodFor {
		return nil, false
	}
	return v.(crewTeamMonitorGood).teams, true
}

// One Team-level summary, built only from the viewer-admitted live projection.
// Hidden/terminal journals are never read here; their explicit posts may appear.
type crewTeamMonitorWork struct {
	Working         int    `json:"working"`
	Label           string `json:"label,omitempty"`
	LastWorkedAt    string `json:"last_worked_at,omitempty"`
	LastWorkedLabel string `json:"last_worked_label,omitempty"`
}

func crewTeamMonitorWorkFor(live crewTeamLiveView, now time.Time) *crewTeamMonitorWork {
	out := &crewTeamMonitorWork{Working: len(live.Agents)}
	if out.Working > 0 {
		out.Label = fmt.Sprintf("%d working", out.Working)
	}
	at := live.lastWorkedAt
	// Preserve observed activity for stable recency ordering. Current work is
	// proof of activity only when its timestamp is otherwise unknown; quiet
	// reads never reset it.
	if out.Working > 0 && at.IsZero() {
		at = now
	}
	if !at.IsZero() {
		out.LastWorkedAt = at.UTC().Format(time.RFC3339Nano)
		age := max(time.Duration(0), now.Sub(at))
		switch {
		case age < time.Minute:
			out.LastWorkedLabel = "just now"
		case age < time.Hour:
			out.LastWorkedLabel = fmt.Sprintf("%dm ago", int(age/time.Minute))
		case age < 24*time.Hour:
			out.LastWorkedLabel = fmt.Sprintf("%dh ago", int(age/time.Hour))
		default:
			out.LastWorkedLabel = fmt.Sprintf("%dd ago", int(age/(24*time.Hour)))
		}
	}
	return out
}

func crewTeamMonitorServerName(c desktopConnection) string {
	name := friendlyServerName(c.Name, c.Server)
	if strings.EqualFold(name, "server") || strings.ContainsAny(name, ".:/") {
		return ""
	}
	return name
}

// crewTeamMonitorPeople merges each Team's people into one row per person and server, adding up what they did in
// all of the viewer's Teams, and the tokens of every listed Team.
func crewTeamMonitorPeople(connections []crewTeamMonitorConnection) ([]crewTeamOverviewPerson, int64) {
	merged := map[string]*crewTeamOverviewPerson{}
	order := []string{}
	var tokens int64
	for _, source := range connections {
		for _, team := range source.Teams {
			if team.Overview == nil {
				continue
			}
			tokens += team.Overview.Counts.Tokens
			for _, p := range team.Overview.People {
				key := source.Connection + "\x00" + p.UserID
				m := merged[key]
				if m == nil {
					m = &crewTeamOverviewPerson{UserID: p.UserID, Person: p.Person, Network: source.Network, Now: []crewTeamOverviewNow{}, Teams: []string{}}
					merged[key] = m
					order = append(order, key)
				}
				m.Working += p.Working
				m.Now = append(m.Now, p.Now...)
				if p.Working > 0 || p.Counts != (crewTeamOverviewCounts{}) {
					m.Teams = append(m.Teams, team.Name)
				}
				m.Counts.Chats += p.Counts.Chats
				m.Counts.Files += p.Counts.Files
				m.Counts.Commits += p.Counts.Commits
				m.Counts.Decisions += p.Counts.Decisions
				m.Counts.Tokens += p.Counts.Tokens
				if p.Last != nil && (m.Last == nil || p.Last.At.After(m.Last.At)) {
					last := *p.Last
					last.Line += " in " + team.Name
					m.Last = &last
				}
			}
		}
	}
	out := []crewTeamOverviewPerson{}
	for _, key := range order {
		merged[key].Done = crewTeamOverviewDoneLine(merged[key].Counts)
		out = append(out, *merged[key])
	}
	crewTeamOverviewSortPeople(out)
	return out, tokens
}

// crewTeamMonitorSummary says in one line how much is going on across the viewer's Teams, leaving out what is zero.
func crewTeamMonitorSummary(connections []crewTeamMonitorConnection, tokens int64, period string) string {
	_, label, _ := crewTeamOverviewWindow(period)
	if period == "week" {
		label = "this week"
	}
	working, active := 0, 0
	for _, source := range connections {
		for _, team := range source.Teams {
			working += len(team.Agents)
			if team.Overview != nil && !team.Overview.Quiet {
				active++
			}
		}
	}
	parts := []string{}
	if working > 0 {
		parts = append(parts, fmt.Sprintf("%d working now", working))
	}
	switch {
	case active == 1:
		parts = append(parts, "1 Team active "+label)
	case active > 1:
		parts = append(parts, fmt.Sprintf("%d Teams active %s", active, label))
	}
	if tokens > 0 {
		parts = append(parts, crewTeamOverviewTokensLabel(tokens)+" tokens")
	}
	return strings.Join(parts, " · ")
}
