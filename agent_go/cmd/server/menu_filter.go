package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// The Projects page's last filter, kept on the server for the person so it survives a restart and is the same on
// every screen (owner, 10 Nov). Project groups are gone (owner, 22 Nov): pins and this filter cover them.
type menuFilter struct {
	// Filter is all, mine or shared; Network is a connection id or all.
	Filter  string `json:"filter,omitempty"`
	Network string `json:"network,omitempty"`
}

// normalized keeps only a known filter and a short network id; anything else (an old group filter) is All.
func (m menuFilter) normalized() menuFilter {
	out := menuFilter{Filter: strings.TrimSpace(m.Filter), Network: strings.TrimSpace(m.Network)}
	if out.Filter != "mine" && out.Filter != "shared" {
		out.Filter = ""
	}
	if len(out.Network) > 80 {
		out.Network = ""
	}
	return out
}

func (api *StreamingAPI) handleMenuFilter(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user := GetUserIDFromContext(r.Context())
	if user == "" {
		http.Error(w, "Sign in to keep your filter.", http.StatusUnauthorized)
		return
	}
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "menu-filter.json"))
	saved := menuFilter{}
	if r.Method == http.MethodPut {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&saved) != nil {
			http.Error(w, "Send the filter and network.", http.StatusBadRequest)
			return
		}
		saved = saved.normalized()
		data, _ := json.Marshal(saved)
		if err := writeRawFileToWorkspace(r.Context(), path, string(data)); err != nil {
			http.Error(w, "Could not save your filter. Try again.", http.StatusServiceUnavailable)
			return
		}
		livefeed.Default.PublishToUser(livefeed.MenuFilter, user, "")
	} else {
		raw, exists, err := readFileFromWorkspace(r.Context(), path)
		if err != nil || (exists && json.Unmarshal([]byte(raw), &saved) != nil) {
			http.Error(w, "Could not read your filter. Try again.", http.StatusServiceUnavailable)
			return
		}
		saved = saved.normalized()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(saved)
}
