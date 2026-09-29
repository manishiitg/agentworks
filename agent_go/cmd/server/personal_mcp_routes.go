package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"
)

// /api/me/mcp/* and /api/me/secrets/*: a person manages their own MCP
// servers, their per-Code switches and their personal secrets
// (docs/design/code_private_mcp.md). Every route acts on the caller's own
// store only; nothing here can read or change another person's.

type personalMCPServerView struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Transport string   `json:"transport"`
	OAuth     bool     `json:"oauth"`
	Connected bool     `json:"connected"`
	Headers   []string `json:"headers,omitempty"` // header names only
	Enabled   bool     `json:"enabled"`           // in the requested Code
	Catalog   string   `json:"catalog,omitempty"` // the catalog server it was added from
}

func personalMCPUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	if userID == "" {
		writeAgentProfileError(w, http.StatusUnauthorized, "sign in to manage your MCP servers")
		return "", false
	}
	return userID, true
}

// personalMCPCodeRoot resolves a Code the caller can chat in (any role) to
// its root.
func (api *StreamingAPI) personalMCPCodeRoot(r *http.Request, userID, projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || api.agentProfiles == nil {
		return "", fmt.Errorf("Code workspace not found")
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID)
	if err != nil {
		return "", fmt.Errorf("Code workspace not found")
	}
	project, err := resolveCrewProjectBinding(r.Context(), userID, profile, projectID, "")
	if err != nil || !codeRoleFor(r.Context(), userID, project.OwnerID, project.Binding.ResourceID).atLeast(codeRoleViewer) {
		return "", fmt.Errorf("Code workspace not found")
	}
	return cleanCodeRoot(agentProfileRuntimeWorkspace(project.OwnerID, project.Binding.WorkspacePath)), nil
}

// redactedURL drops the query string and fragment, which can carry tokens.
func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String()
}

// GET /api/me/mcp/servers?code=<project id>
func (api *StreamingAPI) handleListPersonalMCP(w http.ResponseWriter, r *http.Request) {
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	servers, err := listPersonalMCPServers(userID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := map[string]bool{}
	if project := r.URL.Query().Get("code"); project != "" {
		root, err := api.personalMCPCodeRoot(r, userID, project)
		if err != nil {
			writeAgentProfileError(w, http.StatusNotFound, err.Error())
			return
		}
		names, _ := personalMCPEnabled(userID, root)
		for _, name := range names {
			enabled[name] = true
		}
	}
	dir, _ := personalMCPDir(userID)
	views := make([]personalMCPServerView, 0, len(servers))
	for _, server := range servers {
		view := personalMCPServerView{Name: server.Name, URL: redactedURL(server.URL), Transport: server.Transport, OAuth: server.OAuth != nil, Enabled: enabled[server.Name], Catalog: server.Catalog}
		for header := range server.Headers {
			view.Headers = append(view.Headers, header)
		}
		if server.OAuth != nil {
			store := oauth.NewTokenStore(personalMCPTokenFile(dir, userID, server.Name))
			_, loadErr := store.Load()
			view.Connected = loadErr == nil
		} else {
			view.Connected = true
		}
		views = append(views, view)
	}
	secrets, _ := listPersonalSecretNames(userID)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"servers": views, "secrets": secrets})
}

// POST /api/me/mcp/servers {name, url, transport, headers}
// The server is probed (public-only) for OAuth; a server that needs sign-in
// keeps its discovered endpoints, and is connected with .../connect.
func (api *StreamingAPI) handleAddPersonalMCP(w http.ResponseWriter, r *http.Request) {
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	var request struct {
		personalMCPServer
		// Catalog adds one of the platform's remote servers as the person's
		// own: same URL and sign-in endpoints, their own login.
		Catalog string `json:"catalog"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid request")
		return
	}
	saved, status, err := api.addPersonalMCP(r.Context(), userID, request.personalMCPServer, request.Catalog)
	if err != nil {
		writeAgentProfileError(w, status, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"name": saved.Name, "oauth": saved.OAuth != nil})
}

// addPersonalMCP adds (or replaces) one of the person's servers: a catalog
// server by name, or their own URL. The HTTP route and the Code agent's
// manage_my_mcp_servers tool both use it. The int is the HTTP status of an error.
func (api *StreamingAPI) addPersonalMCP(ctx context.Context, userID string, body personalMCPServer, catalog string) (personalMCPServer, int, error) {
	body.OAuth, body.Catalog = nil, ""
	var catalogClient *registeredClient
	if strings.TrimSpace(catalog) != "" {
		entry, ok := api.personalMCPCatalogEntry(catalog)
		if !ok {
			return personalMCPServer{}, http.StatusBadRequest, fmt.Errorf("%q is not a remote server in the catalog", catalog)
		}
		body.URL, body.Transport, body.Headers, body.Catalog = entry.config.URL, string(entry.config.GetProtocol()), nil, entry.Catalog
		if strings.TrimSpace(body.Name) == "" {
			body.Name = entry.Name
		}
		if entry.config.OAuth != nil {
			catalogOAuth := *entry.config.OAuth
			// The catalog's OAuth app (an admin's Google or GitHub app) is
			// shared; a server with dynamic registration registers per person.
			if catalogOAuth.ClientID != "" && catalogOAuth.RegistrationEndpoint == "" {
				secret := catalogOAuth.ClientSecret
				if secret == "" && catalogOAuth.ClientSecretFile != "" {
					secret, _ = oauth.ReadClientSecretFile(catalogOAuth.ClientSecretFile)
				}
				catalogClient = &registeredClient{ClientID: catalogOAuth.ClientID, ClientSecret: secret}
			}
			catalogOAuth.ClientSecretFile = ""
			catalogOAuth.ClientID, catalogOAuth.ClientSecret, catalogOAuth.RedirectURL, catalogOAuth.UsePKCE = "", "", "", true
			body.OAuth = &catalogOAuth
		}
	}
	if err := validatePersonalMCPServer(&body); err != nil {
		return personalMCPServer{}, http.StatusBadRequest, err
	}
	if body.OAuth != nil {
		for _, endpoint := range []string{body.OAuth.AuthURL, body.OAuth.TokenURL} {
			if err := netguard.CheckURL(endpoint, true); err != nil {
				return personalMCPServer{}, http.StatusBadRequest, fmt.Errorf("the server's sign-in endpoint was refused: %w", err)
			}
		}
	} else if len(body.Headers) == 0 && catalog == "" {
		probe, err := services.ProbeMCPServerAuthWith(ctx, netguard.Client(15*time.Second), body.URL)
		if err != nil {
			return personalMCPServer{}, http.StatusBadRequest, fmt.Errorf("could not check %s: %w", redactedURL(body.URL), err)
		}
		if !probe.NoAuthRequired && probe.Endpoints != nil {
			body.OAuth = &oauth.OAuthConfig{
				AuthURL: probe.Endpoints.AuthURL, TokenURL: probe.Endpoints.TokenURL,
				RegistrationEndpoint: probe.Endpoints.RegistrationEndpoint, Resource: probe.Endpoints.Resource,
				Scopes: probe.Endpoints.ScopesSupported, UsePKCE: true,
			}
			for _, endpoint := range []string{body.OAuth.AuthURL, body.OAuth.TokenURL} {
				if err := netguard.CheckURL(endpoint, true); err != nil {
					return personalMCPServer{}, http.StatusBadRequest, fmt.Errorf("the server's sign-in endpoint was refused: %w", err)
				}
			}
		}
	}
	// Adding over an existing name starts clean: the old sign-in client and
	// login belong to whatever that name pointed at before.
	if err := forgetPersonalMCPLogin(userID, body.Name); err != nil {
		return personalMCPServer{}, http.StatusInternalServerError, err
	}
	saved, err := addPersonalMCPServer(userID, body)
	if err != nil {
		return personalMCPServer{}, http.StatusBadRequest, err
	}
	if catalogClient != nil {
		if err := writePersonalMCPClient(userID, saved.Name, *catalogClient); err != nil {
			return personalMCPServer{}, http.StatusInternalServerError, err
		}
	}
	closePersonalMCPConnection(userID, saved.Name)
	return saved, http.StatusOK, nil
}

// DELETE /api/me/mcp/servers/{name}
func (api *StreamingAPI) handleRemovePersonalMCP(w http.ResponseWriter, r *http.Request) {
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	name := mux.Vars(r)["name"]
	if err := removePersonalMCPServer(userID, name); err != nil {
		writeAgentProfileError(w, http.StatusNotFound, err.Error())
		return
	}
	closePersonalMCPConnection(userID, name)
	_ = forgetPersonalMCPLogin(userID, name)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"removed": name})
}

// PUT /api/me/mcp/servers/{name}/codes/{project_id} {enabled}
func (api *StreamingAPI) handleSwitchPersonalMCP(w http.ResponseWriter, r *http.Request) {
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid request")
		return
	}
	root, err := api.personalMCPCodeRoot(r, userID, mux.Vars(r)["project_id"])
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := setPersonalMCPEnabled(userID, root, mux.Vars(r)["name"], body.Enabled); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"enabled": body.Enabled, "applies": "from your next message"})
}

// POST /api/me/mcp/servers/{name}/connect {client_id?}: the OAuth sign-in,
// through the shared flow with public-only discovery, registration and token
// requests, the token sealed in the person's own store.
func (api *StreamingAPI) handleConnectPersonalMCP(w http.ResponseWriter, r *http.Request) {
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
	var entered *registeredClient
	if clientID := strings.TrimSpace(body.ClientID); clientID != "" {
		entered = &registeredClient{ClientID: clientID, ClientSecret: strings.TrimSpace(body.ClientSecret)}
	}
	authURL, discovery, status, err := api.startPersonalMCPSignIn(userID, mux.Vars(r)["name"], deriveOAuthRedirectURI(r), entered)
	if err != nil {
		writeAgentProfileError(w, status, err.Error())
		return
	}
	if discovery != nil {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"status": discovery.Status, "message": discovery.Message, "redirect_uri": discovery.RedirectURI})
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"auth_url": authURL})
}

// startPersonalMCPSignIn starts the person's OAuth sign-in to one of their
// servers: through the shared flow with public-only discovery, registration
// and token requests, the token sealed in their own store. It returns the
// URL to open, or a discovery answer when the provider needs the person's own
// OAuth app (entered then carries its client ID and secret).
func (api *StreamingAPI) startPersonalMCPSignIn(userID, name, redirectURI string, entered *registeredClient) (string, *OAuthDiscoveryResponse, int, error) {
	internal, cfg, err := personalMCPServerConfig(userID, name)
	if err != nil {
		return "", nil, http.StatusNotFound, err
	}
	if cfg.OAuth == nil {
		return "", nil, http.StatusBadRequest, fmt.Errorf("this server does not use sign-in")
	}
	cfg.OAuth.RedirectURL = redirectURI
	dir, err := personalMCPDir(userID)
	if err != nil {
		return "", nil, http.StatusInternalServerError, err
	}
	// An entered client is kept sealed once the sign-in succeeds, so
	// refreshes keep working and a mistyped one never replaces a client that
	// works.
	if entered != nil {
		entered.RedirectURI = redirectURI
		cfg.OAuth.ClientID, cfg.OAuth.ClientSecret = entered.ClientID, entered.ClientSecret
	} else if cfg.OAuth.RegistrationEndpoint != "" {
		// A dynamic registration is tied to its callback; one made for
		// another address registers again.
		if client, _ := readPersonalMCPClient(dir, userID, name); client != nil && client.RedirectURI != redirectURI {
			cfg.OAuth.ClientID, cfg.OAuth.ClientSecret = "", ""
		}
	}
	start, discovery, err := api.runOAuthFlow("", redirectURI, oauthFlowTarget{
		Name:       internal,
		Config:     cfg,
		ClientFile: personalMCPClientFile(dir, userID, name),
		Discoverer: oauth.Discoverer{Client: netguard.Client(30 * time.Second)},
		OnSuccess: func(*OAuthFlowState) {
			if entered != nil {
				if err := writePersonalMCPClient(userID, name, *entered); err != nil {
					log.Printf("[PERSONAL_MCP] keep sign-in client for %s: %v", name, err)
				}
			}
			closePersonalMCPConnection(userID, name)
		},
	})
	if err != nil {
		return "", nil, http.StatusBadRequest, err
	}
	if discovery != nil {
		return "", discovery, http.StatusOK, nil
	}
	return start.AuthURL, nil, http.StatusOK, nil
}

// PUT /api/me/secrets/{name} {encrypted_value} (from /api/secrets/encrypt,
// bound to the caller); DELETE removes it. Values are never returned.
func (api *StreamingAPI) handlePutPersonalSecret(w http.ResponseWriter, r *http.Request) {
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	name := mux.Vars(r)["name"]
	if r.Method == http.MethodDelete {
		if err := deletePersonalSecret(userID, name); err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"removed": name})
		return
	}
	var body struct {
		EncryptedValue string `json:"encrypted_value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid request")
		return
	}
	value, err := decryptSecretValue(body.EncryptedValue, userID)
	if err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid secret value")
		return
	}
	if err := setPersonalSecret(userID, name, value); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Servers whose headers use it reconnect with the new value.
	if servers, err := listPersonalMCPServers(userID); err == nil {
		for _, server := range servers {
			for _, ref := range server.Headers {
				if ref.Secret == name {
					closePersonalMCPConnection(userID, server.Name)
				}
			}
		}
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"saved": name})
}

func personalMCPClientFile(dir, userID, name string) string {
	return filepath.Join(dir, "clients", personalMCPInternalName(userID, name)+".json")
}

// closePersonalMCPConnection drops a person's pooled connection to one
// server, so the next call reconnects with its current config and login.
func closePersonalMCPConnection(userID, name string) {
	mcpclient.GetSessionRegistry().CloseSessionServer("global", personalMCPInternalName(userID, name))
}

// personalMCPCatalogServer is a platform catalog server a person can add as
// their own: remote, public, and signed into per person (OAuth) or open. A
// server whose credentials are platform headers is not offered.
type personalMCPCatalogServer struct {
	Name        string `json:"name"`
	Catalog     string `json:"catalog"`
	Description string `json:"description,omitempty"`
	SignIn      bool   `json:"sign_in"`
	// NeedsClient: the provider has no dynamic registration and the catalog
	// carries no client, so the person enters their OAuth app's client.
	NeedsClient bool `json:"needs_client"`
	config      mcpclient.MCPServerConfig
}

func (api *StreamingAPI) personalMCPCatalog() []personalMCPCatalogServer {
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil
	}
	out := []personalMCPCatalogServer{}
	for name, cfg := range catalog.MCPServers {
		protocol := cfg.GetProtocol()
		if cfg.URL == "" || len(cfg.Headers) > 0 || (protocol != mcpclient.ProtocolHTTP && protocol != mcpclient.ProtocolSSE) {
			continue
		}
		if netguard.CheckURL(cfg.URL, true) != nil {
			continue
		}
		local := strings.Trim(personalMCPCatalogNameCleaner.ReplaceAllString(strings.ToLower(name), "_"), "_")
		if len(local) > 40 {
			local = local[:40]
		}
		if !personalMCPNamePattern.MatchString(local) {
			continue
		}
		entry := personalMCPCatalogServer{Name: local, Catalog: name, Description: cfg.Description, SignIn: cfg.OAuth != nil, config: cfg}
		if cfg.OAuth != nil {
			entry.NeedsClient = cfg.OAuth.ClientID == "" && cfg.OAuth.RegistrationEndpoint == ""
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Catalog < out[j].Catalog })
	return out
}

var personalMCPCatalogNameCleaner = regexp.MustCompile(`[^a-z0-9_]+`)

func (api *StreamingAPI) personalMCPCatalogEntry(name string) (personalMCPCatalogServer, bool) {
	for _, entry := range api.personalMCPCatalog() {
		if entry.Catalog == strings.TrimSpace(name) {
			return entry, true
		}
	}
	return personalMCPCatalogServer{}, false
}

// GET /api/me/mcp/catalog: the catalog servers a person can add as their own.
func (api *StreamingAPI) handlePersonalMCPCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := personalMCPUser(w, r); !ok {
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"servers": api.personalMCPCatalog()})
}

// forgetPersonalMCPLogin removes a server's sign-in client and login.
func forgetPersonalMCPLogin(userID, name string) error {
	dir, err := personalMCPDir(userID)
	if err != nil {
		return err
	}
	for _, file := range []string{personalMCPClientFile(dir, userID, name), personalMCPTokenFile(dir, userID, name)} {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
