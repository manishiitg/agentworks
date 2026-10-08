package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

const mcpOAuthProtectedResourcePath = "/.well-known/oauth-protected-resource/api/external/v1/mcp"
const mcpOAuthMetadataPath = "/.well-known/oauth-authorization-server"
const mcpOAuthAuthorizePath = "/api/oauth/mcp/authorize"
const mcpOAuthTokenPath = "/api/oauth/mcp/token"
const mcpOAuthRegisterPath = "/api/oauth/mcp/register"
const mcpOAuthConsentPath = "/api/oauth/mcp/consent"
const mcpOAuthConnectionsPath = "/api/oauth/mcp/connections"

// code:review is inert for anyone but an admin or Code reviewer, and
// users:manage for anyone but an admin: the tools re-check the account on
// every call, and consent never offers them to anyone else.
var mcpOAuthDefaultScopes = []string{"workflows:read", "files:read", "runs:execute", "crews:read", "crews:run", "crews:write", "code:review", "vault:manage", "users:manage"}

// Builder is supported only when explicitly requested, never by default.
var mcpOAuthScopes = append(slices.Clone(mcpOAuthDefaultScopes), "builder:chat", "relays:write", "knowledgebase:read", "knowledgebase:write", "files:write")

// The resource identifier is fixed by server configuration, never Host or
// X-Forwarded-Host from an unauthenticated request.
func mcpOAuthURLs() (origin, resource string, ok bool) {
	origin = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" ||
		(u.Scheme != "https" && (u.Scheme != "http" || !cliOAuthLoopback(u.Hostname()))) {
		return "", "", false
	}
	return origin, origin + externalMCPPath, true
}

func mcpOAuthChallenge(w http.ResponseWriter) {
	origin, _, ok := mcpOAuthURLs()
	if ok {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+origin+mcpOAuthProtectedResourcePath+`", scope="`+strings.Join(mcpOAuthDefaultScopes, " ")+`"`)
	}
	w.Header().Set("Cache-Control", "no-store")
}

func (api *StreamingAPI) handleMCPOAuthProtectedResource(w http.ResponseWriter, r *http.Request) {
	origin, resource, ok := mcpOAuthURLs()
	if !ok {
		http.Error(w, "MCP OAuth requires a public HTTPS URL or configured loopback URL", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource": resource, "authorization_servers": []string{origin},
		"scopes_supported": mcpOAuthScopes,
	})
}

func (api *StreamingAPI) handleMCPOAuthMetadata(w http.ResponseWriter, r *http.Request) {
	origin, _, ok := mcpOAuthURLs()
	if !ok {
		http.Error(w, "MCP OAuth requires a public HTTPS URL or configured loopback URL", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": origin, "authorization_endpoint": origin + mcpOAuthAuthorizePath,
		"token_endpoint": origin + mcpOAuthTokenPath, "registration_endpoint": origin + mcpOAuthRegisterPath,
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported": mcpOAuthScopes,
	})
}

func validMCPOAuthRedirect(raw string) bool {
	if raw == "" || len(raw) > 1024 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")
}

func (api *StreamingAPI) handleMCPOAuthRegister(w http.ResponseWriter, r *http.Request) {
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
		mcpOAuthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	for _, redirect := range input.RedirectURIs {
		if !validMCPOAuthRedirect(redirect) {
			mcpOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri")
			return
		}
	}
	if len(input.GrantTypes) > 0 && (!slices.Contains(input.GrantTypes, "authorization_code") || len(input.GrantTypes) > 2 || len(input.GrantTypes) == 2 && !slices.Contains(input.GrantTypes, "refresh_token")) || len(input.Response) > 0 && (len(input.Response) != 1 || input.Response[0] != "code") {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	client, err := store.RegisterClient(r.Context(), strings.TrimSpace(input.Name), input.RedirectURIs)
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"client_id": client.ID, "client_name": client.Name, "redirect_uris": client.RedirectURIs, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
}

// mcpOAuthScopesFor is what this user may actually consent to: code:review
// only for an admin or Code reviewer. Everyone else neither sees nor grants it
// (the tools re-check the account on every call regardless).
func mcpOAuthScopesFor(user *UserClaims, scopes []string) []string {
	canReview, builderOn := claimsCanReviewCode(user), externalBuilderEnabled()
	// Relay authoring needs the server switch AND an account that may edit and has the Relays product (the same rule
	// validateMCPOAuthBuilderSelection enforces); Builder editing needs the server switch. A connection never asks for,
	// shows or grants what the server or the account would refuse: a client that requests every advertised scope used
	// to get a 404 on Allow (Excellence 2026-10-05).
	canRelays := builderOn && userAccessForClaims(user).CanEdit && userAllowedProduct(user, "relays")
	return slices.DeleteFunc(slices.Clone(scopes), func(scope string) bool {
		switch scope {
		case "knowledgebase:read", "knowledgebase:write":
			return !knowledgebaseMCPAllowed(user)
		case "vault:manage":
			return user == nil || !vaultAdminActive(user.UserID)
		case "code:review":
			return !canReview
		case "users:manage":
			return !claimsIsAdmin(user)
		case "files:write":
			return user == nil || !userAccessForClaims(user).CanEdit
		case "builder:chat":
			return !builderOn
		case "relays:write":
			return !canRelays
		}
		return false
	})
}

func validMCPOAuthScopes(raw string) ([]string, bool) {
	scopes := strings.Fields(raw)
	if len(scopes) == 0 {
		scopes = append([]string(nil), mcpOAuthDefaultScopes...)
	}
	if len(scopes) > len(mcpOAuthScopes) {
		return nil, false
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if !slices.Contains(mcpOAuthScopes, scope) || seen[scope] {
			return nil, false
		}
		seen[scope] = true
	}
	if seen["files:write"] && (!seen["workflows:read"] || !seen["files:read"]) {
		return nil, false
	}
	if (seen["builder:chat"] || seen["relays:write"]) && (!seen["workflows:read"] || !seen["files:read"] || !seen["runs:execute"]) {
		return nil, false
	}
	if seen["knowledgebase:write"] && !seen["knowledgebase:read"] {
		return nil, false
	}
	return scopes, true
}

func (api *StreamingAPI) handleMCPOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, resource, ok := mcpOAuthURLs()
	if !ok {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	q := r.URL.Query()
	clientID, redirect := q.Get("client_id"), q.Get("redirect_uri")
	state, challenge := q.Get("state"), q.Get("code_challenge")
	if len(state) == 0 || len(state) > 512 || len(challenge) != 43 || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("resource") != resource {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	for _, c := range challenge {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	scopes, valid := validMCPOAuthScopes(q.Get("scope"))
	if !valid {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_scope")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	client, err := store.Client(r.Context(), clientID)
	if err != nil || !slices.Contains(client.RedirectURIs, redirect) {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_client")
		return
	}
	id, err := store.SaveRequest(r.Context(), mcpOAuthRequest{ClientID: clientID, RedirectURI: redirect, Resource: resource, State: state, Scopes: scopes, Challenge: challenge, ExpiresAt: time.Now().Add(10 * time.Minute)})
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	http.Redirect(w, r, "/oauth/consent?request="+url.QueryEscape(id), http.StatusSeeOther)
}

// builderConsentWorkflows applies the same live account and workflow ACL as
// workflow discovery. Selection is revalidated when consent is submitted.
func validateMCPOAuthBuilderSelection(ctx context.Context, claims *UserClaims, scopes, ids []string) error {
	if slices.Contains(scopes, "relays:write") && (!externalBuilderEnabled() || !userAccessForClaims(claims).CanEdit || !userAllowedProduct(claims, "relays")) {
		return errors.New("Relay authoring is not available to this account or deployment")
	}
	if !slices.Contains(scopes, "builder:chat") {
		if len(ids) != 0 {
			return errors.New("workflow selection requires explicitly requested Builder permission")
		}
		return nil
	}
	// Builder follows the account: no workflow selection. What the connection may edit is decided live, per call, by
	// the account's role and its access to each workflow.
	if len(ids) != 0 {
		return errors.New("Builder follows your own permissions; a workflow selection is not used")
	}
	token := accesstokens.Token{Scopes: scopes, AllWorkflows: true}
	if !token.BuilderAccess() {
		return errors.New("Builder requires companion read/run permissions")
	}
	return nil
}

func (api *StreamingAPI) handleMCPOAuthConsent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" {
		mcpOAuthError(w, http.StatusForbidden, "access_denied")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	id := r.URL.Query().Get("request")
	if len(id) < 20 || len(id) > 100 {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Method == http.MethodGet {
		request, err := store.Request(r.Context(), id)
		if err != nil {
			mcpOAuthError(w, http.StatusNotFound, "invalid_request")
			return
		}
		client, err := store.Client(r.Context(), request.ClientID)
		if err != nil {
			mcpOAuthError(w, http.StatusNotFound, "invalid_client")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"client_name": client.Name, "redirect_uri": request.RedirectURI, "scopes": mcpOAuthScopesFor(claims, request.Scopes), "editable_workflows": []map[string]string{}})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	var input struct {
		Decision    string   `json:"decision"`
		WorkflowIDs []string `json:"workflow_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Decision != "approve" && input.Decision != "deny" {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	request, code, err := store.Decide(r.Context(), id, claims, input.Decision == "approve", input.WorkflowIDs)
	if err != nil {
		var refused *mcpOAuthRefusal
		if errors.As(err, &refused) {
			// The request is still open: say why it was refused instead of a bare "not found".
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request", "error_description": refused.Error()})
			return
		}
		mcpOAuthError(w, http.StatusNotFound, "invalid_request")
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

func mcpOAuthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (api *StreamingAPI) handleMCPOAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, resource, ok := mcpOAuthURLs()
	if !ok || r.PostForm.Get("resource") != resource {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	clientID := r.PostForm.Get("client_id")
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	if _, err = store.Client(r.Context(), clientID); err != nil {
		mcpOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	var grant mcpOAuthGrant
	var access, refresh string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		grant, access, refresh, err = store.ExchangeCode(r.Context(), r.PostForm.Get("code"), clientID, r.PostForm.Get("redirect_uri"), resource, r.PostForm.Get("code_verifier"))
	case "refresh_token":
		grant, access, refresh, err = store.Refresh(r.Context(), r.PostForm.Get("refresh_token"), clientID, resource)
	default:
		mcpOAuthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": strings.Join(grant.Scopes, " ")})
}

func authenticateMCPOAuthToken(w http.ResponseWriter, r *http.Request, raw string) (*UserClaims, bool) {
	if r.URL.Path != externalMCPPath || r.Header.Get("Authorization") == "" {
		mcpOAuthError(w, http.StatusForbidden, "invalid_target")
		return nil, false
	}
	_, resource, ok := mcpOAuthURLs()
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
	if err != nil || grant.Resource != resource || len(grant.Scopes) == 0 {
		mcpOAuthChallenge(w)
		mcpOAuthError(w, http.StatusUnauthorized, "invalid_token")
		return nil, false
	}
	claims, err := accessTokenClaims(mcpOAuthTokenForGrant(grant))
	if err != nil {
		mcpOAuthChallenge(w)
		mcpOAuthError(w, http.StatusUnauthorized, "invalid_token")
		return nil, false
	}
	return claims, true
}

func mcpOAuthFamilyActive(ctx context.Context, family string) (mcpOAuthGrant, error) {
	store, err := openMCPOAuthStore()
	if err != nil {
		return mcpOAuthGrant{}, err
	}
	defer store.Close()
	return store.ActiveFamily(ctx, family)
}

func (api *StreamingAPI) handleMCPOAuthConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" {
		mcpOAuthError(w, http.StatusForbidden, "access_denied")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	defer store.Close()
	if r.Method == http.MethodDelete {
		id := strings.TrimPrefix(r.URL.Path, mcpOAuthConnectionsPath+"/")
		if id == "" || strings.Contains(id, "/") {
			mcpOAuthError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := store.RevokeFamily(r.Context(), id, claims.UserID); err != nil {
			mcpOAuthError(w, http.StatusNotFound, "not_found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	connections, err := store.Connections(r.Context(), claims.UserID)
	if err != nil {
		mcpOAuthError(w, http.StatusServiceUnavailable, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"connections": connections})
}

// Keep the generic read-and-run scope check identical for PAT and OAuth grants.
func mcpOAuthTokenForGrant(grant mcpOAuthGrant) accesstokens.Token {
	name := "MCP OAuth"
	if grant.ClientID == cliOAuthClientID {
		name = "AgentWorks CLI"
	}
	// A grant with no workflow IDs reaches every workflow the account may use (Builder included, bounded by the
	// account's live role); an older Builder grant keeps the IDs it was given.
	allCrews := slices.Contains(grant.Scopes, "crews:read") || slices.Contains(grant.Scopes, "crews:run") || slices.Contains(grant.Scopes, "crews:write")
	return accesstokens.Token{ID: "oauth-" + grant.FamilyID, Name: name, UserID: grant.UserID, Username: grant.Username, Email: grant.Email, Provider: grant.Provider, Scopes: grant.Scopes, WorkflowIDs: slices.Clone(grant.WorkflowIDs), AllWorkflows: len(grant.WorkflowIDs) == 0, AllCrews: allCrews, ExpiresAt: grant.Expires}
}
