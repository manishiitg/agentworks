package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Left menu groups (owner, 10 Nov, approved mockup "Menu Filters Groups"): a group is a name the person gives to some
// of their projects, to show only those with the Projects page filter. Groups belong to the person, are never shared, and hold
// project keys (connection, kind and id, as pins do), so one group can hold projects from several networks.

const (
	menuGroupsMax        = 50
	menuGroupProjectsMax = 500
	menuGroupNameMax     = 40
)

type menuGroup struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
}

type menuGroups struct {
	Groups []menuGroup `json:"groups"`
	// The menu's last choices, kept with the groups so they survive a restart and match on every screen (owner,
	// 10 Nov): Filter is all, mine, shared or group:<id>; Network is a connection id or all.
	Filter  string `json:"filter,omitempty"`
	Network string `json:"network,omitempty"`
}

// normalized trims names, drops empty or repeated names and projects, gives new groups an id and keeps the limits.
func (m menuGroups) normalized() menuGroups {
	out := menuGroups{Groups: []menuGroup{}, Filter: strings.TrimSpace(m.Filter), Network: strings.TrimSpace(m.Network)}
	if len(out.Filter) > 80 {
		out.Filter = ""
	}
	if len(out.Network) > 80 {
		out.Network = ""
	}
	names := map[string]bool{}
	for _, g := range m.Groups {
		name := menuGroupName(g.Name)
		key := strings.ToLower(name)
		if key == "" || names[key] || len(out.Groups) == menuGroupsMax {
			continue
		}
		names[key] = true
		id := strings.TrimSpace(g.ID)
		if id == "" {
			id = uuid.NewString()
		}
		projects, seen := []string{}, map[string]bool{}
		for _, p := range g.Projects {
			if p = strings.TrimSpace(p); p != "" && len(p) <= 512 && !seen[p] && len(projects) < menuGroupProjectsMax {
				seen[p] = true
				projects = append(projects, p)
			}
		}
		out.Groups = append(out.Groups, menuGroup{ID: id, Name: name, Projects: projects})
	}
	return out
}

// menuGroupName is a group name as it is kept: single spaces, at most menuGroupNameMax characters.
func menuGroupName(raw string) string {
	name := []rune(strings.Join(strings.Fields(raw), " "))
	if len(name) > menuGroupNameMax {
		name = name[:menuGroupNameMax]
	}
	return string(name)
}

// menuGroupsError is a refusal with the HTTP status the screen gets and a plain message.
type menuGroupsError struct {
	status  int
	message string
}

func (e menuGroupsError) Error() string { return e.message }

func menuGroupsStatus(err error) int {
	var refusal menuGroupsError
	if errors.As(err, &refusal) {
		return refusal.status
	}
	return http.StatusInternalServerError
}

// updateMenuGroups is the one read, change and save of the caller's groups, used by the screen. A nil
// change only reads.
func updateMenuGroups(ctx context.Context, change func(*menuGroups) error) (menuGroups, error) {
	user := GetUserIDFromContext(ctx)
	if user == "" {
		return menuGroups{}, menuGroupsError{http.StatusUnauthorized, "Sign in to change your menu groups."}
	}
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "menu-groups.json"))
	saved := menuGroups{}
	raw, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil || (exists && json.Unmarshal([]byte(raw), &saved) != nil) {
		return menuGroups{}, menuGroupsError{http.StatusServiceUnavailable, "Could not read your groups. Try again."}
	}
	saved = saved.normalized()
	if change == nil {
		return saved, nil
	}
	if err := change(&saved); err != nil {
		return saved, err
	}
	saved = saved.normalized()
	data, _ := json.Marshal(saved)
	if err := writeRawFileToWorkspace(ctx, path, string(data)); err != nil {
		return saved, menuGroupsError{http.StatusServiceUnavailable, "Could not save your groups. Try again."}
	}
	livefeed.Default.PublishToUser(livefeed.MenuGroups, user, "")
	return saved, nil
}

func (api *StreamingAPI) handleMenuGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var change func(*menuGroups) error
	if r.Method == http.MethodPut {
		var body menuGroups
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil {
			http.Error(w, "Send the full list of groups.", http.StatusBadRequest)
			return
		}
		// The screen sends the full list; it replaces what is saved.
		change = func(saved *menuGroups) error { *saved = body; return nil }
	}
	saved, err := updateMenuGroups(r.Context(), change)
	if err != nil {
		http.Error(w, err.Error(), menuGroupsStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(saved)
}
