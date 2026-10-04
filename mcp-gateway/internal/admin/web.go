package admin

import (
	"context"
	"crypto/subtle"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

// UIRoutes mounts the server-rendered admin UI.
func (a *Admin) UIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/login", a.uiLogin)
	mux.HandleFunc("/admin/logout", a.requireUI(a.uiLogout))
	mux.HandleFunc("/admin/{$}", a.requireUI(a.uiDashboard))
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
	mux.HandleFunc("/admin/tools/approve", a.requireUI(a.uiToolsApprove))
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
		if err := r.ParseForm(); err == nil && a.HumanToken != "" && subtle.ConstantTimeCompare([]byte(r.PostForm.Get("token")), []byte(a.HumanToken)) == 1 {
			session, err := a.issueSession()
			if err != nil {
				http.Error(w, "could not create admin session", http.StatusInternalServerError)
				return
			}
			public, _ := url.Parse(a.PublicURL)
			http.SetCookie(w, &http.Cookie{Name: "gw_admin", Value: session, Path: "/", HttpOnly: true, Secure: r.TLS != nil || public != nil && public.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: int(adminSessionLifetime.Seconds())})
			http.Redirect(w, r, "/admin/", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/admin/login?err=1", http.StatusSeeOther)
		return
	}
	render(w, "login", map[string]any{"Err": r.URL.Query().Get("err") != ""})
}

func (a *Admin) uiLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie("gw_admin"); err == nil {
		a.revokeSession(cookie.Value)
	}
	public, _ := url.Parse(a.PublicURL)
	http.SetCookie(w, &http.Cookie{Name: "gw_admin", Path: "/", HttpOnly: true, Secure: r.TLS != nil || public != nil && public.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
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
		"Audit":      auditCount(a.Store, a.WorkspaceID),
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
	back(w, r, "/admin/groups", a.CreateGroupWithDescription(r.PostForm.Get("id"), r.PostForm.Get("name"), r.PostForm.Get("description")))
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
		Previous      []store.ToolSnapshot
	}
	var rows []row
	for _, t := range a.Store.ListTools(a.WorkspaceID) {
		users, groups := a.Store.ToolHolders(t.PublicName)
		label := ""
		if c, ok := a.Store.GetConnector(t.ConnectorID); ok {
			label = c.Label
		}
		rows = append(rows, row{ToolSnapshot: t, Users: users, Groups: groups, ConnLabel: label, Previous: a.Store.ListToolVersions(a.WorkspaceID, t.PublicName)})
	}
	render(w, "tools", map[string]any{
		"Rows": rows, "Users": a.Store.ListUsers(a.WorkspaceID),
		"Groups": a.Store.ListGroups(a.WorkspaceID), "Err": r.URL.Query().Get("err"),
	})
}

func (a *Admin) uiToolsApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		back(w, r, "/admin/tools", err)
		return
	}
	version, err := strconv.Atoi(r.PostForm.Get("version"))
	if err == nil {
		_, err = a.ApproveTool(r.PostForm.Get("name"), r.PostForm.Get("fingerprint"), version)
	}
	back(w, r, "/admin/tools", err)
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
	q := r.URL.Query()
	filter := store.AuditFilter{
		WorkspaceID: a.WorkspaceID, UserID: q.Get("user"), GroupID: q.Get("group"),
		ClientID:    q.Get("client"),
		ConnectorID: q.Get("connector"), PublicName: q.Get("tool"),
		Decision: q.Get("decision"), Outcome: q.Get("outcome"), Limit: 200,
	}
	export := r.URL.Query()
	export.Del("format")
	if date := q.Get("after"); date != "" {
		if parsed, err := time.Parse("2006-01-02", date); err == nil {
			filter.After = parsed
			export.Set("after", parsed.Format(time.RFC3339))
		}
	}
	if date := q.Get("before"); date != "" {
		if parsed, err := time.Parse("2006-01-02", date); err == nil {
			filter.Before = parsed.Add(24*time.Hour - time.Nanosecond)
			export.Set("before", filter.Before.Format(time.RFC3339Nano))
		}
	}
	export.Set("format", "csv")
	csvURL := "/api/admin/audit?" + export.Encode()
	export.Set("format", "json")
	jsonURL := "/api/admin/audit?" + export.Encode()
	rows, err := a.Store.ReadAudit(filter)
	if err != nil {
		http.Error(w, "audit storage unavailable", 503)
		return
	}
	usage, err := a.Store.ReadAuditSummary(filter)
	if err != nil {
		http.Error(w, "audit storage unavailable", 503)
		return
	}
	render(w, "audit", map[string]any{"Rows": rows, "Usage": usage, "Filter": q, "CSVURL": csvURL, "JSONURL": jsonURL})
}

func auditCount(s *store.MemoryStore, workspace string) int {
	summary, err := s.ReadAuditSummary(store.AuditFilter{WorkspaceID: workspace})
	if err != nil {
		return 0
	}
	return summary.Total
}
