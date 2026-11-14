package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Mac Crew is reached from the phone and the web only when its owner turns "Reach it from your phone and the web" on
// (owner, 3 Nov: Off by default): that creates its empty online copy (the Mac's "relay" action). Nothing pairs a
// Crew in the background (PLAT-856-509).

// The server must know empty twins; an older one would create a real Crew instead.
func desktopServerSupportsRelay(ctx context.Context, owner string, connection desktopConnection) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, connection.Server+"/api/crew-sync-capabilities", nil)
	if err != nil {
		return false
	}
	request.Header.Set("Authorization", "Bearer "+connection.Token)
	response, err := desktopHTTPClient().Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var out struct {
		Relay bool `json:"relay"`
	}
	return response.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&out) == nil && out.Relay
}

func (api *StreamingAPI) createDesktopCrewRelayTwin(ctx context.Context, owner string, connection desktopConnection, id, name string) error {
	roots, err := api.crewSyncRoots(ctx, &UserClaims{UserID: owner, Username: owner}, id)
	if err != nil {
		return err
	}
	pair := uuid.NewString()
	macName := desktopMacDisplayName()
	previous := readCrewTeamState(roots.ID).PreviousProject
	remote, err := desktopCrewSyncCall(ctx, owner, &connection, "new", crewSyncRequest{Action: "create", Mode: "relay", SplitFiles: roots.SeparateFiles(), PairID: pair, SourceID: roots.ID, Name: name, MacName: macName, MacID: desktopRelayDeviceID(connection.ID), PreviousProjectID: previous})
	if err != nil {
		return err
	}
	if remote.Binding == nil || remote.ProjectID == "" || remote.Binding.Mode != "relay" || remote.Binding.PairID == "" {
		return fmt.Errorf("the server did not create an empty twin; update the server")
	}
	pair = remote.Binding.PairID // an existing twin keeps its pair
	if err = crewroot.WriteSync(roots.ID, crewroot.SyncBinding{Mode: "relay", SplitFiles: roots.SeparateFiles(), PairID: pair, Side: "local", Editing: "local", ConnectionID: connection.ID, PeerProjectID: remote.ProjectID, PeerServer: connection.Server, MacName: macName}); err != nil {
		return err
	}
	if previous != "" {
		_ = writeCrewTeamState(roots.ID, func(s *crewTeamLocalState) { s.PreviousProject = "" })
	}
	log.Printf("[CREW_RELAY] twin project=%s server_project=%s server=%s previous_project=%s", roots.ID, remote.ProjectID, connection.Server, previous)
	return nil
}

// The name people see for this Mac, such as "Manish's MacBook Air", not its network hostname.
func desktopMacDisplayName() string {
	if out, err := exec.Command("scutil", "--get", "ComputerName").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	name, _ := os.Hostname()
	return strings.TrimSuffix(name, ".local")
}

// existingCrewRelayTwin finds the caller's twin of a Mac Crew on this server.
func (api *StreamingAPI) existingCrewRelayTwin(ctx context.Context, claims *UserClaims, source string) (string, *crewroot.SyncBinding) {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", nil
	}
	entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !ok || strings.HasSuffix(id, "-files") {
			continue
		}
		b, e := crewroot.ReadSync(id)
		if e != nil || b == nil || b.Side != "server" || b.Mode != "relay" || b.PeerProjectID != source {
			continue
		}
		if _, e = api.crewSyncRoots(ctx, claims, id); e == nil {
			return id, b
		}
	}
	return "", nil
}

func handleCrewSyncCapabilities(w http.ResponseWriter, r *http.Request) {
	externalJSON(w, map[string]bool{"relay": true, "shared_agents": true})
}

const macCrewShareBlocked = "This Crew lives on your Mac. So people can use it any time, even when your Mac is off, put the agent they use on the server first: open its menu and choose Run online."

// crewRelayTwin reports a server Crew that is only the empty twin of a Mac Crew.
func crewRelayTwin(id string) bool {
	if id == "" {
		return false
	}
	b, err := crewroot.ReadSync(id)
	return err == nil && b != nil && b.Side == "server" && b.Editing == "local"
}
