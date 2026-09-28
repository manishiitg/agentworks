package mcpoauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// CLIURLs resolves the device-flow origin and resource. Without a configured
// public origin, loopback HTTP is admitted for local sign-in. A public origin
// is never derived from the untrusted Host header.
func (s *Server) CLIURLs(r *http.Request) (origin, resource string, ok bool) {
	return s.cliURLs(r)
}

func (s *Server) cliURLs(r *http.Request) (origin, resource string, ok bool) {
	origin, _, ok = OriginResource(s.cfg.PublicURL, s.cfg.ResourcePath)
	if !ok {
		configured := strings.TrimRight(strings.TrimSpace(s.cfg.PublicURL), "/")
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
		if !IsLoopback(u.Hostname()) {
			return "", "", false
		}
		origin = candidate
	}
	return origin, origin + s.cfg.CLIResourcePath, true
}

func (s *Server) cliBrowserOrigin(serverOrigin string) string {
	if s.cfg.CLIBrowserOrigin == nil {
		return serverOrigin
	}
	return s.cfg.CLIBrowserOrigin(serverOrigin)
}

// HandleDeviceCreate starts a device-flow approval.
func (s *Server) HandleDeviceCreate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	origin, _, ok := s.cliURLs(r)
	if !ok {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	device, verify, err := store.CreateCLIDevice(r.Context())
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	browserOrigin := s.cliBrowserOrigin(origin)
	_ = json.NewEncoder(w).Encode(map[string]any{"device_code": device, "verification_uri": browserOrigin + s.cfg.CLIBrowserPath, "verification_uri_complete": browserOrigin + s.cfg.CLIBrowserPath + "?code=" + verify, "user_code": strings.ToUpper(verify[len("cli_verify_") : len("cli_verify_")+8]), "expires_in": 600, "interval": 3})
}

// HandleDeviceConsent is the JSON device-approval API for the logged-in human.
func (s *Server) HandleDeviceConsent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := s.cfg.CurrentUser(r)
	if !ok {
		oauthError(w, http.StatusForbidden, "access_denied")
		return
	}
	code := r.URL.Query().Get("code")
	if !strings.HasPrefix(code, "cli_verify_") || len(code) != 75 {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	if r.Method == http.MethodGet {
		scopes, err := store.CLIDeviceRequest(r.Context(), code)
		if err != nil {
			oauthError(w, http.StatusNotFound, "invalid_request")
			return
		}
		name := s.cfg.CLIClientName
		if name == "" {
			name = "CLI"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"client_name": name, "scopes": s.consentScopes(r, scopes), "user_code": strings.ToUpper(code[len("cli_verify_") : len("cli_verify_")+8])})
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
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	requestedScopes, err := store.CLIDeviceRequest(r.Context(), code)
	if err != nil {
		oauthError(w, http.StatusNotFound, "invalid_request")
		return
	}
	allowedScopes := s.consentScopes(r, requestedScopes)
	if input.Decision == "approve" && len(allowedScopes) == 0 {
		oauthError(w, http.StatusForbidden, "access_denied")
		return
	}
	if err := store.DecideCLIDevice(r.Context(), code, *user, input.Decision == "approve", allowedScopes); err != nil {
		oauthError(w, http.StatusNotFound, "invalid_request")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": input.Decision})
}

// HandleDeviceToken polls device approval and rotates CLI refresh tokens.
func (s *Server) HandleDeviceToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, resource, ok := s.cliURLs(r)
	if !ok || r.PostForm.Get("resource") != resource || r.PostForm.Get("client_id") != s.cfg.CLIClientID {
		oauthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	var grant Grant
	var access, refresh string
	switch r.PostForm.Get("grant_type") {
	case "urn:ietf:params:oauth:grant-type:device_code":
		grant, access, refresh, err = store.PollCLIDevice(r.Context(), r.PostForm.Get("device_code"), resource)
	case "refresh_token":
		grant, access, refresh, err = store.Refresh(r.Context(), r.PostForm.Get("refresh_token"), s.cfg.CLIClientID, resource)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		code := "invalid_grant"
		switch {
		case errors.Is(err, ErrPending):
			code = "authorization_pending"
		case errors.Is(err, ErrSlowDown):
			code = "slow_down"
		case errors.Is(err, ErrDenied):
			code = "access_denied"
		case errors.Is(err, sql.ErrNoRows):
			code = "expired_token"
		}
		oauthError(w, http.StatusBadRequest, code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": strings.Join(grant.Scopes, " ")})
}

// HandleDeviceRevoke revokes the family holding a CLI refresh token.
func (s *Server) HandleDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	_ = store.RevokeByRefresh(r.Context(), r.PostForm.Get("token"))
	w.WriteHeader(http.StatusOK)
}

// AuthenticateCLI validates a CLI bearer token against the CLI resource.
func (s *Server) AuthenticateCLI(ctx context.Context, r *http.Request, raw string) (Grant, error) {
	_, resource, ok := s.cliURLs(r)
	if !ok {
		return Grant{}, errors.New("oauth server misconfigured")
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		return Grant{}, err
	}
	defer store.Close()
	grant, err := store.Authenticate(ctx, raw)
	if err != nil || grant.Resource != resource || grant.ClientID != s.cfg.CLIClientID || len(grant.Scopes) == 0 {
		return Grant{}, errors.New("invalid token")
	}
	return grant, nil
}
