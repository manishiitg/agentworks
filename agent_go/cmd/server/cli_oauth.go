package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const cliOAuthDevicePath = "/api/oauth/cli/device"
const cliOAuthConsentPath = "/api/oauth/cli/consent"
const cliOAuthTokenPath = "/api/oauth/cli/token"
const cliOAuthRevokePath = "/api/oauth/cli/revoke"
const cliOAuthBrowserPath = "/oauth/cli"

func cliOAuthResource(r *http.Request) (origin, resource string, ok bool) {
	origin, _, ok = mcpOAuthURLs()
	if !ok {
		// A local install can use browser sign-in without configuring a public URL.
		// Never derive a public origin from the untrusted Host header.
		configured := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
		if configured == "" && r == nil {
			return "", "", false
		}
		candidate := configured
		if candidate == "" {
			candidate = "http://" + r.Host
		}
		u, err := url.Parse(candidate)
		if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", "", false
		}
		if !cliOAuthLoopback(u.Hostname()) {
			return "", "", false
		}
		origin = candidate
	}
	return origin, origin + "/api/external/v1", true
}

func cliOAuthLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	w.Header().Set("Cache-Control", "no-store")
	origin, _, ok := cliOAuthResource(r)
	if !ok {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	device, verify, err := store.CreateCLIDevice(r.Context())
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	browserOrigin := cliOAuthBrowserOrigin(origin)
	_ = json.NewEncoder(w).Encode(map[string]any{"device_code": device, "verification_uri": browserOrigin + cliOAuthBrowserPath, "verification_uri_complete": browserOrigin + cliOAuthBrowserPath + "?code=" + verify, "user_code": strings.ToUpper(verify[len("cli_verify_") : len("cli_verify_")+8]), "expires_in": 600, "interval": 3})
}

func (api *StreamingAPI) handleCLIOAuthConsent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" {
		mcpOAuthError(w, http.StatusForbidden, "access_denied")
		return
	}
	code := r.URL.Query().Get("code")
	if !strings.HasPrefix(code, "cli_verify_") || len(code) != 75 {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	if r.Method == http.MethodGet {
		scopes, err := store.CLIDeviceRequest(r.Context(), code)
		if err != nil {
			mcpOAuthError(w, http.StatusNotFound, "invalid_request")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"client_name": "AgentWorks CLI", "scopes": scopes, "user_code": strings.ToUpper(code[len("cli_verify_") : len("cli_verify_")+8])})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var input struct {
		Decision string `json:"decision"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Decision != "approve" && input.Decision != "deny") {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := store.DecideCLIDevice(r.Context(), code, claims, input.Decision == "approve"); err != nil {
		mcpOAuthError(w, http.StatusNotFound, "invalid_request")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": input.Decision})
}

func (api *StreamingAPI) handleCLIOAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, resource, ok := cliOAuthResource(r)
	if !ok || r.PostForm.Get("resource") != resource || r.PostForm.Get("client_id") != cliOAuthClientID {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	var grant mcpOAuthGrant
	var access, refresh string
	switch r.PostForm.Get("grant_type") {
	case "urn:ietf:params:oauth:grant-type:device_code":
		grant, access, refresh, err = store.PollCLIDevice(r.Context(), r.PostForm.Get("device_code"), resource)
	case "refresh_token":
		grant, access, refresh, err = store.Refresh(r.Context(), r.PostForm.Get("refresh_token"), cliOAuthClientID, resource)
	default:
		mcpOAuthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		code := "invalid_grant"
		switch {
		case errors.Is(err, errCLIOAuthPending):
			code = "authorization_pending"
		case errors.Is(err, errCLIOAuthSlowDown):
			code = "slow_down"
		case errors.Is(err, errCLIOAuthDenied):
			code = "access_denied"
		case errors.Is(err, sql.ErrNoRows):
			code = "expired_token"
		}
		mcpOAuthError(w, http.StatusBadRequest, code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": strings.Join(grant.Scopes, " ")})
}

func (api *StreamingAPI) handleCLIOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	_ = store.RevokeByRefresh(r.Context(), r.PostForm.Get("token"))
	w.WriteHeader(http.StatusOK)
}

func authenticateCLIOAuthToken(w http.ResponseWriter, r *http.Request, raw string) (*UserClaims, bool) {
	if r.Header.Get("Authorization") == "" || !cliOAuthAllowedPath(r.Method, r.URL.Path) {
		mcpOAuthError(w, http.StatusForbidden, "invalid_target")
		return nil, false
	}
	_, resource, ok := cliOAuthResource(r)
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
