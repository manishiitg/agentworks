package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		team, err := readCrewTeam(entry.Name())
		if err != nil || !crewTeamReachOpen(team, viewer) {
			continue
		}
		out = append(out, crewTeamLiveFor(ctx, team, teamAgentsByPerson(ctx, team, viewer), viewer))
	}
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
				result := crewTeamMonitorConnection{Connection: c.ID, Network: c.Name, NetworkID: c.Server, Teams: []crewTeamLiveView{}}
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
	for _, source := range out.Connections {
		outcome := "listed"
		if source.Error != "" {
			outcome = "unavailable"
		}
		log.Printf("[TEAM_MONITOR] connection=%s viewer=%s teams=%d dropped_no_live=0 outcome=%s", source.Connection, claims.UserID, len(source.Teams), outcome)
	}
	externalJSON(w, out)
}
