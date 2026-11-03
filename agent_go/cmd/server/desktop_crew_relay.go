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
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
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
	remote, err := desktopCrewSyncCall(ctx, owner, &connection, "new", crewSyncRequest{Action: "create", Mode: "relay", SplitFiles: roots.SeparateFiles(), PairID: pair, SourceID: roots.ID, Name: name, MacName: macName, MacID: connection.ID})
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
	log.Printf("[CREW_RELAY] twin project=%s server_project=%s server=%s", roots.ID, remote.ProjectID, connection.Server)
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
	externalJSON(w, map[string]bool{"relay": true})
}

const macCrewShareBlocked = "This Crew lives on your Mac. So people can use it any time, even when your Mac is off, move it online first from the Mac app: Where it lives, then Move online."

// crewRelayTwin reports a server Crew that is only the empty twin of a Mac Crew.
func crewRelayTwin(id string) bool {
	if id == "" {
		return false
	}
	b, err := crewroot.ReadSync(id)
	return err == nil && b != nil && b.Side == "server" && b.Editing == "local"
}

// handleCrewSyncReadiness tells the owner, before Cloud turns on, whether this
// Crew fits a first upload and about how long it takes. Mac app only; read-only.
func (api *StreamingAPI) handleCrewSyncReadiness(w http.ResponseWriter, r *http.Request) {
	if !IsLocalMode() {
		externalError(w, 404, "local_app_required", "Open the Crew in the local AgentWorks app.")
		return
	}
	roots, err := api.crewSyncRoots(r.Context(), GetUserFromContext(r.Context()), mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	m, err := crewsync.Measure(r.Context(), roots)
	if err != nil {
		externalError(w, 409, "sync_unavailable", "Could not check this Crew's folder: "+err.Error())
		return
	}
	// What the Crew uses from the Mac's Vault: selected secrets are copied to the server, apps and
	// accounts need the same connection there (checked when Cloud turns on).
	secrets, connections := 0, []map[string]string{}
	if vaultConfigured() {
		if inventory, e := api.projectVaultInventory(r.Context(), roots.Owner, roots.Virtual); e == nil {
			secrets = len(inventory.Secrets)
			seen := map[string]bool{}
			for _, group := range inventory.Vaults {
				for _, item := range group.Items {
					if item.Selected && !seen[item.Kind+":"+item.ID] && (item.Kind == "mcp" || item.Kind == "google" || item.Kind == "slack") {
						seen[item.Kind+":"+item.ID] = true
						connections = append(connections, map[string]string{"kind": item.Kind, "name": item.Name})
					}
				}
			}
		}
	}
	// About 1.5 MB/s up from a typical laptop connection; shown as a rough minute count.
	externalJSON(w, map[string]any{"measurement": m, "fits": m.Fits(), "estimated_seconds": m.Bytes / (3 << 19), "secrets": secrets, "connections": connections})
}
