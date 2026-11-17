package server

import (
	"context"
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
	Error      string             `json:"error,omitempty"`
}
type crewTeamMonitorReply struct {
	Connections []crewTeamMonitorConnection `json:"connections"`
	Notice      string                      `json:"notice,omitempty"`
}

// Team membership, not the own-project sidebar, is the monitor's inventory.
func crewTeamMonitorFor(ctx context.Context, viewer string) ([]crewTeamLiveView, error) {
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
	teams, err := crewTeamMonitorFor(r.Context(), claims.UserID)
	if err != nil {
		externalError(w, 503, "team_unavailable", "Your Teams could not be loaded. Try again.")
		return
	}
	local := crewTeamMonitorConnection{Connection: "local", Teams: teams}
	out := crewTeamMonitorReply{Connections: []crewTeamMonitorConnection{local}}
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
				round, cancel := context.WithTimeout(r.Context(), 30*time.Second)
				defer cancel()
				var reply crewTeamMonitorReply
				if err := crewTeamRemoteJSON(round, claims.UserID, c, http.MethodGet, "/api/crew-team-monitor", nil, &reply); err != nil {
					result.Error = "Teams could not be loaded. Try again."
				} else {
					for _, source := range reply.Connections {
						for _, live := range source.Teams {
							crewTeamLiveLocalSelf(&live, c)
							live.Connection = c.ID
							result.Teams = append(result.Teams, live)
						}
					}
				}
				mu.Lock()
				out.Connections = append(out.Connections, result)
				mu.Unlock()
			}(connection)
		}
		wg.Wait()
	}
	unreachable := 0
	for _, source := range out.Connections {
		outcome := "listed"
		if source.Error != "" {
			outcome = "unavailable"
			unreachable++
		}
		log.Printf("[TEAM_MONITOR] connection=%s viewer=%s teams=%d dropped_no_live=0 outcome=%s", source.Connection, claims.UserID, len(source.Teams), outcome)
	}
	if unreachable == 1 {
		out.Notice = "1 server unreachable"
	} else if unreachable > 1 {
		out.Notice = fmt.Sprintf("%d servers unreachable", unreachable)
	}
	externalJSON(w, out)
}

// One Team-level summary, built only from the viewer-admitted live projection.
// Hidden/terminal journals are never read here; their explicit posts may appear.
type crewTeamMonitorWork struct {
	Working         int      `json:"working"`
	Label           string   `json:"label,omitempty"`
	Lines           []string `json:"lines"`
	More            string   `json:"more,omitempty"`
	LastWorkedAt    string   `json:"last_worked_at,omitempty"`
	LastWorkedLabel string   `json:"last_worked_label,omitempty"`
}

func crewTeamMonitorWorkFor(live crewTeamLiveView, now time.Time) *crewTeamMonitorWork {
	out := &crewTeamMonitorWork{Working: len(live.Agents), Lines: []string{}}
	rows := slices.Clone(live.Agents)
	// Stable order: a map-backed registry must not shuffle the two summary lines.
	slices.SortFunc(rows, func(a, b crewTeamLiveAgent) int { return strings.Compare(a.OwnerID+"/"+a.ID, b.OwnerID+"/"+b.ID) })
	for _, row := range rows[:min(2, len(rows))] {
		line := firstNonEmptyTrimmed(row.LastMessage, row.Doing, row.Waiting, row.Activity)
		out.Lines = append(out.Lines, row.Name+" · "+crewTeamMessageLine(line)+" · "+row.Person)
	}
	if out.Working > 0 {
		out.Label = fmt.Sprintf("%d working", out.Working)
	}
	if out.Working > 2 {
		out.More = fmt.Sprintf("+%d more", out.Working-2)
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
			out.LastWorkedLabel = "last worked just now"
		case age < time.Hour:
			out.LastWorkedLabel = fmt.Sprintf("last worked %dm ago", int(age/time.Minute))
		case age < 24*time.Hour:
			out.LastWorkedLabel = fmt.Sprintf("last worked %dh ago", int(age/time.Hour))
		default:
			out.LastWorkedLabel = fmt.Sprintf("last worked %dd ago", int(age/(24*time.Hour)))
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
