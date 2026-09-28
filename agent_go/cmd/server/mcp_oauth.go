package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/mcpoauth"
)

const mcpOAuthProtectedResourcePath = "/.well-known/oauth-protected-resource/api/external/v1/mcp"
const mcpOAuthMetadataPath = "/.well-known/oauth-authorization-server"
const mcpOAuthAuthorizePath = "/api/oauth/mcp/authorize"
const mcpOAuthTokenPath = "/api/oauth/mcp/token"
const mcpOAuthRegisterPath = "/api/oauth/mcp/register"
const mcpOAuthConsentPath = "/api/oauth/mcp/consent"
const mcpOAuthConnectionsPath = "/api/oauth/mcp/connections"

const mcpOAuthAccessPrefix = "aw_mcp_"
const mcpOAuthRefreshPrefix = "aw_mcp_refresh_"
const cliOAuthAccessPrefix = "aw_cli_"
const cliOAuthRefreshPrefix = "aw_cli_refresh_"
const cliOAuthClientID = "agentworks-cli"

// code:review is issued only to an admin or Code reviewer. The tools also
// re-check that role on every call.
var mcpOAuthScopes = []string{"workflows:read", "files:read", "runs:execute", "crews:read", "crews:run", "crews:write", "code:review"}

func mcpOAuthScopesFor(user *UserClaims, scopes []string) []string {
	if claimsCanReviewCode(user) {
		return scopes
	}
	return slices.DeleteFunc(slices.Clone(scopes), func(scope string) bool { return scope == "code:review" })
}

// mcpOAuthConfig wires the shared authorization server to this deployment.
// PUBLIC_URL is read per call so tests and reloads see the current value.
func mcpOAuthConfig() mcpoauth.Config {
	return mcpoauth.Config{
		PublicURL:             os.Getenv("PUBLIC_URL"),
		ResourcePath:          externalMCPPath,
		Scopes:                mcpOAuthScopes,
		AccessPrefix:          mcpOAuthAccessPrefix,
		RefreshPrefix:         mcpOAuthRefreshPrefix,
		CLIClientID:           cliOAuthClientID,
		CLIClientName:         "AgentWorks CLI",
		CLIAccessPrefix:       cliOAuthAccessPrefix,
		CLIRefreshPrefix:      cliOAuthRefreshPrefix,
		CLIResourcePath:       "/api/external/v1",
		CLIDefaultScopes:      mcpOAuthScopes,
		ConsentUIPath:         "/oauth/consent",
		ProtectedResourcePath: mcpOAuthProtectedResourcePath,
		RegisterPath:          mcpOAuthRegisterPath,
		AuthorizePath:         mcpOAuthAuthorizePath,
		TokenPath:             mcpOAuthTokenPath,
		OpenStore:             openMCPOAuthStore,
		CurrentUser:           mcpOAuthCurrentUser,
		FilterScopes: func(r *http.Request, scopes []string) []string {
			return mcpOAuthScopesFor(GetUserFromContext(r.Context()), scopes)
		},
		CLIBrowserOrigin: cliOAuthBrowserOrigin,
		CLIBrowserPath:   cliOAuthBrowserPath,
	}
}

func mcpOAuthServer() *mcpoauth.Server {
	return mcpoauth.NewServer(mcpOAuthConfig())
}

// mcpOAuthCurrentUser resolves the logged-in human. Token-bearing callers
// (PAT sessions, scoped tokens) can never approve consent.
func mcpOAuthCurrentUser(r *http.Request) (*mcpoauth.User, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" {
		return nil, false
	}
	return &mcpoauth.User{ID: claims.UserID, Username: claims.Username, Email: claims.Email, Provider: claims.Provider}, true
}

// The resource identifier is fixed by server configuration, never Host or
// X-Forwarded-Host from an unauthenticated request.
func mcpOAuthURLs() (origin, resource string, ok bool) {
	return mcpoauth.OriginResource(os.Getenv("PUBLIC_URL"), externalMCPPath)
}

func mcpOAuthChallenge(w http.ResponseWriter) {
	mcpOAuthServer().Challenge(w)
}

func mcpOAuthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (api *StreamingAPI) handleMCPOAuthProtectedResource(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleProtectedResource(w, r)
}

func (api *StreamingAPI) handleMCPOAuthMetadata(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleMetadata(w, r)
}

func (api *StreamingAPI) handleMCPOAuthRegister(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleRegister(w, r)
}

func (api *StreamingAPI) handleMCPOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleAuthorize(w, r)
}

func (api *StreamingAPI) handleMCPOAuthConsent(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleConsent(w, r)
}

func (api *StreamingAPI) handleMCPOAuthToken(w http.ResponseWriter, r *http.Request) {
	mcpOAuthServer().HandleToken(w, r)
}

func (api *StreamingAPI) handleMCPOAuthConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		id := strings.TrimPrefix(r.URL.Path, mcpOAuthConnectionsPath+"/")
		mcpOAuthServer().RevokeConnection(w, r, id)
		return
	}
	mcpOAuthServer().HandleConnections(w, r)
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

func mcpOAuthFamilyActive(ctx context.Context, family string) (mcpoauth.Grant, error) {
	store, err := openMCPOAuthStore()
	if err != nil {
		return mcpoauth.Grant{}, err
	}
	defer store.Close()
	return store.ActiveFamily(ctx, family)
}

// Keep the generic read-and-run scope check identical for PAT and OAuth grants.
func mcpOAuthTokenForGrant(grant mcpoauth.Grant) accesstokens.Token {
	name := "MCP OAuth"
	if grant.ClientID == cliOAuthClientID {
		name = "AgentWorks CLI"
	}
	// An OAuth grant reaches everything the user can: all their workflows and,
	// when a Crew permission was approved, all Crews they can use.
	allCrews := slices.Contains(grant.Scopes, "crews:read") || slices.Contains(grant.Scopes, "crews:run") || slices.Contains(grant.Scopes, "crews:write")
	return accesstokens.Token{ID: "oauth-" + grant.FamilyID, Name: name, UserID: grant.UserID, Username: grant.Username, Email: grant.Email, Provider: grant.Provider, Scopes: grant.Scopes, AllWorkflows: true, AllCrews: allCrews, ExpiresAt: time.Unix(grant.Expires, 0)}
}

func mcpOAuthSecret() (string, error) {
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return "", err
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	// OAuth clients and refresh tokens are server-owned state, never workflow
	// files. Keep the same isolation rule as personal access tokens.
	docs, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(docs, root)
	if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("MCP OAuth state must be outside workspace documents")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if actualDocs, e := filepath.EvalSymlinks(docs); e == nil {
		docs = actualDocs
	}
	rel, err = filepath.Rel(docs, resolved)
	if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("MCP OAuth state must be outside workspace documents")
	}
	authority := sha256.Sum256(GetAuthSecret())
	return filepath.Join(resolved, "auth", hex.EncodeToString(authority[:16])+".mcp-oauth.sqlite"), nil
}

func openMCPOAuthStore() (*mcpoauth.Store, error) {
	path, err := mcpOAuthSecret()
	if err != nil {
		return nil, err
	}
	return mcpoauth.OpenStore(path, mcpOAuthConfig())
}
