// Package admin serves workspace management: users, groups, connectors,
// tool grants, and audit history, as a JSON API (/api/admin) and a
// server-rendered UI (/admin). Single workspace in M0.
package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

// Admin wires the store, gateway runtime, and catalog to HTTP.
type Admin struct {
	Store       *store.MemoryStore
	Gateway     *mcpserver.Gateway
	Catalog     *catalog.Catalog
	WorkspaceID string
	HumanToken  string
	PublicURL   string
}

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func newID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

func (a *Admin) authed(r *http.Request) bool {
	// Local runs have no separate admin identity: a request arriving
	// directly on a loopback interface is the local user, who is admin.
	// Only the direct peer address counts (never X-Forwarded-For), so a
	// gateway behind a proxy still fails closed to the token.
	if isLoopbackPeer(r) {
		return true
	}
	if a.HumanToken == "" {
		return false
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ") == a.HumanToken
	}
	c, err := r.Cookie("gw_admin")
	return err == nil && c.Value == a.HumanToken
}

// isLoopbackPeer reports whether the request arrived directly from a
// loopback address.
func isLoopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackOrigin reports whether an Origin header value names loopback
// http(s), e.g. the local AgentWorks dev server or desktop shell.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LocalhostCORS lets the local AgentWorks UI (a different loopback origin)
// call the admin API from the browser. Only loopback origins get headers;
// anything else passes through untouched.
func LocalhostCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !isLoopbackOrigin(origin) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- service (shared by JSON API and UI) ---

func (a *Admin) CreateUser(id, email string) error {
	if !validID.MatchString(id) {
		return errors.New("invalid user id")
	}
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return errors.New("invalid email")
	}
	if _, ok := a.Store.GetUser(id); ok {
		return errors.New("user exists")
	}
	a.Store.AddUser(store.User{ID: id, WorkspaceID: a.WorkspaceID, Email: email})
	return nil
}

func (a *Admin) CreateGroup(id, name string) error {
	if !validID.MatchString(id) {
		return errors.New("invalid group id")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("invalid group name")
	}
	if _, ok := a.Store.GetGroup(id); ok {
		return errors.New("group exists")
	}
	a.Store.AddGroup(store.Group{ID: id, WorkspaceID: a.WorkspaceID, Name: name})
	return nil
}

func (a *Admin) SetMember(groupID, userID string, add bool) error {
	g, ok := a.Store.GetGroup(groupID)
	if !ok || g.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown group")
	}
	u, ok := a.Store.GetUser(userID)
	if !ok || u.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown user")
	}
	if add {
		a.Store.AddMember(groupID, userID)
	} else {
		a.Store.RemoveMember(groupID, userID)
	}
	return nil
}

func (a *Admin) addConnectorRow(provider, label, slug, upstreamURL string) store.Connector {
	label = strings.TrimSpace(label)
	if label == "" {
		label = provider
	}
	c := store.Connector{
		ID: newID("c"), WorkspaceID: a.WorkspaceID, Provider: provider,
		InstanceSlug: strings.TrimSpace(slug), Label: label,
		UpstreamURL: upstreamURL, Status: store.StatusActive,
	}
	a.Store.AddConnector(c)
	return c
}

// AddConnectorFromCatalog connects a catalog provider template.
func (a *Admin) AddConnectorFromCatalog(ctx context.Context, providerName, label, slug string) (store.Connector, error) {
	p, ok := a.Catalog.Find(providerName)
	if !ok {
		return store.Connector{}, errors.New("unknown provider")
	}
	c := a.addConnectorRow(p.Key, label, slug, p.URL)
	if err := a.Gateway.AddConnector(ctx, c); err != nil {
		a.Store.DeleteConnector(c.ID)
		return store.Connector{}, err
	}
	return c, nil
}

// AddConnectorCustom connects an arbitrary upstream MCP URL.
func (a *Admin) AddConnectorCustom(ctx context.Context, provider, label, slug, upstreamURL string) (store.Connector, error) {
	key := catalog.Key(provider)
	if key == "" {
		return store.Connector{}, errors.New("invalid provider name")
	}
	upstreamURL = strings.TrimSpace(upstreamURL)
	if !strings.HasPrefix(upstreamURL, "https://") && !strings.HasPrefix(upstreamURL, "http://127.0.0.1") && !strings.HasPrefix(upstreamURL, "http://localhost") {
		return store.Connector{}, errors.New("upstream must be https (or loopback http)")
	}
	c := a.addConnectorRow(key, label, slug, upstreamURL)
	if err := a.Gateway.AddConnector(ctx, c); err != nil {
		a.Store.DeleteConnector(c.ID)
		return store.Connector{}, err
	}
	return c, nil
}

// SyncConnector rediscovers one connector's tools.
func (a *Admin) SyncConnector(ctx context.Context, id string) error {
	c, ok := a.Store.GetConnector(id)
	if !ok || c.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown connector")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return a.Gateway.Resync(ctx, c)
}

// DeleteConnector disconnects and removes one connector instance.
func (a *Admin) DeleteConnector(id string) error {
	c, ok := a.Store.GetConnector(id)
	if !ok || c.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown connector")
	}
	a.Gateway.RemoveConnector(id)
	return nil
}

func (a *Admin) checkTool(publicName string) error {
	t, ok := a.Store.GetTool(publicName)
	if !ok || t.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown tool")
	}
	return nil
}

// SetUserGrant grants or revokes one tool for one user.
func (a *Admin) SetUserGrant(userID, publicName string, grant bool) error {
	u, ok := a.Store.GetUser(userID)
	if !ok || u.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown user")
	}
	if err := a.checkTool(publicName); err != nil {
		return err
	}
	if grant {
		a.Store.AddGrant(store.Grant{UserID: userID, PublicName: publicName})
	} else {
		a.Store.RevokeGrant(userID, publicName)
	}
	return nil
}

// SetGroupGrant grants or revokes one tool for one group.
func (a *Admin) SetGroupGrant(groupID, publicName string, grant bool) error {
	g, ok := a.Store.GetGroup(groupID)
	if !ok || g.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown group")
	}
	if err := a.checkTool(publicName); err != nil {
		return err
	}
	if grant {
		a.Store.AddGroupGrant(store.GroupGrant{GroupID: groupID, PublicName: publicName})
	} else {
		a.Store.RevokeGroupGrant(groupID, publicName)
	}
	return nil
}

// --- JSON API ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (a *Admin) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			writeErr(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next(w, r)
	}
}

// APIRoutes mounts the JSON admin API.
func (a *Admin) APIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/users", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct {
				ID, Email string
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, 400, err)
				return
			}
			if err := a.CreateUser(in.ID, in.Email); err != nil {
				writeErr(w, 400, err)
				return
			}
			writeJSON(w, 201, map[string]string{"id": in.ID})
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 200, map[string]any{"users": a.Store.ListUsers(a.WorkspaceID)})
	}))
	mux.HandleFunc("/api/admin/groups", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct {
				ID, Name string
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, 400, err)
				return
			}
			if err := a.CreateGroup(in.ID, in.Name); err != nil {
				writeErr(w, 400, err)
				return
			}
			writeJSON(w, 201, map[string]string{"id": in.ID})
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 200, map[string]any{"groups": a.Store.ListGroups(a.WorkspaceID)})
	}))
	mux.HandleFunc("/api/admin/groups/{id}/members", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			g, ok := a.Store.GetGroup(r.PathValue("id"))
			if !ok || g.WorkspaceID != a.WorkspaceID {
				writeErr(w, 400, errors.New("unknown group"))
				return
			}
			writeJSON(w, 200, map[string]any{"members": a.Store.MembersOf(g.ID)})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := a.SetMember(r.PathValue("id"), in.UserID, true); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "added"})
	}))
	mux.HandleFunc("/api/admin/groups/{id}/members/{uid}", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := a.SetMember(r.PathValue("id"), r.PathValue("uid"), false); err != nil {
			writeErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/admin/connectors", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct {
				Provider, Label, Slug, URL string
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, 400, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			defer cancel()
			var c store.Connector
			var err error
			if in.URL != "" {
				c, err = a.AddConnectorCustom(ctx, in.Provider, in.Label, in.Slug, in.URL)
			} else {
				c, err = a.AddConnectorFromCatalog(ctx, in.Provider, in.Label, in.Slug)
			}
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			writeJSON(w, 201, c)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 200, map[string]any{"connectors": a.Store.ListConnectors(a.WorkspaceID)})
	}))
	mux.HandleFunc("/api/admin/connectors/{id}/sync", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := a.SyncConnector(r.Context(), r.PathValue("id")); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "synced"})
	}))
	mux.HandleFunc("/api/admin/connectors/{id}", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := a.DeleteConnector(r.PathValue("id")); err != nil {
			writeErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/admin/tools", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 200, map[string]any{"tools": a.Store.ListTools(a.WorkspaceID)})
	}))
	mux.HandleFunc("/api/admin/grants", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var in struct {
				UserID  string `json:"user_id"`
				GroupID string `json:"group_id"`
				Tool    string `json:"tool"`
				Grant   bool   `json:"grant"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, 400, err)
				return
			}
			if in.UserID == "" == (in.GroupID == "") {
				writeErr(w, 400, errors.New("set exactly one of user_id, group_id"))
				return
			}
			var err error
			if in.UserID != "" {
				err = a.SetUserGrant(in.UserID, in.Tool, in.Grant)
			} else {
				err = a.SetGroupGrant(in.GroupID, in.Tool, in.Grant)
			}
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			writeJSON(w, 200, map[string]string{"status": "ok"})
		case http.MethodGet:
			user, group := r.URL.Query().Get("user"), r.URL.Query().Get("group")
			out := map[string]any{}
			if user != "" {
				out["user_grants"] = a.Store.UserGrantsFor(user)
			}
			if group != "" {
				out["group_grants"] = a.Store.GroupGrantsFor(group)
			}
			writeJSON(w, 200, out)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/api/admin/audit", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		events := a.Store.ListAudit()
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
		writeJSON(w, 200, map[string]any{"events": events})
	}))
	mux.HandleFunc("/api/admin/catalog", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 200, map[string]any{"providers": a.Catalog.Providers})
	}))
}
