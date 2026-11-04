package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// On a person's own Mac, agents may use the Mac's Downloads folder by default.
// The person can remove it; then every agent sandbox on this Mac blocks it.
func macDownloadsAccessPath() (string, error) {
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "mac-downloads-access.json"), err
}

func loadMacDownloadsAccess() {
	p, err := macDownloadsAccessPath()
	if err != nil {
		return
	}
	var saved struct {
		Removed bool `json:"removed"`
	}
	if raw, err := os.ReadFile(p); err == nil && json.Unmarshal(raw, &saved) == nil {
		common.HostDownloadsRemoved.Store(saved.Removed)
	}
}

func macDownloadsFolder() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

func (api *StreamingAPI) handleMacDownloadsAccess(w http.ResponseWriter, r *http.Request) {
	if !cliPersonalMac() {
		externalJSON(w, map[string]any{"available": false})
		return
	}
	if r.Method == http.MethodPut {
		claims := GetUserFromContext(r.Context())
		if claims == nil || !userAccessForClaims(claims).CanEdit {
			externalError(w, 403, "forbidden", "Only the person using this Mac can change this.")
			return
		}
		var input struct {
			Removed bool `json:"removed"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			externalError(w, 400, "invalid_request", "Say whether Downloads is removed.")
			return
		}
		p, err := macDownloadsAccessPath()
		if err == nil {
			raw, _ := json.Marshal(input)
			_ = os.MkdirAll(filepath.Dir(p), 0o700)
			err = os.WriteFile(p, raw, 0o600)
		}
		if err != nil {
			externalError(w, 500, "save_failed", "Could not save this setting.")
			return
		}
		common.HostDownloadsRemoved.Store(input.Removed)
	}
	// home: the person's home folder, which the Folders screen offers as "Your whole Mac" (owner, 4 Nov). Attaching it is
	// an ordinary folder grant through /api/work/folders; nothing here grants access.
	home, _ := os.UserHomeDir()
	externalJSON(w, map[string]any{"available": true, "removed": common.HostDownloadsRemoved.Load(), "path": macDownloadsFolder(), "home": home})
}
