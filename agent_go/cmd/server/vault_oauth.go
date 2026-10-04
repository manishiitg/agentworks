package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"os"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/mcpoauth"
)

const vaultMCPPath = "/api/vault/mcp"
const vaultOAuthPrefix = "/api/oauth/vault"
const vaultResourceMetadataPath = "/.well-known/oauth-protected-resource/api/vault/mcp"
const vaultAuthorizationMetadataPath = "/.well-known/oauth-authorization-server/vault"

// Vault's authorization server shares platform login and its active directory.
// Its resource, token namespace and private SQLite state are separate from
// workflow/CLI grants. Neither a product JWT nor the service token is an MCP token.
func vaultOAuthConfig() mcpoauth.Config {
	cfg := mcpoauth.Config{
		PublicURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/"),
		ResourcePath: vaultMCPPath, Scopes: []string{"vault:mcp"},
		AccessPrefix: "aw_vault_", RefreshPrefix: "aw_vault_refresh_",
		ConsentUIPath: "/oauth/vault", ProtectedResourcePath: vaultResourceMetadataPath,
		RegisterPath: vaultOAuthPrefix + "/register", AuthorizePath: vaultOAuthPrefix + "/authorize", TokenPath: vaultOAuthPrefix + "/token",
		CurrentUser: func(r *http.Request) (*mcpoauth.User, bool) {
			c := GetUserFromContext(r.Context())
			if c == nil || c.AccessToken != nil || c.Scope != "" || c.Provider == "bot_route" || !activeMCPPerson(c.UserID) || !userAllowedProduct(c, "mcp-gateway") {
				return nil, false
			}
			return &mcpoauth.User{ID: c.UserID, Username: c.Username, Email: c.Email, Provider: c.Provider}, true
		},
		GrantActive: func(_ context.Context, g mcpoauth.Grant) bool {
			_, err := vaultGrantClaims(g)
			return err == nil
		},
	}
	cfg.OpenStore = func() (*mcpoauth.Store, error) {
		path, err := mcpOAuthSecret() // already excludes workspace roots and symlinks
		if err != nil {
			return nil, err
		}
		return mcpoauth.OpenStore(path+".vault", cfg)
	}
	return cfg
}

func vaultGrantClaims(g mcpoauth.Grant) (*UserClaims, error) {
	c, err := accessTokenClaims(accesstokens.Token{UserID: g.UserID, Username: g.Username, Email: g.Email, Provider: g.Provider})
	if err != nil || !slices.Contains(g.Scopes, "vault:mcp") || !activeMCPPerson(g.UserID) || !userAllowedProduct(c, "mcp-gateway") {
		return nil, errors.New("Vault access revoked")
	}
	return c, nil
}

func (api *StreamingAPI) registerVaultOAuthRoutes(router *mux.Router) {
	// Resolve configuration on every request, matching the platform auth paths.
	serve := func(fn func(*mcpoauth.Server, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fn(mcpoauth.NewServer(vaultOAuthConfig()), w, r) }
	}
	router.HandleFunc(vaultResourceMetadataPath, api.handleVaultOAuthResource).Methods("GET")
	router.HandleFunc(vaultAuthorizationMetadataPath, api.handleVaultOAuthMetadata).Methods("GET")
	router.HandleFunc(vaultOAuthPrefix+"/register", serve((*mcpoauth.Server).HandleRegister)).Methods("POST")
	router.HandleFunc(vaultOAuthPrefix+"/authorize", serve((*mcpoauth.Server).HandleAuthorize)).Methods("GET")
	router.HandleFunc(vaultOAuthPrefix+"/token", serve((*mcpoauth.Server).HandleToken)).Methods("POST")
	router.HandleFunc(vaultOAuthPrefix+"/consent", serve((*mcpoauth.Server).HandleConsent)).Methods("GET", "POST")
	router.HandleFunc(vaultOAuthPrefix+"/connections", serve((*mcpoauth.Server).HandleConnections)).Methods("GET")
	router.HandleFunc(vaultOAuthPrefix+"/connections/{id}", serve(func(s *mcpoauth.Server, w http.ResponseWriter, r *http.Request) {
		s.RevokeConnection(w, r, mux.Vars(r)["id"])
	})).Methods("DELETE")
	router.HandleFunc(vaultMCPPath, api.handleExternalVaultMCP).Methods("POST", "GET", "DELETE")
	router.HandleFunc("/api/vault/connection", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := vaultOAuthConfig().CurrentUser(r); !ok {
			http.Error(w, "Vault access required", 403)
			return
		}
		_, resource, ok := mcpoauth.NewServer(vaultOAuthConfig()).URLs()
		if !ok {
			http.Error(w, "PUBLIC_URL is not configured", 503)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeUsersJSON(w, 200, map[string]string{"endpoint": resource})
	}).Methods("GET")
}

// Path-qualified issuer prevents Vault clients discovering the workflow AS.
func (api *StreamingAPI) handleVaultOAuthResource(w http.ResponseWriter, r *http.Request) {
	cfg := vaultOAuthConfig()
	origin, resource, ok := mcpoauth.NewServer(cfg).URLs()
	if !ok {
		http.Error(w, "PUBLIC_URL is not configured", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{origin + "/vault"}, "scopes_supported": cfg.Scopes})
}

func (api *StreamingAPI) handleVaultOAuthMetadata(w http.ResponseWriter, r *http.Request) {
	cfg := vaultOAuthConfig()
	origin, _, ok := mcpoauth.NewServer(cfg).URLs()
	if !ok {
		http.Error(w, "PUBLIC_URL is not configured", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": origin + "/vault", "authorization_endpoint": origin + cfg.AuthorizePath, "token_endpoint": origin + cfg.TokenPath, "registration_endpoint": origin + cfg.RegisterPath,
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": cfg.Scopes,
	})
}

func (api *StreamingAPI) handleExternalVaultMCP(w http.ResponseWriter, r *http.Request) {
	srv := mcpoauth.NewServer(vaultOAuthConfig())
	grant, err := srv.AuthenticateRequest(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		srv.Challenge(w)
		http.Error(w, "Vault OAuth sign-in required", http.StatusUnauthorized)
		return
	}
	target, secret, err := capLayerServiceConfig()
	if err != nil {
		http.Error(w, "Vault unavailable", 503)
		return
	}
	// Rebuild headers: user-supplied identity, cookies, tokens and connector
	// selections cannot override the verified grant. Group policy stays live.
	proxy := &httputil.ReverseProxy{Transport: capLayerTransport, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.URL.Path = strings.TrimRight(target.Path, "/") + "/api/admin/runtime/external-mcp"
		pr.Out.URL.RawPath, pr.Out.URL.RawQuery = "", ""
		pr.Out.Header = make(http.Header)
		for _, key := range []string{"Content-Type", "Accept", "Mcp-Protocol-Version"} {
			pr.Out.Header.Set(key, r.Header.Get(key))
		}
		pr.Out.Header.Set("Authorization", "Bearer "+secret)
		pr.Out.Header.Set("X-CapLayer-Actor", grant.UserID)
		pr.Out.Header.Set("X-Vault-Platform-User", "1")
		pr.Out.Header.Set("X-Vault-OAuth-Client", grant.ClientID)
	}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return errors.New("unexpected Vault redirect")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "Vault unavailable", 502) }}
	w.Header().Set("Cache-Control", "no-store")
	proxy.ServeHTTP(w, r)
}
