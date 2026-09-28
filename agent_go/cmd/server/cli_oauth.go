package server

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/mcpoauth"
)

const cliOAuthDevicePath = "/api/oauth/cli/device"
const cliOAuthConsentPath = "/api/oauth/cli/consent"
const cliOAuthTokenPath = "/api/oauth/cli/token"
const cliOAuthRevokePath = "/api/oauth/cli/revoke"
const cliOAuthBrowserPath = "/oauth/cli"

func cliOAuthLoopback(host string) bool {
	return mcpoauth.IsLoopback(host)
}

func cliOAuthBrowserOrigin(serverOrigin string) string {
	// Local development uses the current Vite app for approval; the API server
	// may serve an older static build. Never use this override for public URLs.
	serverURL, err := url.Parse(serverOrigin)
	if err != nil || serverURL.Scheme != "http" || !cliOAuthLoopback(serverURL.Hostname()) {
		return serverOrigin
	}
	browser := strings.TrimRight(strings.TrimSpace(os.Getenv("AGENTWORKS_CLI_BROWSER_URL")), "/")
	u, err := url.Parse(browser)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !cliOAuthLoopback(u.Hostname()) {
		return serverOrigin
	}
	return browser
}

func (api *StreamingAPI) handleCLIOAuthDevice(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleDeviceCreate(w, r)
}

func (api *StreamingAPI) handleCLIOAuthConsent(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleDeviceConsent(w, r)
}

func (api *StreamingAPI) handleCLIOAuthToken(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleDeviceToken(w, r)
}

func (api *StreamingAPI) handleCLIOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleDeviceRevoke(w, r)
}

func authenticateCLIOAuthToken(w http.ResponseWriter, r *http.Request, raw string) (*UserClaims, bool) {
	if r.Header.Get("Authorization") == "" || !cliOAuthAllowedPath(r.Method, r.URL.Path) {
		mcpOAuthError(w, http.StatusForbidden, "invalid_target")
		return nil, false
	}
	_, resource, ok := mcpOAuthServer().CLIURLs(r)
	if !ok {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return nil, false
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return nil, false
	}
	defer store.Close()
	grant, err := store.Authenticate(r.Context(), raw)
	if err != nil || grant.Resource != resource || grant.ClientID != cliOAuthClientID || len(grant.Scopes) == 0 {
		mcpOAuthError(w, http.StatusUnauthorized, "invalid_token")
		return nil, false
	}
	claims, err := accessTokenClaims(mcpOAuthTokenForGrant(grant))
	if err != nil {
		mcpOAuthError(w, http.StatusUnauthorized, "invalid_token")
		return nil, false
	}
	return claims, true
}

func cliOAuthAllowedPath(method, path string) bool {
	return (method == http.MethodGet && path == "/api/external/v1/tools") || (method == http.MethodPost && path == "/api/external/v1/call") || ((method == http.MethodGet || method == http.MethodHead) && path == "/api/external/v1/files/content") || (method == http.MethodGet && (path == "/api/external/v1/skill.md" || path == "/api/external/v1/skill.zip" || path == "/api/external/v1/agentworks.plugin"))
}
