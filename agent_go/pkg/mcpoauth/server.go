package mcpoauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Server serves one authorization server deployment. Hosts mount each
// Handle* method on their own router paths.
type Server struct {
	cfg Config
}

// NewServer builds the AS. Config.OpenStore and Config.CurrentUser must be set.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// URLs returns the deployment origin and protected resource identifier.
func (s *Server) URLs() (origin, resource string, ok bool) {
	return OriginResource(s.cfg.PublicURL, s.cfg.ResourcePath)
}

// Challenge writes the WWW-Authenticate header pointing at this deployment.
func (s *Server) Challenge(w http.ResponseWriter) {
	origin, _, ok := s.URLs()
	if ok {
		w.Header().Set("WWW-Authenticate", `Bearer [REDACTED]"`+origin+s.cfg.ProtectedResourcePath+`", scope="`+strings.Join(s.cfg.Scopes, " ")+`"`)
	}
	w.Header().Set("Cache-Control", "no-store")
}

func oauthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// HandleProtectedResource serves RFC 9728 protected-resource metadata.
func (s *Server) HandleProtectedResource(w http.ResponseWriter, r *http.Request) {
	origin, resource, ok := s.URLs()
	if !ok {
		http.Error(w, "OAuth requires a public HTTPS URL or configured loopback URL", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource": resource, "authorization_servers": []string{origin},
		"scopes_supported": s.cfg.Scopes,
	})
}

// HandleMetadata serves RFC 8414 authorization-server metadata.
func (s *Server) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	origin, _, ok := s.URLs()
	if !ok {
		http.Error(w, "OAuth requires a public HTTPS URL or configured loopback URL", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": origin, "authorization_endpoint": origin + s.cfg.AuthorizePath,
		"token_endpoint": origin + s.cfg.TokenPath, "registration_endpoint": origin + s.cfg.RegisterPath,
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported": s.cfg.Scopes,
	})
}

// HandleRegister serves RFC 7591 dynamic client registration.
func (s *Server) HandleRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input struct {
		Name         string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		Response     []string `json:"response_types"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(strings.TrimSpace(input.Name)) == 0 || len(input.Name) > 128 || len(input.RedirectURIs) == 0 || len(input.RedirectURIs) > 5 || input.AuthMethod != "" && input.AuthMethod != "none" {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	for _, redirect := range input.RedirectURIs {
		if !ValidRedirect(redirect) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri")
			return
		}
	}
	if len(input.GrantTypes) > 0 && (!slices.Contains(input.GrantTypes, "authorization_code") || len(input.GrantTypes) > 2 || len(input.GrantTypes) == 2 && !slices.Contains(input.GrantTypes, "refresh_token")) || len(input.Response) > 0 && (len(input.Response) != 1 || input.Response[0] != "code") {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	client, err := store.RegisterClient(r.Context(), strings.TrimSpace(input.Name), input.RedirectURIs)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"client_id": client.ID, "client_name": client.Name, "redirect_uris": client.RedirectURIs, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
}

func (s *Server) validScopes(raw string) ([]string, bool) {
	scopes := strings.Fields(raw)
	if len(scopes) == 0 {
		scopes = append([]string(nil), s.cfg.Scopes...)
	}
	if len(scopes) > len(s.cfg.Scopes) {
		return nil, false
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if !slices.Contains(s.cfg.Scopes, scope) || seen[scope] {
			return nil, false
		}
		seen[scope] = true
	}
	return scopes, true
}

// HandleAuthorize validates the request and redirects to the consent UI.
func (s *Server) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, resource, ok := s.URLs()
	if !ok {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	q := r.URL.Query()
	clientID, redirect := q.Get("client_id"), q.Get("redirect_uri")
	state, challenge := q.Get("state"), q.Get("code_challenge")
	if len(state) == 0 || len(state) > 512 || len(challenge) != 43 || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("resource") != resource {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	for _, c := range challenge {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			oauthError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	scopes, valid := s.validScopes(q.Get("scope"))
	if !valid {
		oauthError(w, http.StatusBadRequest, "invalid_scope")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	client, err := store.Client(r.Context(), clientID)
	if err != nil || !slices.Contains(client.RedirectURIs, redirect) {
		oauthError(w, http.StatusBadRequest, "invalid_client")
		return
	}
	id, err := store.SaveRequest(r.Context(), AuthRequest{ClientID: clientID, RedirectURI: redirect, Resource: resource, State: state, Scopes: scopes, Challenge: challenge, ExpiresUnix: time.Now().Add(10 * time.Minute).Unix()})
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	http.Redirect(w, r, s.cfg.ConsentUIPath+"?request="+url.QueryEscape(id), http.StatusSeeOther)
}

// HandleConsent is the JSON consent API: GET describes the pending request,
// POST approves/denies it and returns the client redirect. The caller must be
// the logged-in human (Config.CurrentUser); token-bearing callers are denied.
func (s *Server) HandleConsent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := s.cfg.CurrentUser(r)
	if !ok {
		oauthError(w, http.StatusForbidden, "access_denied")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	id := r.URL.Query().Get("request")
	if len(id) < 20 || len(id) > 100 {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Method == http.MethodGet {
		request, err := store.Request(r.Context(), id)
		if err != nil {
			oauthError(w, http.StatusNotFound, "invalid_request")
			return
		}
		client, err := store.Client(r.Context(), request.ClientID)
		if err != nil {
			oauthError(w, http.StatusNotFound, "invalid_client")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"client_name": client.Name, "redirect_uri": request.RedirectURI, "scopes": request.Scopes})
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
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Decision != "approve" && input.Decision != "deny" {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	request, code, err := store.Decide(r.Context(), id, *user, input.Decision == "approve")
	if err != nil {
		oauthError(w, http.StatusNotFound, "invalid_request")
		return
	}
	redirect, _ := url.Parse(request.RedirectURI)
	q := redirect.Query()
	if input.Decision == "approve" {
		q.Set("code", code)
	} else {
		q.Set("error", "access_denied")
	}
	q.Set("state", request.State)
	redirect.RawQuery = q.Encode()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"redirect_url": redirect.String()})
}

// HandleToken exchanges authorization codes and rotates refresh tokens.
func (s *Server) HandleToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, resource, ok := s.URLs()
	if !ok || r.PostForm.Get("resource") != resource {
		oauthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	clientID := r.PostForm.Get("client_id")
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	if _, err = store.Client(r.Context(), clientID); err != nil {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	var grant Grant
	var access, refresh string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		grant, access, refresh, err = store.ExchangeCode(r.Context(), r.PostForm.Get("code"), clientID, r.PostForm.Get("redirect_uri"), resource, r.PostForm.Get("code_verifier"))
	case "refresh_token":
		grant, access, refresh, err = store.Refresh(r.Context(), r.PostForm.Get("refresh_token"), clientID, resource)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": strings.Join(grant.Scopes, " ")})
}

// HandleConnections lists the human's grant families (GET). Hosts wire
// revocation separately via RevokeConnection.
func (s *Server) HandleConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := s.cfg.CurrentUser(r)
	if !ok {
		oauthError(w, http.StatusForbidden, "access_denied")
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	connections, err := store.Connections(r.Context(), user.ID)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"connections": connections})
}

// RevokeConnection revokes one of the human's grant families.
func (s *Server) RevokeConnection(w http.ResponseWriter, r *http.Request, familyID string) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := s.cfg.CurrentUser(r)
	if !ok {
		oauthError(w, http.StatusForbidden, "access_denied")
		return
	}
	if familyID == "" || strings.Contains(familyID, "/") {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	if err := store.RevokeFamily(r.Context(), familyID, user.ID); err != nil {
		oauthError(w, http.StatusNotFound, "not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AuthenticateRequest validates a bearer token against the resource. It
// returns the grant; hosts map it to their own claims type.
func (s *Server) AuthenticateRequest(ctx context.Context, raw string) (Grant, error) {
	_, resource, ok := s.URLs()
	if !ok {
		return Grant{}, errors.New("oauth server misconfigured")
	}
	store, err := s.cfg.OpenStore()
	if err != nil {
		return Grant{}, err
	}
	defer store.Close()
	grant, err := store.Authenticate(ctx, raw)
	if err != nil {
		return Grant{}, err
	}
	if grant.Resource != resource || len(grant.Scopes) == 0 {
		return Grant{}, errors.New("invalid token")
	}
	return grant, nil
}
