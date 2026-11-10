package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// menuGroupsError is a refusal with the HTTP status the screen gets and a plain message for chat and MCP.
type menuGroupsError struct {
	status  int
	message string
}

func (e menuGroupsError) Error() string { return e.message }

func menuGroupsRefusal(format string, args ...any) error {
	return menuGroupsError{status: http.StatusBadRequest, message: fmt.Sprintf(format, args...)}
}

func menuGroupsStatus(err error) int {
	var refusal menuGroupsError
	if errors.As(err, &refusal) {
		return refusal.status
	}
	return http.StatusInternalServerError
}

// updateMenuGroups is the one read, change and save of the caller's groups, used by the screen, chat and MCP. A nil
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

// menuCrew is one Crew row of the caller's left menu on this server.
type menuCrew struct{ ID, Name string }

// menuCrews lists the Crews the left menu shows the caller here, from the same navigation list the menu reads.
func (api *StreamingAPI) menuCrews(ctx context.Context) []menuCrew {
	if api.agentProfiles == nil || GetUserFromContext(ctx) == nil {
		return nil
	}
	recorder := httptest.NewRecorder()
	api.listExternalWorkspaces(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces", nil).WithContext(ctx), map[string]any{}, true)
	var body struct {
		Workspaces []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			PersonalAgent bool   `json:"personal_agent"`
		} `json:"workspaces"`
	}
	if recorder.Code >= 400 || json.Unmarshal(recorder.Body.Bytes(), &body) != nil {
		return nil
	}
	crews := []menuCrew{}
	for _, row := range body.Workspaces {
		// The person's own AgentWorks agent is pinned apart and is never in a group, as on the screen.
		if !row.PersonalAgent && row.ID != "" {
			crews = append(crews, menuCrew{ID: row.ID, Name: row.Name})
		}
	}
	return crews
}

// menuProjectKey splits a saved project key, the left menu's JSON ["connection","kind","id"]
// (connectedProjectKey in the frontend).
func menuProjectKey(key string) (connection, kind, id string, ok bool) {
	var parts []string
	if json.Unmarshal([]byte(key), &parts) != nil || len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func newMenuProjectKey(connection, kind, id string) string {
	data, _ := json.Marshal([]string{connection, kind, id})
	return string(data)
}

// menuProjectName is the Crew's name when it is in the caller's menu here, else the saved key.
func menuProjectName(key string, crews []menuCrew) string {
	if _, kind, id, ok := menuProjectKey(key); ok && kind == "crew" {
		for _, crew := range crews {
			if crew.ID == id {
				return crew.Name
			}
		}
	}
	return key
}

// resolveMenuProject turns a Crew name, a Crew id or a saved key into a project key. A Crew keeps the connection a
// saved key already gives it (the Mac app names each server it connects to); otherwise it is this app's own ("local").
func resolveMenuProject(raw string, crews []menuCrew, saved menuGroups) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", menuGroupsRefusal("Say which Crew: its name as the left menu shows it.")
	}
	if _, _, _, ok := menuProjectKey(raw); ok {
		return raw, nil
	}
	var matches []menuCrew
	for _, crew := range crews {
		if crew.ID == raw {
			matches = []menuCrew{crew}
			break
		}
		if strings.EqualFold(strings.TrimSpace(crew.Name), raw) {
			matches = append(matches, crew)
		}
	}
	if len(matches) == 0 {
		for _, crew := range crews {
			if strings.Contains(strings.ToLower(crew.Name), strings.ToLower(raw)) {
				matches = append(matches, crew)
			}
		}
	}
	if len(matches) == 0 {
		return "", menuGroupsRefusal("No Crew called %q is in your left menu here. Check the name as the menu shows it.", raw)
	}
	if len(matches) > 1 {
		names := make([]string, len(matches))
		for i, crew := range matches {
			names[i] = crew.Name
		}
		return "", menuGroupsRefusal("More than one Crew matches %q: %s. Say the full name.", raw, strings.Join(names, ", "))
	}
	connection := "local"
	for _, group := range saved.Groups {
		for _, key := range group.Projects {
			if c, kind, id, ok := menuProjectKey(key); ok && kind == "crew" && id == matches[0].ID {
				connection = c
			}
		}
	}
	return newMenuProjectKey(connection, "crew", matches[0].ID), nil
}

// sameMenuProject compares two keys by kind and id, so a project matches whichever connection named it.
func sameMenuProject(a, b string) bool {
	if a == b {
		return true
	}
	_, kindA, idA, okA := menuProjectKey(a)
	_, kindB, idB, okB := menuProjectKey(b)
	return okA && okB && kindA == kindB && idA == idB
}

func findMenuGroup(saved *menuGroups, raw string) (*menuGroup, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, menuGroupsRefusal("Say which group.")
	}
	for i := range saved.Groups {
		if saved.Groups[i].ID == raw || strings.EqualFold(saved.Groups[i].Name, menuGroupName(raw)) {
			return &saved.Groups[i], nil
		}
	}
	if len(saved.Groups) == 0 {
		return nil, menuGroupsRefusal("You have no groups yet. Create one first.")
	}
	names := make([]string, len(saved.Groups))
	for i, group := range saved.Groups {
		names[i] = group.Name
	}
	return nil, menuGroupsRefusal("No group called %q. Your groups: %s.", raw, strings.Join(names, ", "))
}

func menuGroupNameTaken(saved *menuGroups, name string, except *menuGroup) bool {
	for i := range saved.Groups {
		if &saved.Groups[i] != except && strings.EqualFold(saved.Groups[i].Name, name) {
			return true
		}
	}
	return false
}

type menuGroupView struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
}

type menuGroupsReply struct {
	Message string          `json:"message"`
	Groups  []menuGroupView `json:"groups"`
}

// menuGroupsAction runs a chat or MCP menu_groups action for the signed-in caller, through updateMenuGroups.
func (api *StreamingAPI) menuGroupsAction(ctx context.Context, args map[string]any) (menuGroupsReply, error) {
	action := strings.TrimSpace(externalArg(args, "action"))
	group, name := externalArg(args, "group"), externalArg(args, "name")
	projects := []string{}
	if project := externalArg(args, "project"); project != "" {
		projects = append(projects, project)
	}
	if list, ok := args["projects"].([]any); ok {
		for _, item := range list {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				projects = append(projects, text)
			}
		}
	}
	var crews []menuCrew
	crewsRead := false
	listCrews := func() []menuCrew {
		if !crewsRead {
			crews, crewsRead = api.menuCrews(ctx), true
		}
		return crews
	}
	message := ""
	var change func(*menuGroups) error
	switch action {
	case "list":
	case "create":
		change = func(saved *menuGroups) error {
			clean := menuGroupName(name)
			if clean == "" {
				return menuGroupsRefusal("Give the new group a name.")
			}
			if menuGroupNameTaken(saved, clean, nil) {
				return menuGroupsRefusal("You already have a group called %q. Choose another name.", clean)
			}
			if len(saved.Groups) >= menuGroupsMax {
				return menuGroupsRefusal("You have the most groups allowed (%d). Delete one first.", menuGroupsMax)
			}
			keys := []string{}
			for _, item := range projects {
				key, err := resolveMenuProject(item, listCrews(), *saved)
				if err != nil {
					return err
				}
				keys = append(keys, key)
			}
			saved.Groups = append(saved.Groups, menuGroup{Name: clean, Projects: keys})
			message = fmt.Sprintf("Created the group %q with %d Crews. Choose it in the left menu to show only those.", clean, len(keys))
			return nil
		}
	case "rename":
		change = func(saved *menuGroups) error {
			target, err := findMenuGroup(saved, group)
			if err != nil {
				return err
			}
			clean := menuGroupName(name)
			if clean == "" {
				return menuGroupsRefusal("Give the new name for the group.")
			}
			if menuGroupNameTaken(saved, clean, target) {
				return menuGroupsRefusal("You already have a group called %q. Choose another name.", clean)
			}
			message = fmt.Sprintf("Renamed the group %q to %q.", target.Name, clean)
			target.Name = clean
			return nil
		}
	case "delete":
		change = func(saved *menuGroups) error {
			target, err := findMenuGroup(saved, group)
			if err != nil {
				return err
			}
			message = fmt.Sprintf("Deleted the group %q. Its Crews stay in your left menu.", target.Name)
			id := target.ID
			kept := []menuGroup{}
			for _, g := range saved.Groups {
				if g.ID != id {
					kept = append(kept, g)
				}
			}
			saved.Groups = kept
			return nil
		}
	case "add", "remove":
		change = func(saved *menuGroups) error {
			target, err := findMenuGroup(saved, group)
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				return menuGroupsRefusal("Say which Crew: its name as the left menu shows it.")
			}
			done := []string{}
			for _, item := range projects {
				key, err := resolveMenuProject(item, listCrews(), *saved)
				if err != nil {
					return err
				}
				label := menuProjectName(key, listCrews())
				at := -1
				for i, existing := range target.Projects {
					if sameMenuProject(existing, key) {
						at = i
						break
					}
				}
				switch {
				case action == "add" && at >= 0:
					done = append(done, fmt.Sprintf("%q is already in %q.", label, target.Name))
				case action == "add" && len(target.Projects) >= menuGroupProjectsMax:
					return menuGroupsRefusal("The group %q is full (%d Crews). Remove one first.", target.Name, menuGroupProjectsMax)
				case action == "add":
					target.Projects = append(target.Projects, key)
					done = append(done, fmt.Sprintf("Added %q to %q.", label, target.Name))
				case at < 0:
					return menuGroupsRefusal("%q is not in the group %q.", label, target.Name)
				default:
					target.Projects = append(target.Projects[:at:at], target.Projects[at+1:]...)
					done = append(done, fmt.Sprintf("Removed %q from %q.", label, target.Name))
				}
			}
			message = strings.Join(done, " ")
			return nil
		}
	default:
		return menuGroupsReply{}, menuGroupsRefusal("Choose list, create, rename, delete, add or remove.")
	}
	saved, err := updateMenuGroups(ctx, change)
	if err != nil {
		return menuGroupsReply{}, err
	}
	reply := menuGroupsReply{Message: message, Groups: []menuGroupView{}}
	for _, g := range saved.Groups {
		view := menuGroupView{ID: g.ID, Name: g.Name, Projects: []string{}}
		for _, key := range g.Projects {
			view.Projects = append(view.Projects, menuProjectName(key, listCrews()))
		}
		reply.Groups = append(reply.Groups, view)
	}
	if action == "list" {
		reply.Message = fmt.Sprintf("You have %d groups in your left menu.", len(saved.Groups))
		if len(saved.Groups) == 0 {
			reply.Message = "You have no groups yet. Create one with a name and the Crews to show in it."
		}
	}
	return reply, nil
}

// externalMenuGroupsCall answers menu_groups over MCP with the same action code as chat.
func (api *StreamingAPI) externalMenuGroupsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	reply, err := api.menuGroupsAction(r.Context(), args)
	if err != nil {
		externalError(w, menuGroupsStatus(err), "invalid_arguments", err.Error())
		return
	}
	externalJSON(w, reply)
}

// menuGroupsToolDefinition is the one chat and MCP schema; Builder chat panels reuse it through externalTools.
func menuGroupsToolDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("menu_groups", "Your own left menu groups: a group is a name you give to some of your Crews, to show only those in the left menu. list shows your groups and their Crews; create (name, optional projects), rename (group, name), delete (group), add or remove (group, project). Name a group by its name and a Crew by its name as the left menu shows it. Changes only your menu; never changes a Crew or who can use it.", true, false, map[string]any{
		"action":   map[string]any{"type": "string", "enum": []any{"list", "create", "rename", "delete", "add", "remove"}},
		"group":    externalString("The group's name (or its id from list)."),
		"name":     externalString("The new group's name, or the new name when renaming."),
		"project":  externalString("A Crew's name as the left menu shows it."),
		"projects": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Names of the Crews to put in the new group."},
	}, "action")
}
