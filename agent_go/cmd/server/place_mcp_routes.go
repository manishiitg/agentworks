package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"
)

// /api/me/mcp/* and /api/me/secrets/*: a person manages their own MCP
// servers, their per-Code switches and their personal secrets
// (docs/design/code_private_mcp.md). Every route acts on the caller's own
// store only; nothing here can read or change another person's.

func placeMCPUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	if userID == "" {
		writeAgentProfileError(w, http.StatusUnauthorized, "sign in to manage your MCP servers")
		return "", false
	}
	return userID, true
}

// placeMCPCodeRoot resolves a Code the caller can chat in (any role) to
// its root.
// redactedURL drops the query string and fragment, which can carry tokens.
func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String()
}

// addPlaceMCP adds (or replaces) one of the person's servers: a catalog
// server by name, or their own URL. The HTTP route and the Code agent's
// manage_my_mcp_servers tool both use it. The int is the HTTP status of an error.
func (api *StreamingAPI) addPlaceMCP(ctx context.Context, userID string, body placeMCPServer, catalog string) (placeMCPServer, int, error) {
	body.OAuth, body.Catalog, body.AppKey = nil, "", ""
	body.SlackUserID, body.SlackTeamID = "", "" // provider identity is recorded only after OAuth
	var catalogClient *registeredClient
	if strings.TrimSpace(catalog) != "" {
		entry, ok := api.placeMCPCatalogEntry(catalog)
		if !ok {
			return placeMCPServer{}, http.StatusBadRequest, fmt.Errorf("%q is not a remote server in the catalog", catalog)
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
			// A provider without registration signs in through the
			// deployment's app, when an admin has set one up.
			if catalogClient == nil && catalogOAuth.ClientID == "" && catalogOAuth.RegistrationEndpoint == "" {
				body.AppKey = api.mcpAppKeyOf(entry.Catalog)
			}
			catalogOAuth.ClientID, catalogOAuth.ClientSecret, catalogOAuth.RedirectURL, catalogOAuth.UsePKCE = "", "", "", true
			body.OAuth = &catalogOAuth
		}
	}
	if err := validatePlaceMCPServer(&body); err != nil {
		return placeMCPServer{}, http.StatusBadRequest, err
	}
	if body.OAuth != nil {
		for _, endpoint := range []string{body.OAuth.AuthURL, body.OAuth.TokenURL} {
			if err := netguard.CheckURL(endpoint, true); err != nil {
				return placeMCPServer{}, http.StatusBadRequest, fmt.Errorf("the server's sign-in endpoint was refused: %w", err)
			}
		}
	} else if len(body.Headers) == 0 && catalog == "" {
		probe, err := services.ProbeMCPServerAuthWith(ctx, netguard.Client(15*time.Second), body.URL)
		if err != nil {
			return placeMCPServer{}, http.StatusBadRequest, fmt.Errorf("could not check %s: %w", redactedURL(body.URL), err)
		}
		if !probe.NoAuthRequired && probe.Endpoints != nil {
			body.OAuth = &oauth.OAuthConfig{
				AuthURL: probe.Endpoints.AuthURL, TokenURL: probe.Endpoints.TokenURL,
				RegistrationEndpoint: probe.Endpoints.RegistrationEndpoint, Resource: probe.Endpoints.Resource,
				Scopes: probe.Endpoints.ScopesSupported, UsePKCE: true,
			}
			for _, endpoint := range []string{body.OAuth.AuthURL, body.OAuth.TokenURL} {
				if err := netguard.CheckURL(endpoint, true); err != nil {
					return placeMCPServer{}, http.StatusBadRequest, fmt.Errorf("the server's sign-in endpoint was refused: %w", err)
				}
			}
		}
	}
	// Adding over an existing name starts clean: the old sign-in client and
	// login belong to whatever that name pointed at before.
	if err := forgetPlaceMCPLogin(userID, body.Name); err != nil {
		return placeMCPServer{}, http.StatusInternalServerError, err
	}
	saved, err := addPlaceMCPServer(userID, body)
	if err != nil {
		return placeMCPServer{}, http.StatusBadRequest, err
	}
	if catalogClient != nil {
		if err := writePlaceMCPClient(userID, saved.Name, *catalogClient); err != nil {
			return placeMCPServer{}, http.StatusInternalServerError, err
		}
	}
	closePlaceMCPConnection(userID, saved.Name)
	return saved, http.StatusOK, nil
}

func placeMCPClientFile(dir, userID, name string) string {
	return filepath.Join(dir, "clients", placeMCPInternalName(userID, name)+".json")
}

// closePlaceMCPConnection drops a person's pooled connection to one
// server, so the next call reconnects with its current config and login.
func closePlaceMCPConnection(userID, name string) {
	mcpclient.GetSessionRegistry().CloseSessionServer("global", placeMCPInternalName(userID, name))
}

// placeMCPCatalogServer is a platform catalog server a person can add as
// their own: remote, public, and signed into per person (OAuth) or open. A
// server whose credentials are platform headers is not offered.
type placeMCPCatalogServer struct {
	Name        string `json:"name"`
	Catalog     string `json:"catalog"`
	Description string `json:"description,omitempty"`
	SignIn      bool   `json:"sign_in"`
	// NeedsClient: the provider has no dynamic registration and the catalog
	// carries no client, so the person enters their OAuth app's client.
	NeedsClient bool `json:"needs_client"`
	// Group is the sign-in app key the server shares (google, github, ...):
	// servers with the same group are offered together and share one login.
	Group  string `json:"group,omitempty"`
	config mcpclient.MCPServerConfig
}

// mcpCatalogHiddenKeys are providers whose servers are not offered as MCP connectors: Google
// (connected through the Google app and gog, not as MCP) and GitHub (a personal access token).
var mcpCatalogHiddenKeys = map[string]bool{"google": true, "github": true}

func (api *StreamingAPI) placeMCPCatalog() []placeMCPCatalogServer {
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil
	}
	out := []placeMCPCatalogServer{}
	keys := mcpAppKeyIndex(catalog.MCPServers)
	for name, cfg := range catalog.MCPServers {
		protocol := cfg.GetProtocol()
		if cfg.URL == "" || len(cfg.Headers) > 0 || (protocol != mcpclient.ProtocolHTTP && protocol != mcpclient.ProtocolSSE) {
			continue
		}
		if netguard.CheckURL(cfg.URL, true) != nil {
			continue
		}
		// Google and GitHub are not offered as MCP connectors (owner decision 2026-09-30).
		// Google apps (Gmail, Drive, Calendar, Docs, Sheets, Slides) are connected through the
		// gog integration (the Gmail tab): Google's Workspace MCP servers are a Developer Preview
		// that needs Google-side enrollment, while gog works with a normal OAuth client and keeps
		// the token on the server. GitHub is a personal access token used with git and the API.
		// An existing connection to either keeps working; new ones are not offered.
		if mcpCatalogHiddenKeys[keys[name]] {
			continue
		}
		local := strings.Trim(placeMCPCatalogNameCleaner.ReplaceAllString(strings.ToLower(name), "_"), "_")
		if len(local) > 40 {
			local = local[:40]
		}
		if !placeMCPNamePattern.MatchString(local) {
			continue
		}
		entry := placeMCPCatalogServer{Name: local, Catalog: name, Description: cfg.Description, SignIn: cfg.OAuth != nil, Group: keys[name], config: cfg}
		if cfg.OAuth != nil {
			entry.NeedsClient = cfg.OAuth.ClientID == "" && cfg.OAuth.RegistrationEndpoint == ""
			if entry.NeedsClient {
				// An app the admin set up for the provider is enough.
				if key := keys[name]; key != "" {
					if app, err := readMCPApp(key); err == nil && app != nil {
						entry.NeedsClient = false
					}
				}
			}
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Catalog < out[j].Catalog })
	return out
}

var placeMCPCatalogNameCleaner = regexp.MustCompile(`[^a-z0-9_]+`)

func (api *StreamingAPI) placeMCPCatalogEntry(name string) (placeMCPCatalogServer, bool) {
	name = strings.TrimSpace(name)
	entries := api.placeMCPCatalog()
	for _, entry := range entries {
		if entry.Catalog == name {
			return entry, true
		}
	}
	var match placeMCPCatalogServer
	found := false
	for _, entry := range entries {
		if strings.EqualFold(entry.Catalog, name) || strings.EqualFold(entry.Name, name) {
			if found { // Sanitized/case-folded provider aliases must be unique.
				return placeMCPCatalogServer{}, false
			}
			match, found = entry, true
		}
	}
	return match, found
}

// GET /api/me/mcp/catalog: the catalog servers a person can add as their own.
func (api *StreamingAPI) handlePlaceMCPCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := placeMCPUser(w, r); !ok {
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"servers": api.placeMCPCatalog()})
}

// forgetPlaceMCPLogin removes a server's sign-in client and login.
func forgetPlaceMCPLogin(userID, name string) error {
	dir, err := placeMCPDir(userID)
	if err != nil {
		return err
	}
	for _, file := range []string{placeMCPClientFile(dir, userID, name), placeMCPTokenFile(dir, userID, name)} {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// A group login nobody else uses goes with its last server.
	return forgetUnusedPlaceMCPGroups(dir, userID, name)
}
