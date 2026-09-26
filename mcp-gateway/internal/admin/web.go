package admin

import (
	"context"
	"html/template"
	"net/http"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

// UIRoutes mounts the server-rendered admin UI.
func (a *Admin) UIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/login", a.uiLogin)
	mux.HandleFunc("/admin/", a.requireUI(a.uiDashboard))
	mux.HandleFunc("/admin/users", a.requireUI(a.uiUsers))
	mux.HandleFunc("/admin/users/add", a.requireUI(a.uiUsersAdd))
	mux.HandleFunc("/admin/groups", a.requireUI(a.uiGroups))
	mux.HandleFunc("/admin/groups/add", a.requireUI(a.uiGroupsAdd))
	mux.HandleFunc("/admin/groups/members", a.requireUI(a.uiGroupMembers))
	mux.HandleFunc("/admin/connectors", a.requireUI(a.uiConnectors))
	mux.HandleFunc("/admin/connectors/add", a.requireUI(a.uiConnectorsAdd))
	mux.HandleFunc("/admin/connectors/sync", a.requireUI(a.uiConnectorsSync))
	mux.HandleFunc("/admin/connectors/delete", a.requireUI(a.uiConnectorsDelete))
	mux.HandleFunc("/admin/tools", a.requireUI(a.uiTools))
	mux.HandleFunc("/admin/grants/set", a.requireUI(a.uiGrantsSet))
	mux.HandleFunc("/admin/audit", a.requireUI(a.uiAudit))
}

func (a *Admin) requireUI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (a *Admin) uiLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil && r.PostForm.Get("token") == a.HumanToken && a.HumanToken != "" {
			http.SetCookie(w, &http.Cookie{Name: "gw_admin", Value: a.HumanToken, Path: "/", HttpOnly: true})
			http.Redirect(w, r, "/admin/", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/admin/login?err=1", http.StatusSeeOther)
		return
	}
	render(w, "login", map[string]any{"Err": r.URL.Query().Get("err") != ""})
}

func back(w http.ResponseWriter, r *http.Request, path string, err error) {
	if err != nil {
		http.Redirect(w, r, path+"?err="+template.URLQueryEscaper(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (a *Admin) uiDashboard(w http.ResponseWriter, r *http.Request) {
	tools := a.Store.ListTools(a.WorkspaceID)
	grantCount := 0
	for _, t := range tools {
		u, g := a.Store.ToolHolders(t.PublicName)
		grantCount += len(u) + len(g)
	}
	render(w, "dashboard", map[string]any{
		"Users":      len(a.Store.ListUsers(a.WorkspaceID)),
		"Groups":     len(a.Store.ListGroups(a.WorkspaceID)),
		"Connectors": len(a.Store.ListConnectors(a.WorkspaceID)),
		"Tools":      len(tools),
		"Grants":     grantCount,
		"Audit":      len(a.Store.ListAudit()),
		"MCPURL":     a.PublicURL + "/mcp",
	})
}

func (a *Admin) uiUsers(w http.ResponseWriter, r *http.Request) {
	type row struct {
		store.User
		Groups []string
		Grants []string
	}
	var rows []row
	for _, u := range a.Store.ListUsers(a.WorkspaceID) {
		rows = append(rows, row{User: u, Groups: a.Store.GroupsOf(u.ID), Grants: a.Store.UserGrantsFor(u.ID)})
	}
	render(w, "users", map[string]any{"Rows": rows, "Err": r.URL.Query().Get("err")})
}

func (a *Admin) uiUsersAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	back(w, r, "/admin/users", a.CreateUser(r.PostForm.Get("id"), r.PostForm.Get("email")))
}

func (a *Admin) uiGroups(w http.ResponseWriter, r *http.Request) {
	type row struct {
		store.Group
		Members []string
		Grants  []string
	}
	var rows []row
	for _, g := range a.Store.ListGroups(a.WorkspaceID) {
		rows = append(rows, row{Group: g, Members: a.Store.MembersOf(g.ID), Grants: a.Store.GroupGrantsFor(g.ID)})
	}
	render(w, "groups", map[string]any{
		"Rows": rows, "Users": a.Store.ListUsers(a.WorkspaceID),
		"Tools": a.Store.ListTools(a.WorkspaceID), "Err": r.URL.Query().Get("err"),
	})
}

func (a *Admin) uiGroupsAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	back(w, r, "/admin/groups", a.CreateGroup(r.PostForm.Get("id"), r.PostForm.Get("name")))
}

func (a *Admin) uiGroupMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	back(w, r, "/admin/groups", a.SetMember(r.PostForm.Get("group"), r.PostForm.Get("user"), r.PostForm.Get("action") != "remove"))
}

func (a *Admin) uiConnectors(w http.ResponseWriter, r *http.Request) {
	type row struct {
		store.Connector
		ToolCount int
	}
	var rows []row
	for _, c := range a.Store.ListConnectors(a.WorkspaceID) {
		rows = append(rows, row{Connector: c, ToolCount: len(a.Store.ListToolsForConnector(c.ID))})
	}
	render(w, "connectors", map[string]any{
		"Rows": rows, "Providers": a.Catalog.Providers, "Err": r.URL.Query().Get("err"),
	})
}

func (a *Admin) uiConnectorsAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	provider, label, slug, url := r.PostForm.Get("provider"), r.PostForm.Get("label"), r.PostForm.Get("slug"), r.PostForm.Get("url")
	var err error
	if r.PostForm.Get("mode") == "custom" {
		_, err = a.AddConnectorCustom(ctx, provider, label, slug, url)
	} else {
		_, err = a.AddConnectorFromCatalog(ctx, provider, label, slug)
	}
	back(w, r, "/admin/connectors", err)
}

func (a *Admin) uiConnectorsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	back(w, r, "/admin/connectors", a.SyncConnector(r.Context(), r.PostForm.Get("id")))
}

func (a *Admin) uiConnectorsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	back(w, r, "/admin/connectors", a.DeleteConnector(r.PostForm.Get("id")))
}

func (a *Admin) uiTools(w http.ResponseWriter, r *http.Request) {
	type row struct {
		store.ToolSnapshot
		Users, Groups []string
		ConnLabel     string
	}
	var rows []row
	for _, t := range a.Store.ListTools(a.WorkspaceID) {
		users, groups := a.Store.ToolHolders(t.PublicName)
		label := ""
		if c, ok := a.Store.GetConnector(t.ConnectorID); ok {
			label = c.Label
		}
		rows = append(rows, row{ToolSnapshot: t, Users: users, Groups: groups, ConnLabel: label})
	}
	render(w, "tools", map[string]any{
		"Rows": rows, "Users": a.Store.ListUsers(a.WorkspaceID),
		"Groups": a.Store.ListGroups(a.WorkspaceID), "Err": r.URL.Query().Get("err"),
	})
}

func (a *Admin) uiGrantsSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	tool, grant := r.PostForm.Get("tool"), r.PostForm.Get("action") != "revoke"
	var err error
	if u := r.PostForm.Get("user"); u != "" {
		err = a.SetUserGrant(u, tool, grant)
	} else if g := r.PostForm.Get("group"); g != "" {
		err = a.SetGroupGrant(g, tool, grant)
	} else {
		err = errNoSubject
	}
	back(w, r, "/admin/tools", err)
}

func (a *Admin) uiAudit(w http.ResponseWriter, r *http.Request) {
	events := a.Store.ListAudit()
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	if len(events) > 200 {
		events = events[:200]
	}
	render(w, "audit", map[string]any{"Rows": events})
}
