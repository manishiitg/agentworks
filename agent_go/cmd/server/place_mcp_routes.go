package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
		entry, ok := api.mcpCatalogEntry(catalog)
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

// mcpCatalogHiddenKeys are providers whose servers are not offered as MCP connectors: Google
// (connected through the Google app and gog, not as MCP) and GitHub (a personal access token).
var mcpCatalogHiddenKeys = map[string]bool{"google": true, "github": true}

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
