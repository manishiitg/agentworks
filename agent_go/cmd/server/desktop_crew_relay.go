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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Every Mac Crew gets an empty twin on its one server, so its owner can use it
// from the website or phone while the Mac is on. Files move only when the
// owner turns on "Work with my Mac off" (PLAT-856-509, A1).
func (api *StreamingAPI) startDesktopCrewRelayTwins() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if !IsLocalMode() {
		return cancel
	}
	go func() {
		for {
			api.ensureDesktopCrewRelayTwins(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}()
	return cancel
}

// A failed twin waits before the next try, so an error never creates a server Crew every minute.
var crewRelayRetryAt = struct {
	sync.Mutex
	next map[string]time.Time
	wait map[string]time.Duration
}{next: map[string]time.Time{}, wait: map[string]time.Duration{}}

func crewRelayBackoff(id string, failed bool) bool {
	crewRelayRetryAt.Lock()
	defer crewRelayRetryAt.Unlock()
	if !failed {
		return time.Now().Before(crewRelayRetryAt.next[id])
	}
	wait := crewRelayRetryAt.wait[id]*2 + time.Hour
	if wait > 24*time.Hour {
		wait = 24 * time.Hour
	}
	crewRelayRetryAt.wait[id], crewRelayRetryAt.next[id] = wait, time.Now().Add(wait)
	return true
}

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

// With several connected servers the owner picks the Crew's server in its
// Cloud panel; a Crew belongs to exactly one server.
func (api *StreamingAPI) ensureDesktopCrewRelayTwins(ctx context.Context) {
	owner := GetDefaultUserID()
	connections, err := readDesktopConnections(owner)
	if err != nil || len(connections) != 1 || !desktopServerSupportsRelay(ctx, owner, connections[0]) {
		return
	}
	crews, err := listCrewProjectsForOwner(ctx, owner, owner, "")
	if err != nil {
		return
	}
	for _, crew := range crews {
		id, _ := crew["id"].(string)
		name, _ := crew["name"].(string)
		if id == "" || ctx.Err() != nil {
			continue
		}
		if b, e := crewroot.ReadSync(id); e != nil || b != nil || crewRelayBackoff(id, false) {
			continue
		}
		if err := api.createDesktopCrewRelayTwin(ctx, owner, connections[0], id, name); err != nil {
			crewRelayBackoff(id, true)
			log.Printf("[CREW_RELAY] twin project=%s failed: %v", id, err)
		}
	}
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

const macCrewShareBlocked = "This Crew runs on your Mac. To share its dashboard and Ask, move it to the server first: turn on Work with my Mac off in Cloud."

// crewRelayTwin reports a server Crew that is only the empty twin of a Mac Crew.
func crewRelayTwin(id string) bool {
	if id == "" {
		return false
	}
	b, err := crewroot.ReadSync(id)
	return err == nil && b != nil && b.Side == "server" && b.Mode == "relay"
}
