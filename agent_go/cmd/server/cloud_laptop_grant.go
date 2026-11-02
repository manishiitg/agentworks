package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// While a Mac Crew is in the Cloud, its server copy may use this Crew's own
// folder on the Mac, with terminal commands (owner request, PLAT-856-501).
// The grant goes through the server's existing owner-only laptop settings and
// their device check. Only a grant Cloud made is ever removed; a laptop grant
// the person set by hand is left as it is. Never a parent folder or $HOME.
type cloudLaptopGrant struct {
	PeerProjectID string `json:"peer_project_id"`
	DeviceID      string `json:"device_id"`
	Path          string `json:"path"`
}

// cloudLaptopState is what the Cloud page says about laptop access.
type cloudLaptopState struct {
	State   string `json:"state"` // on, existing, needs_app, off
	Message string `json:"message,omitempty"`
}

func cloudLaptopPath(id string) (string, error) {
	p, err := crewSyncManifestPath(id)
	return p + ".cloud-laptop", err
}

func readCloudLaptop(id string) (*cloudLaptopGrant, cloudLaptopState) {
	p, err := cloudLaptopPath(id)
	if err != nil {
		return nil, cloudLaptopState{State: "off"}
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, cloudLaptopState{State: "off"}
	}
	var saved struct {
		Grant *cloudLaptopGrant `json:"grant,omitempty"`
		cloudLaptopState
	}
	if json.Unmarshal(raw, &saved) != nil {
		return nil, cloudLaptopState{State: "off"}
	}
	return saved.Grant, saved.cloudLaptopState
}

func writeCloudLaptop(id string, grant *cloudLaptopGrant, state cloudLaptopState) {
	p, err := cloudLaptopPath(id)
	if err != nil {
		return
	}
	raw, _ := json.Marshal(struct {
		Grant *cloudLaptopGrant `json:"grant,omitempty"`
		cloudLaptopState
	}{grant, state})
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, raw, 0o600)
}

// thisMacDeviceID is the computer id the CLI uses when it connects this Mac.
func thisMacDeviceID() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(dir, "agentworks", "computer-id"))
	if id := strings.TrimSpace(string(raw)); err == nil && localfiles.ValidID(id) {
		return id
	}
	return ""
}

type serverLaptopSettings struct {
	DeviceID string                       `json:"device_id"`
	Folders  []localfiles.FolderSelection `json:"folders"`
	Commands bool                         `json:"commands"`
}

func cloudLaptopCall(ctx context.Context, conn desktopConnection, peer, method string, body any) (serverLaptopSettings, int, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return serverLaptopSettings{}, 0, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(conn.Server, "/")+"/api/agent-profiles/work/projects/"+url.PathEscape(peer)+"/laptop", payload)
	if err != nil {
		return serverLaptopSettings{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+conn.Token)
	res, err := desktopHTTPClient().Do(req)
	if err != nil {
		return serverLaptopSettings{}, 0, err
	}
	defer res.Body.Close()
	var out serverLaptopSettings
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var failure map[string]any
		_ = json.Unmarshal(raw, &failure)
		return out, res.StatusCode, errors.New(externalErrorMessage(failure, res.Status))
	}
	err = json.Unmarshal(raw, &out)
	return out, res.StatusCode, err
}

// syncCloudLaptop brings the server copy's laptop grant in line with Cloud on
// or off, after a Cloud request on this Mac has finished.
func (api *StreamingAPI) syncCloudLaptop(userID string, roots crewroot.Roots) {
	if !IsLocalMode() || roots.ID == "" || roots.Owner != userID {
		return
	}
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil || binding == nil || binding.Side != "local" || binding.ConnectionID == "" || binding.PeerProjectID == "" {
		return
	}
	conn, err := desktopConnectionFor(userID, binding.ConnectionID)
	if err != nil || strings.TrimRight(conn.Server, "/") != strings.TrimRight(binding.PeerServer, "/") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	peer := binding.PeerProjectID
	grant, _ := readCloudLaptop(roots.ID)
	on := binding.Mode != "off"
	logResult := func(action string, status int, err error) {
		log.Printf("[CLOUD_LAPTOP] crew=%s peer=%s action=%s -> %d err=%v", roots.ID, peer, action, status, err)
	}
	current, status, err := cloudLaptopCall(ctx, conn, peer, http.MethodGet, nil)
	if err != nil {
		logResult("read", status, err)
		return
	}
	if !on {
		// Off removes only the grant Cloud made, and only while it is still the one in place.
		if grant != nil && current.DeviceID == grant.DeviceID {
			_, status, err = cloudLaptopCall(ctx, conn, peer, http.MethodPut, serverLaptopSettings{Folders: []localfiles.FolderSelection{}})
			logResult("remove", status, err)
			if err != nil {
				return
			}
		}
		writeCloudLaptop(roots.ID, nil, cloudLaptopState{State: "off"})
		return
	}
	if current.DeviceID != "" && (grant == nil || current.DeviceID != grant.DeviceID) {
		writeCloudLaptop(roots.ID, nil, cloudLaptopState{State: "existing", Message: "Uses your existing laptop access."})
		logResult("keep_existing", status, nil)
		return
	}
	device := thisMacDeviceID()
	if device == "" {
		writeCloudLaptop(roots.ID, nil, cloudLaptopState{State: "needs_app", Message: "Laptop access needs AgentWorks running on this Mac."})
		logResult("no_device", 0, nil)
		return
	}
	// Exactly this Crew's own folder, never a parent or the home folder.
	folder := filepath.Clean(roots.Files)
	if home, _ := os.UserHomeDir(); folder == "" || folder == "/" || folder == filepath.Clean(home) {
		logResult("refused_folder", 0, fmt.Errorf("unsafe folder"))
		return
	}
	if grant != nil && current.DeviceID == device && current.Commands && len(current.Folders) == 1 && current.Folders[0].Path == folder {
		return
	}
	want := serverLaptopSettings{DeviceID: device, Folders: []localfiles.FolderSelection{{Path: folder, Access: "write"}}, Commands: true}
	_, status, err = cloudLaptopCall(ctx, conn, peer, http.MethodPut, want)
	logResult("grant", status, err)
	if err != nil {
		writeCloudLaptop(roots.ID, nil, cloudLaptopState{State: "needs_app", Message: "Laptop access needs AgentWorks running on this Mac."})
		return
	}
	writeCloudLaptop(roots.ID, &cloudLaptopGrant{PeerProjectID: peer, DeviceID: device, Path: folder}, cloudLaptopState{State: "on"})
}

// handleCloudLaptopStatus answers the Cloud page: on, existing, needs_app or off.
func (api *StreamingAPI) handleCloudLaptopStatus(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 403, "unavailable", err.Error())
		return
	}
	_, state := readCloudLaptop(roots.ID)
	externalJSON(w, state)
}
