package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
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

// With several connected servers the owner picks the Crew's server in its
// Cloud panel; a Crew belongs to exactly one server.
func (api *StreamingAPI) ensureDesktopCrewRelayTwins(ctx context.Context) {
	owner := GetDefaultUserID()
	connections, err := readDesktopConnections(owner)
	if err != nil || len(connections) != 1 {
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
		if b, e := crewroot.ReadSync(id); e != nil || b != nil {
			continue
		}
		if err := api.createDesktopCrewRelayTwin(ctx, owner, connections[0], id, name); err != nil {
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
	if remote.Binding == nil || remote.ProjectID == "" || remote.Binding.Mode != "relay" {
		return fmt.Errorf("the server did not create an empty twin; update the server")
	}
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
