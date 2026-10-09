// Package admin serves workspace management: users, groups, connectors,
// tool grants, and audit history, as a JSON API (/api/admin) and a
// server-rendered UI (/admin). Single workspace in M0.
package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/setupagent"
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
	SetupAgent  *setupagent.Agent
	sessionsMu  sync.Mutex
	sessions    map[string]time.Time
}

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func newID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

func (a *Admin) authed(r *http.Request) bool {
	if a.HumanToken == "" {
		return false
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(a.HumanToken)) == 1
	}
	c, err := r.Cookie("gw_admin")
	if err != nil || !a.validSession(c.Value) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host {
			return false
		}
	}
	return true
}

const adminSessionLifetime = 12 * time.Hour

func (a *Admin) issueSession() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(random[:])
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if a.sessions == nil {
		a.sessions = make(map[string]time.Time)
	}
	for value, expiry := range a.sessions {
		if !time.Now().Before(expiry) {
			delete(a.sessions, value)
		}
	}
	if len(a.sessions) >= 1024 {
		var oldest string
		var earliest time.Time
		for value, expiry := range a.sessions {
			if oldest == "" || expiry.Before(earliest) {
				oldest, earliest = value, expiry
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[token] = time.Now().Add(adminSessionLifetime)
	return token, nil
}

func (a *Admin) validSession(token string) bool {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	expiry, ok := a.sessions[token]
	if ok && !time.Now().Before(expiry) {
		delete(a.sessions, token)
		return false
	}
	return ok
}

func (a *Admin) revokeSession(token string) {
	a.sessionsMu.Lock()
	delete(a.sessions, token)
	a.sessionsMu.Unlock()
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
		h.Set("Access-Control-Expose-Headers", "WWW-Authenticate")
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
	return a.CreateGroupWithDescription(id, name, "")
}

func (a *Admin) CreateGroupWithDescription(id, name, description string) error {
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
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > store.MaxGroupDescriptionLength {
		return errors.New("group description must be at most 1000 characters")
	}
	a.Store.AddGroup(store.Group{ID: id, WorkspaceID: a.WorkspaceID, Name: name, Description: description})
	return nil
}

func (a *Admin) SetMember(groupID, userID string, add bool, actor string) error {
	g, ok := a.Store.GetGroup(groupID)
	if !ok || g.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown group")
	}
	if g.BuiltIn {
		return errors.New("built-in group membership is automatic")
	}
	u, ok := a.Store.GetUser(userID)
	if !ok || u.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown user")
	}
	action := "add_member"
	if add {
		a.Store.AddMember(groupID, userID)
	} else {
		a.Store.RemoveMember(groupID, userID)
		action = "remove_member"
	}
	a.Store.AppendPolicyEvent(a.WorkspaceID, store.PolicyEvent{At: time.Now().UTC(), Actor: actor, Action: action, GroupID: groupID, UserID: userID})
	return nil
}

func (a *Admin) addConnectorRow(provider, label, slug, upstreamURL string, oauthServer ...string) (store.Connector, error) {
	return a.addConnectorRowFor("", provider, label, slug, upstreamURL, oauthServer...)
}

// addConnectorRowFor creates the connection row; vaultID makes it a person-owned vault's connection from the moment it
// exists, so it is never granted to the whole company (PLAT-507).
func (a *Admin) addConnectorRowFor(vaultID, provider, label, slug, upstreamURL string, oauthServer ...string) (store.Connector, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = provider
	}
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return store.Connector{}, errors.New("could not create connection identity")
	}
	c := store.Connector{
		ID: "c-" + hex.EncodeToString(identity[:]), WorkspaceID: a.WorkspaceID, Provider: provider,
		InstanceSlug: strings.TrimSpace(slug), Label: label,
		UpstreamURL: upstreamURL, Status: store.StatusActive, VaultID: vaultID,
	}
	if len(label) > 100 || len(c.InstanceSlug) > 100 {
		return store.Connector{}, errors.New("connection name and instance must be at most 100 characters")
	}
	if len(oauthServer) > 0 && c.InstanceSlug == "" {
		for _, existing := range a.Store.ListConnectors(a.WorkspaceID) {
			if existing.Provider == provider {
				c.InstanceSlug = c.ID
				break
			}
		}
	}
	if len(oauthServer) > 0 {
		c.OAuthServer = oauthServer[0]
		if c.OAuthServer != "" {
			c.OAuthCredentialID = c.ID
			c.Status = store.StatusAuthRequired
			if c.InstanceSlug == "" {
				c.InstanceSlug = c.ID
			}
		}
	}
	if !a.Store.AddConnectorUnique(c) {
		return store.Connector{}, errors.New("a connector with this provider and instance name already exists")
	}
	return c, nil
}

// AddConnectorFromCatalog connects a catalog provider template.
func (a *Admin) AddConnectorFromCatalog(ctx context.Context, providerName, label, slug string) (store.Connector, error) {
	return a.addConnectorFromCatalogFor(ctx, "", providerName, label, slug)
}

func (a *Admin) addConnectorFromCatalogFor(ctx context.Context, vaultID, providerName, label, slug string) (store.Connector, error) {
	p, ok := a.Catalog.Find(providerName)
	if !ok {
		// A bare "unknown provider" left people guessing (Confida 2026-10-07: "Github" is not in Vault's catalog).
		return store.Connector{}, fmt.Errorf("%q is not an app Vault can connect. Available: %s. Google apps, GitHub and Slack are connected through their own integrations (Integrations), not Vault; for another app, store its API key as a Vault secret", strings.TrimSpace(providerName), strings.Join(a.Catalog.Names(), ", "))
	}
	if p.OAuth && (a.Gateway == nil || !a.Gateway.HasSharedOAuth()) {
		return store.Connector{}, errors.New("this provider requires upstream OAuth; configure the shared product OAuth service")
	}
	if err := a.Gateway.ValidateUpstreamURL(p.URL); err != nil {
		return store.Connector{}, err
	}
	oauthServer := ""
	if p.OAuth {
		oauthServer = p.Name
	}
	if strings.TrimSpace(label) == "" {
		label = p.Name
	}
	c, err := a.addConnectorRowFor(vaultID, p.Key, label, slug, p.URL, oauthServer)
	if err != nil {
		return store.Connector{}, err
	}
	if c.OAuthCredentialID != "" {
		return c, nil
	}
	if err := a.Gateway.AddConnector(ctx, c); err != nil {
		a.Store.DeleteConnector(c.ID)
		return store.Connector{}, err
	}
	return c, nil
}

// AddConnectorCustom connects an arbitrary upstream MCP URL.
func (a *Admin) AddConnectorCustom(ctx context.Context, provider, label, slug, upstreamURL string) (store.Connector, error) {
	return a.AddConnectorCustomWithBearer(ctx, provider, label, slug, upstreamURL, "")
}

func (a *Admin) AddConnectorCustomWithBearer(ctx context.Context, provider, label, slug, upstreamURL, bearerToken string) (store.Connector, error) {
	return a.AddConnectorCustomWithCredential(ctx, provider, label, slug, upstreamURL, bearerToken, "Bearer")
}

func validateConnectorCredential(token, scheme string) error {
	if strings.ContainsAny(token, "\r\n") || len(token) > 8192 || (scheme != "" && scheme != "Bearer" && scheme != "Basic") {
		return errors.New("invalid connector credential")
	}
	if scheme == "Basic" {
		decoded, err := base64.StdEncoding.DecodeString(token)
		if err != nil || !bytes.Contains(decoded, []byte(":")) {
			return errors.New("invalid Basic credential")
		}
	}
	return nil
}

// The scheme is public metadata; credential bytes stay in the encrypted store.
func (a *Admin) AddConnectorCustomWithCredential(ctx context.Context, provider, label, slug, upstreamURL, bearerToken, scheme string) (store.Connector, error) {
	if err := validateConnectorCredential(bearerToken, scheme); err != nil {
		return store.Connector{}, err
	}
	key := catalog.Key(provider)
	if key == "" {
		return store.Connector{}, errors.New("invalid provider name")
	}
	upstreamURL = strings.TrimSpace(upstreamURL)
	if err := a.Gateway.ValidateUpstreamURL(upstreamURL); err != nil {
		return store.Connector{}, err
	}
	c, err := a.addConnectorRow(key, label, slug, upstreamURL)
	if err != nil {
		return store.Connector{}, err
	}
	if c.OAuthServer != "" && scheme == "Basic" {
		a.Store.DeleteConnector(c.ID)
		return store.Connector{}, errors.New("OAuth connectors cannot use Basic credentials")
	}
	c.AuthScheme = scheme
	a.Store.AddConnector(c)
	a.Store.SetConnectorBearer(c.ID, bearerToken)
	if c.OAuthCredentialID != "" {
		return c, nil
	}
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

// ApproveTool activates only the exact discovered definition the admin saw.
func (a *Admin) ApproveTool(publicName, fingerprint string, version int) (store.ToolSnapshot, error) {
	t, ok := a.Store.ApproveTool(a.WorkspaceID, publicName, fingerprint, version)
	if !ok {
		return store.ToolSnapshot{}, errors.New("tool changed, was removed, or is not awaiting review; refresh and try again")
	}
	return t, nil
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func (a *Admin) checkTool(publicName string) error {
	t, ok := a.Store.GetTool(publicName)
	if !ok || t.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown tool")
	}
	return nil
}

// ErrGroupOnlyAccess: Vault access is given to groups only (owner,
// 2026-10-09). Old direct grants still work and can be revoked.
var ErrGroupOnlyAccess = errors.New("Vault access is group-only: add the person to a group that has the tool (a group can have one member)")

// SetUserGrant revokes one tool granted directly to one user. New direct
// grants are refused.
func (a *Admin) SetUserGrant(userID, publicName string, grant bool) error {
	if grant {
		return ErrGroupOnlyAccess
	}
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

// RenameGroup changes a group's display name, preserving its description.
func (a *Admin) RenameGroup(id, name string) error {
	return a.UpdateGroup(id, &name, nil)
}

func (a *Admin) UpdateGroup(id string, name, description *string) error {
	g, ok := a.Store.GetGroup(id)
	if !ok || g.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown group")
	}
	if name == nil && description == nil {
		return errors.New("provide a group name or description")
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if g.BuiltIn && trimmed != g.Name {
			return errors.New("built-in group name cannot be changed")
		}
		if trimmed == "" {
			return errors.New("invalid group name")
		}
		name = &trimmed
	}
	if description != nil {
		trimmed := strings.TrimSpace(*description)
		if utf8.RuneCountInString(trimmed) > store.MaxGroupDescriptionLength {
			return errors.New("group description must be at most 1000 characters")
		}
		description = &trimmed
	}
	a.Store.UpdateGroup(id, name, description)
	return nil
}

// CreateGroupKey mints a shareable API key carrying the group's grants.
// The token is returned once; listings never include it.
func (a *Admin) CreateGroupKey(groupID, label string) (store.APIKey, error) {
	g, ok := a.Store.GetGroup(groupID)
	if !ok || g.WorkspaceID != a.WorkspaceID {
		return store.APIKey{}, errors.New("unknown group")
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return store.APIKey{}, err
	}
	idBytes := make([]byte, 4)
	if _, err := rand.Read(idBytes); err != nil {
		return store.APIKey{}, err
	}
	k := store.APIKey{
		ID:          "key_" + hex.EncodeToString(idBytes),
		WorkspaceID: a.WorkspaceID,
		GroupID:     groupID,
		Label:       strings.TrimSpace(label),
		Token:       "gwk_" + hex.EncodeToString(tokenBytes),
		CreatedAt:   time.Now(),
	}
	a.Store.AddAPIKey(k)
	return k, nil
}

// RevokeGroupKey deletes an API key by ID.
func (a *Admin) RevokeGroupKey(groupID, id string) error {
	g, ok := a.Store.GetGroup(groupID)
	if !ok || g.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown group")
	}
	if !a.Store.RevokeAPIKey(groupID, id) {
		return errors.New("unknown key")
	}
	return nil
}

// SetGroupServer attaches or detaches a whole connector to a group.
func (a *Admin) SetGroupServer(groupID, connectorID string, attach bool) error {
	g, ok := a.Store.GetGroup(groupID)
	if !ok || g.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown group")
	}
	c, ok := a.Store.GetConnector(connectorID)
	if !ok || c.WorkspaceID != a.WorkspaceID {
		return errors.New("unknown connector")
	}
	if attach {
		a.Store.AddGroupServerGrant(groupID, connectorID)
	} else {
		a.Store.RevokeGroupServerGrant(groupID, connectorID)
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
		if a.Store.PersistenceError() != nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("configuration storage unavailable; repair storage and restart the gateway"))
			return
		}
		if r.Method == http.MethodGet {
			next(w, r)
			return
		}
		// Do not acknowledge a configuration change before its durable write.
		buffer := &adminResponseBuffer{header: make(http.Header), status: http.StatusOK}
		next(buffer, r)
		if a.Store.PersistenceError() != nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("configuration could not be saved; repair storage and restart the gateway"))
			return
		}
		for key, values := range buffer.header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(buffer.status)
		_, _ = w.Write(buffer.body.Bytes())
	}
}

type adminResponseBuffer struct {
	header      http.Header
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (b *adminResponseBuffer) Header() http.Header { return b.header }
func (b *adminResponseBuffer) WriteHeader(status int) {
	if !b.wroteHeader {
		b.status = status
		b.wroteHeader = true
	}
}
func (b *adminResponseBuffer) Write(data []byte) (int, error) {
	if !b.wroteHeader {
		b.WriteHeader(http.StatusOK)
	}
	return b.body.Write(data)
}

// APIRoutes mounts the JSON admin API.
func (a *Admin) APIRoutes(mux *http.ServeMux) {
	a.runtimeRoutes(mux)
	a.secretRoutes(mux)
	a.accessRoutes(mux)
	a.databaseRoutes(mux)
	a.setupRoutes(mux)
	a.vaultRoutes(mux)
	mux.HandleFunc("/api/admin/users/sync", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct{ ID, Email string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&in) != nil || !validID.MatchString(in.ID) {
			writeErr(w, http.StatusBadRequest, errors.New("invalid product identity"))
			return
		}
		a.Store.AddUser(store.User{ID: in.ID, WorkspaceID: a.WorkspaceID, Email: in.Email})
		w.WriteHeader(http.StatusNoContent)
	}))
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
				ID, Name, Description string
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, 400, err)
				return
			}
			if err := a.CreateGroupWithDescription(in.ID, in.Name, in.Description); err != nil {
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
	mux.HandleFunc("/api/admin/groups/{id}", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Name, Description *string
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := a.UpdateGroup(r.PathValue("id"), in.Name, in.Description); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "renamed"})
	}))
	mux.HandleFunc("/api/admin/groups/{id}/keys", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			g, ok := a.Store.GetGroup(r.PathValue("id"))
			if !ok || g.WorkspaceID != a.WorkspaceID {
				writeErr(w, 400, errors.New("unknown group"))
				return
			}
			writeJSON(w, 200, map[string]any{"keys": a.Store.ListAPIKeys(g.ID)})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Label string
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		k, err := a.CreateGroupKey(r.PathValue("id"), in.Label)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 201, k)
	}))
	mux.HandleFunc("/api/admin/groups/{id}/keys/{keyid}", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := a.RevokeGroupKey(r.PathValue("id"), r.PathValue("keyid")); err != nil {
			writeErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
		if err := a.SetMember(r.PathValue("id"), in.UserID, true, adminActor(r)); err != nil {
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
		if err := a.SetMember(r.PathValue("id"), r.PathValue("uid"), false, adminActor(r)); err != nil {
			writeErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/admin/groups/{id}/servers", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			g, ok := a.Store.GetGroup(r.PathValue("id"))
			if !ok || g.WorkspaceID != a.WorkspaceID {
				writeErr(w, 400, errors.New("unknown group"))
				return
			}
			writeJSON(w, 200, map[string]any{"servers": a.Store.GroupServersFor(g.ID)})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ConnectorID string `json:"connector_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := a.SetGroupServer(r.PathValue("id"), in.ConnectorID, true); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "attached"})
	}))
	mux.HandleFunc("/api/admin/groups/{id}/servers/{cid}", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := a.SetGroupServer(r.PathValue("id"), r.PathValue("cid"), false); err != nil {
			writeErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/admin/connectors", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct {
				Provider, Label, Slug, URL, BearerToken, AuthScheme string
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
				c, err = a.AddConnectorCustomWithCredential(ctx, in.Provider, in.Label, in.Slug, in.URL, in.BearerToken, in.AuthScheme)
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
	mux.HandleFunc("/api/admin/connectors/{id}/credential", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		c, ok := a.Store.GetConnector(r.PathValue("id"))
		if !ok || c.WorkspaceID != a.WorkspaceID {
			writeErr(w, http.StatusNotFound, errors.New("connector not found"))
			return
		}
		var in struct{ BearerToken string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if validateConnectorCredential(in.BearerToken, c.AuthScheme) != nil {
			writeErr(w, http.StatusBadRequest, errors.New("invalid bearer token"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if err := a.Gateway.ReplaceConnectorCredentials(ctx, c, in.BearerToken); err != nil {
			writeErr(w, http.StatusBadGateway, errors.New("connector did not accept the replacement credential"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	}))
	mux.HandleFunc("/api/admin/connectors/{id}/oauth/disconnect", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		c, ok := a.Store.GetConnector(r.PathValue("id"))
		if !ok || c.WorkspaceID != a.WorkspaceID || c.OAuthCredentialID == "" {
			writeErr(w, 404, errors.New("OAuth connection not found"))
			return
		}
		a.Gateway.SuspendOAuth(c)
		writeJSON(w, 200, map[string]string{"status": store.StatusAuthRequired})
	}))
	mux.HandleFunc("/api/admin/connectors/{id}", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			c, ok := a.Store.GetConnector(r.PathValue("id"))
			if !ok || c.WorkspaceID != a.WorkspaceID {
				writeErr(w, 404, errors.New("connector not found"))
				return
			}
			writeJSON(w, 200, c)
			return
		}
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
	mux.HandleFunc("/api/admin/tools/{name}/versions", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := r.PathValue("name")
		t, ok := a.Store.GetTool(name)
		if !ok || t.WorkspaceID != a.WorkspaceID {
			writeErr(w, http.StatusNotFound, errors.New("unknown tool"))
			return
		}
		writeJSON(w, 200, map[string]any{"versions": a.Store.ListToolVersions(a.WorkspaceID, name)})
	}))
	mux.HandleFunc("/api/admin/tools/{name}/approve", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Fingerprint string `json:"fingerprint"`
			Version     int    `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		t, err := a.ApproveTool(r.PathValue("name"), in.Fingerprint, in.Version)
		if err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
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
	mux.HandleFunc("/api/admin/audit/settings", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, a.Store.AuditInfo())
	}))
	mux.HandleFunc("/api/admin/audit", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		filter, err := parseAuditFilter(q, a.WorkspaceID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		format := q.Get("format")
		if format != "" && format != "csv" && format != "json" {
			writeErr(w, http.StatusBadRequest, errors.New("invalid audit export format"))
			return
		}
		if format == "" {
			filter.Limit = 100
			if v := q.Get("limit"); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 5000 {
					writeErr(w, http.StatusBadRequest, errors.New("audit limit must be 1 to 5000"))
					return
				}
				filter.Limit = n
			}
		}
		events, err := a.Store.ReadAudit(filter)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("audit storage unavailable"))
			return
		}
		if format == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="gateway-audit.csv"`)
			writer := csv.NewWriter(w)
			_ = writer.Write([]string{"time", "call_id", "user", "groups", "client", "connector", "tool", "decision", "outcome", "duration_ms", "error", "input", "output", "input_truncated", "output_truncated"})
			for _, e := range events {
				_ = writer.Write([]string{
					e.Timestamp.Format(time.RFC3339Nano), csvSafe(e.CallID), csvSafe(e.UserID), csvSafe(strings.Join(e.GroupIDs, ";")),
					csvSafe(e.ClientID), csvSafe(e.ConnectorID), csvSafe(e.PublicName), e.Decision, e.Outcome,
					strconv.FormatInt(e.DurationMs, 10), csvSafe(e.ErrorText),
					csvSafe(string(e.Input)), csvSafe(string(e.Output)), strconv.FormatBool(e.InputTruncated), strconv.FormatBool(e.OutputTruncated),
				})
			}
			writer.Flush()
			return
		}
		if format == "json" {
			w.Header().Set("Content-Disposition", `attachment; filename="gateway-audit.json"`)
		}
		writeJSON(w, 200, map[string]any{"events": events})
	}))
	mux.HandleFunc("/api/admin/usage", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		filter, err := parseAuditFilter(r.URL.Query(), a.WorkspaceID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		summary, err := a.Store.ReadAuditSummary(filter)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("audit storage unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}))
	mux.HandleFunc("/api/admin/catalog", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 200, map[string]any{"providers": a.Catalog.Providers})
	}))
}

func parseAuditFilter(q url.Values, workspaceID string) (store.AuditFilter, error) {
	filter := store.AuditFilter{
		WorkspaceID: workspaceID,
		UserID:      q.Get("user"), GroupID: q.Get("group"), ClientID: q.Get("client"),
		ConnectorID: q.Get("connector"), PublicName: q.Get("tool"),
		Decision: q.Get("decision"), Outcome: q.Get("outcome"),
	}
	for _, bound := range []struct {
		value  string
		target *time.Time
	}{{q.Get("after"), &filter.After}, {q.Get("before"), &filter.Before}} {
		if bound.value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, bound.value)
		if err != nil {
			return store.AuditFilter{}, errors.New("invalid audit date; use RFC3339")
		}
		*bound.target = parsed
	}
	return filter, nil
}

// csvSafe keeps exported metadata from becoming spreadsheet formulas.
func csvSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}
