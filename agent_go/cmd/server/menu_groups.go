package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Left menu groups (owner, 10 Nov, approved mockup "Menu Filters Groups"): a group is a name the person gives to some
// of their projects, to show only those in the left menu. Groups belong to the person, are never shared, and hold
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
}

// normalized trims names, drops empty or repeated names and projects, gives new groups an id and keeps the limits.
func (m menuGroups) normalized() menuGroups {
	out := menuGroups{Groups: []menuGroup{}}
	names := map[string]bool{}
	for _, g := range m.Groups {
		name := []rune(strings.Join(strings.Fields(g.Name), " "))
		if len(name) > menuGroupNameMax {
			name = name[:menuGroupNameMax]
		}
		key := strings.ToLower(string(name))
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
		out.Groups = append(out.Groups, menuGroup{ID: id, Name: string(name), Projects: projects})
	}
	return out
}

func (api *StreamingAPI) handleMenuGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user := GetUserIDFromContext(r.Context())
	if user == "" {
		http.Error(w, "Sign in to change your menu groups.", http.StatusUnauthorized)
		return
	}
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "menu-groups.json"))
	saved := menuGroups{}
	if r.Method == http.MethodPut {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&saved) != nil {
			http.Error(w, "Send the full list of groups.", http.StatusBadRequest)
			return
		}
		saved = saved.normalized()
		data, _ := json.Marshal(saved)
		if err := writeRawFileToWorkspace(r.Context(), path, string(data)); err != nil {
			http.Error(w, "Could not save your groups. Try again.", http.StatusServiceUnavailable)
			return
		}
	} else {
		raw, exists, err := readFileFromWorkspace(r.Context(), path)
		if err != nil || (exists && json.Unmarshal([]byte(raw), &saved) != nil) {
			http.Error(w, "Could not read your groups. Try again.", http.StatusServiceUnavailable)
			return
		}
		saved = saved.normalized()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(saved)
}
