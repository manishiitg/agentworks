package server

import (
	"encoding/json"
	"fmt"
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
		view := personalMCPServerView{Name: server.Name, URL: redactedURL(server.URL), Transport: server.Transport, OAuth: server.OAuth != nil, Enabled: enabled[server.Name]}
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
	body := request.personalMCPServer
	body.OAuth = nil
	var catalogClient *registeredClient
	if strings.TrimSpace(request.Catalog) != "" {
		entry, ok := api.personalMCPCatalogEntry(request.Catalog)
		if !ok {
			writeAgentProfileError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a remote server in the catalog", request.Catalog))
			return
		}
		body.URL, body.Transport, body.Headers = entry.config.URL, string(entry.config.GetProtocol()), nil
		if strings.TrimSpace(body.Name) == "" {
			body.Name = entry.Name
		}
		if entry.config.OAuth != nil {
			catalogOAuth := *entry.config.OAuth
			if catalogOAuth.ClientID != "" {
				catalogClient = &registeredClient{ClientID: catalogOAuth.ClientID, ClientSecret: catalogOAuth.ClientSecret}
			}
			catalogOAuth.ClientID, catalogOAuth.ClientSecret, catalogOAuth.RedirectURL, catalogOAuth.UsePKCE = "", "", "", true
			body.OAuth = &catalogOAuth
		}
	}
	if err := validatePersonalMCPServer(&body); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.OAuth != nil {
		for _, endpoint := range []string{body.OAuth.AuthURL, body.OAuth.TokenURL} {
			if err := netguard.CheckURL(endpoint, true); err != nil {
				writeAgentProfileError(w, http.StatusBadRequest, fmt.Sprintf("the server's sign-in endpoint was refused: %v", err))
				return
			}
		}
	} else if len(body.Headers) == 0 && request.Catalog == "" {
		probe, err := services.ProbeMCPServerAuthWith(r.Context(), netguard.Client(15*time.Second), body.URL)
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, fmt.Sprintf("could not check %s: %v", redactedURL(body.URL), err))
			return
		}
		if !probe.NoAuthRequired && probe.Endpoints != nil {
			body.OAuth = &oauth.OAuthConfig{
				AuthURL: probe.Endpoints.AuthURL, TokenURL: probe.Endpoints.TokenURL,
				RegistrationEndpoint: probe.Endpoints.RegistrationEndpoint, Resource: probe.Endpoints.Resource,
				Scopes: probe.Endpoints.ScopesSupported, UsePKCE: true,
			}
			for _, endpoint := range []string{body.OAuth.AuthURL, body.OAuth.TokenURL} {
				if err := netguard.CheckURL(endpoint, true); err != nil {
					writeAgentProfileError(w, http.StatusBadRequest, fmt.Sprintf("the server's sign-in endpoint was refused: %v", err))
					return
				}
			}
		}
	}
	saved, err := addPersonalMCPServer(userID, body)
	if err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	if catalogClient != nil {
		if err := writePersonalMCPClient(userID, saved.Name, *catalogClient); err != nil {
			writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	closePersonalMCPConnection(userID, saved.Name)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"name": saved.Name, "oauth": saved.OAuth != nil})
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
	if dir, err := personalMCPDir(userID); err == nil {
		_ = os.Remove(personalMCPClientFile(dir, userID, name))
	}
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
	name := mux.Vars(r)["name"]
	internal, cfg, err := personalMCPServerConfig(userID, name)
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, err.Error())
		return
	}
	if cfg.OAuth == nil {
		writeAgentProfileError(w, http.StatusBadRequest, "this server does not use sign-in")
		return
	}
	redirectURI := deriveOAuthRedirectURI(r)
	cfg.OAuth.RedirectURL = redirectURI
	dir, err := personalMCPDir(userID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if clientID := strings.TrimSpace(body.ClientID); clientID != "" {
		// A client the person registered on the provider (Google, GitHub):
		// kept sealed, so refreshes after this sign-in keep working.
		client := registeredClient{ClientID: clientID, ClientSecret: strings.TrimSpace(body.ClientSecret), RedirectURI: redirectURI}
		if err := writePersonalMCPClient(userID, name, client); err != nil {
			writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
			return
		}
		cfg.OAuth.ClientID, cfg.OAuth.ClientSecret = client.ClientID, client.ClientSecret
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
		OnSuccess:  func(*OAuthFlowState) { closePersonalMCPConnection(userID, name) },
	})
	if err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	if discovery != nil {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"status": discovery.Status, "message": discovery.Message, "redirect_uri": discovery.RedirectURI})
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"auth_url": start.AuthURL})
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
