package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"regexp"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

type contextShortcuts struct {
	Version       int      `json:"version"`
	Revision      uint64   `json:"revision"`
	Shortcuts     []string `json:"shortcuts"` // null restores product defaults; [] intentionally shows none.
	InheritedFrom string   `json:"inherited_from,omitempty"`
}

var contextShortcutsLocks dashboardQuestionLockSet
var contextShortcutID = regexp.MustCompile(`^[a-z][a-z0-9:-]{0,79}$`)

func contextShortcutProduct(product string) bool {
	return product == "agentworks" || product == "relays" || product == "work" || product == "code"
}

func contextShortcutsPath(user, product string) string {
	return filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "context-shortcuts", product+".json"))
}

func validContextShortcuts(value contextShortcuts) bool {
	if value.Version != 1 || len(value.Shortcuts) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range value.Shortcuts {
		if !contextShortcutID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func readContextShortcuts(r *http.Request, product string) (contextShortcuts, bool, error) {
	value := contextShortcuts{Version: 1}
	raw, exists, err := readFileFromWorkspace(r.Context(), contextShortcutsPath(GetUserIDFromContext(r.Context()), product))
	if err != nil || !exists {
		return value, exists, err
	}
	if len(raw) > 8192 || json.Unmarshal([]byte(raw), &value) != nil || !validContextShortcuts(value) {
		log.Printf("[CONTEXT_SHORTCUTS] user=%q product=%q invalid saved preferences; using defaults", GetUserIDFromContext(r.Context()), product)
		return contextShortcuts{Version: 1}, true, nil
	}
	value.InheritedFrom = ""
	return value, true, nil
}

// These are presentation preferences, never panel grants. Each workspace still
// derives its available destinations from its own mode and resource permissions.
func (api *StreamingAPI) handleContextShortcuts(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	product := mux.Vars(r)["product"]
	if !contextShortcutProduct(product) {
		http.Error(w, "Unknown shortcut product.", http.StatusBadRequest)
		return
	}
	if !homeProductAllowed(claims, product) {
		http.Error(w, "This product is unavailable.", http.StatusForbidden)
		return
	}
	unlock := contextShortcutsLocks.Lock(claims.UserID + ":" + product)
	defer unlock()
	value, exists, err := readContextShortcuts(r, product)
	if err != nil {
		http.Error(w, "Could not load your shortcuts.", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodPut {
		var incoming struct {
			Version   int      `json:"version"`
			Revision  uint64   `json:"revision"`
			Shortcuts []string `json:"shortcuts"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&incoming); err != nil {
			http.Error(w, "Invalid shortcuts.", http.StatusBadRequest)
			return
		}
		next := contextShortcuts{Version: incoming.Version, Revision: incoming.Revision, Shortcuts: incoming.Shortcuts}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || !validContextShortcuts(next) {
			http.Error(w, "Invalid shortcuts.", http.StatusBadRequest)
			return
		}
		if next.Revision != value.Revision {
			http.Error(w, "Your shortcuts changed in another tab. Reload before saving.", http.StatusConflict)
			return
		}
		next.Revision++
		raw, err := json.Marshal(next)
		if err != nil || writeRawFileToWorkspace(r.Context(), contextShortcutsPath(claims.UserID, product), string(raw)) != nil {
			http.Error(w, "Could not save your shortcuts.", http.StatusServiceUnavailable)
			return
		}
		value = next
	} else if r.Method == http.MethodGet {
		// A future consolidated project product can explicitly inherit this user's
		// legacy origin. Never infer a merge from a product being disabled, a role,
		// or a display name. A canonical preference always wins; old data is kept.
		legacy := r.URL.Query().Get("legacy")
		if legacy != "" && !((product == "code" && legacy == "work") || (product == "work" && legacy == "code")) {
			http.Error(w, "Invalid legacy shortcut product.", http.StatusBadRequest)
			return
		}
		if !exists && legacy != "" {
			old, found, err := readContextShortcuts(r, legacy)
			if err != nil {
				http.Error(w, "Could not load your legacy shortcuts.", http.StatusServiceUnavailable)
				return
			}
			if found {
				value.Shortcuts, value.InheritedFrom = old.Shortcuts, legacy
			}
		}
	} else {
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
