package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// ContextKey type for context values
type ContextKey string

// UserContextKey is the key for user claims in context
const UserContextKey ContextKey = "user"

const deprecatedDefaultAuthSecret = "dev-secret-change-in-production"

// UserClaims represents the JWT claims for authenticated users
type UserClaims struct {
	ExternalBuilderOperationID string              `json:"-"` // Trusted operation binding; never accepted from JWT/JSON.
	SlackTrustedApp            bool                `json:"-"` // Set only by the validated server-owned event adapter.
	ExecutionPrincipal         *ExecutionPrincipal `json:"-"`
	AccessToken                *accesstokens.Token `json:"-"` // Server-validated PAT restrictions; never read from JWT claims.
	UserID                     string              `json:"user_id"`
	Username                   string              `json:"username"`
	Email                      string              `json:"email,omitempty"`
	Provider                   string              `json:"provider,omitempty"` // Auth provider: "simple", "cognito", "supabase"
	// BotRouteGrant is set only by server-owned connector paths. It lets normal
	// workflow access resolution treat a configured bot route as the executing
	// principal while keeping the external sender as audit metadata.
	BotRouteGrant           string `json:"bot_route_grant,omitempty"`
	BotRouteWorkflowID      string `json:"bot_route_workflow_id,omitempty"`
	BotRouteProfileID       string `json:"bot_route_profile_id,omitempty"`
	BotRouteConversationKey string `json:"bot_route_conversation_key,omitempty"`
	BotRouteWorkspacePath   string `json:"bot_route_workspace_path,omitempty"`
	// Scope, when set, narrows a token to one purpose: the middleware admits it
	// only to the paths scopeAllowsPath names for that scope. A normal session
	// token has no scope. Today's only scope is "report-preview" (a headless
	// browser rendering one workflow's report), bound to ScopeWorkspace.
	Scope               string `json:"scope,omitempty"`
	ScopeWorkspace      string `json:"scope_workspace,omitempty"`
	PreviewConnectionID string `json:"preview_connection_id,omitempty"`
	ScopeFile           string `json:"scope_file,omitempty"` // exact file for report media playback
	jwt.RegisteredClaims
}

// HardcodedUser represents a user defined in environment variables
type HardcodedUser struct {
	Username string
	Password string // Plain text password from env
	UserID   string // Generated from username
}

var (
	hardcodedUsers     map[string]*HardcodedUser
	hardcodedUsersOnce sync.Once
)

// IsMultiUserMode returns true if MULTI_USER_MODE environment variable is set to "true"
func IsMultiUserMode() bool {
	return os.Getenv("MULTI_USER_MODE") == "true"
}

// IsLocalMode returns true if LOCAL_MODE environment variable is set to "true".
// When local mode is enabled, features like CDP browser connection are available.
func IsLocalMode() bool {
	return os.Getenv("LOCAL_MODE") == "true"
}

// IsHardcodedUserMode returns true if AUTH_USERS environment variable is set
// When hardcoded users are configured, registration is disabled
func IsHardcodedUserMode() bool {
	return os.Getenv("AUTH_USERS") != ""
}

// GetHardcodedUsers parses AUTH_USERS env var and returns the user map
// Format: AUTH_USERS=user1:pass1,user2:pass2
func GetHardcodedUsers() map[string]*HardcodedUser {
	hardcodedUsersOnce.Do(func() {
		hardcodedUsers = make(map[string]*HardcodedUser)
		usersEnv := os.Getenv("AUTH_USERS")
		if usersEnv == "" {
			return
		}

		pairs := strings.Split(usersEnv, ",")
		for _, pair := range pairs {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) != 2 {
				log.Printf("[AUTH] Invalid AUTH_USERS format for entry: %s (expected user:pass)", pair)
				continue
			}
			username := strings.TrimSpace(parts[0])
			password := strings.TrimSpace(parts[1])
			if username == "" || password == "" {
				log.Printf("[AUTH] Empty username or password in AUTH_USERS entry")
				continue
			}

			// Generate a deterministic user ID from username
			hash := sha256.Sum256([]byte("user:" + username))
			userID := hex.EncodeToString(hash[:16])

			hardcodedUsers[username] = &HardcodedUser{
				Username: username,
				Password: password,
				UserID:   userID,
			}
			log.Printf("[AUTH] Loaded hardcoded user: %s (id: %s)", username, userID)
		}

		if len(hardcodedUsers) > 0 {
			log.Printf("[AUTH] Hardcoded user mode enabled with %d users", len(hardcodedUsers))
		}
	})
	return hardcodedUsers
}

// ValidateHardcodedUser checks if username/password match a hardcoded user
// Returns the user if valid, nil otherwise
func ValidateHardcodedUser(username, password string) *HardcodedUser {
	users := GetHardcodedUsers()
	if user, ok := users[username]; ok {
		if user.Password == password {
			return user
		}
	}
	return nil
}

// GetDefaultUserID returns the default user ID for single-user mode
// Uses DEFAULT_USER_ID env var or falls back to "default-user"
func GetDefaultUserID() string {
	if id := os.Getenv("DEFAULT_USER_ID"); id != "" {
		return id
	}
	return "default"
}

// startupAuthSecret caches AUTH_SECRET in memory at boot. The env var is
// cleared right after (ClearAuthSecretFromEnv) so child processes (agent
// shells, CLIs, tmux) can never inherit it; see resolveBridgeTokenSecret
// for the same pattern.
var startupAuthSecret []byte

// InitAuthSecretCache reads the configured signing secret into memory. Call once at server
// startup, before ClearAuthSecretFromEnv.
func InitAuthSecretCache() {
	startupAuthSecret = GetAuthSecret()
}

// ClearAuthSecretFromEnv removes AUTH_SECRET from the process environment so
// spawned children cannot inherit it. Server code keeps working through the
// startup cache in GetAuthSecret.
func ClearAuthSecretFromEnv() {
	os.Unsetenv("AUTH_SECRET")
}

// ValidateConfiguredAuthSecret verifies the runtime JWT/encryption secret is explicit.
func ValidateConfiguredAuthSecret() error {
	return ValidateAuthSecretValue(string(GetAuthSecret()))
}

// ValidateAuthSecretValue verifies an AUTH_SECRET value is not empty or public.
func ValidateAuthSecretValue(secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("AUTH_SECRET env var must be set")
	}
	if secret == deprecatedDefaultAuthSecret {
		return fmt.Errorf("AUTH_SECRET is using the deprecated public default")
	}
	return nil
}

// GetAuthSecret uses the private desktop bootstrap when present; other launches
// use the live environment (tests, CLI commands), then the startup cache.
// The returned slice is a copy; callers must not retain or mutate it.
func GetAuthSecret() []byte {
	if desktopStartup.AuthSecret != "" {
		return []byte(desktopStartup.AuthSecret)
	}
	if v := strings.TrimSpace(os.Getenv("AUTH_SECRET")); v != "" {
		return []byte(v)
	}
	out := make([]byte, len(startupAuthSecret))
	copy(out, startupAuthSecret)
	return out
}

// AuthMiddleware handles JWT authentication for API routes
// API routes require JWT tokens in both single-user and multi-user modes.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The MCP Apps document bootstrap is inert and must load through API
		// ingress without credentials. Only this exact GET is public.
		if (r.Method == http.MethodGet && r.URL.Path == dashboardAppFramePath) || shouldSkipAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Browser event streams send Authorization headers. Reject URL credentials
		// even when a valid header is present; old clients must reconnect without
		// putting their sign-in token in proxy logs.
		if headerOnlyEventStream(r.URL.Path) && r.URL.Query().Has("token") {
			http.Error(w, "Use an Authorization header for event streams", http.StatusBadRequest)
			return
		}
		// Query authentication remains for WebSocket upgrades and signed links.
		authHeader := r.Header.Get("Authorization")
		headerCredential := authHeader != ""
		cookieScope := ""
		if authHeader == "" {
			if qToken := r.URL.Query().Get("token"); qToken != "" {
				authHeader = "Bearer " + qToken
			}
		}
		if authHeader == "" {
			if token, scope := serverProductUICookie(r); token != "" {
				authHeader, cookieScope = "Bearer "+token, scope
			}
		}
		if authHeader == "" {
			if isMCPRequestPath(r.URL.Path) {
				projectMCPChallenge(w, r)
			}
			http.Error(w, `{"error": "Authorization header required"}`, http.StatusUnauthorized)
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, `{"error": "Invalid authorization header format"}`, http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if strings.HasPrefix(tokenString, cliOAuthAccessPrefix) {
			claims, ok := authenticateCLIOAuthToken(w, r, tokenString)
			if !ok {
				return
			}
			next.ServeHTTP(w, r.WithContext(productpolicy.WithSelection(context.WithValue(r.Context(), UserContextKey, claims), builderProductSelection(claims))))
			return
		}
		if strings.HasPrefix(tokenString, mcpOAuthAccessPrefix) {
			claims, ok := authenticateMCPOAuthToken(w, r, tokenString)
			if !ok {
				return
			}
			next.ServeHTTP(w, r.WithContext(productpolicy.WithSelection(context.WithValue(r.Context(), UserContextKey, claims), builderProductSelection(claims))))
			return
		}
		if strings.HasPrefix(tokenString, accesstokens.Prefix) {
			claims, ok := authenticateAccessToken(w, r, tokenString)
			if !ok {
				return
			}
			next.ServeHTTP(w, r.WithContext(productpolicy.WithSelection(context.WithValue(r.Context(), UserContextKey, claims), builderProductSelection(claims))))
			return
		}
		claims := &UserClaims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
			// Validate signing method
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return GetAuthSecret(), nil
		})

		if err != nil {
			log.Printf("[AUTH] Token validation failed: %v", err)
			http.Error(w, `{"error": "Invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		if !token.Valid {
			http.Error(w, `{"error": "Invalid token"}`, http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(claims.Scope, serverProductUIScopePrefix) ||
			(cookieScope != "" && (claims.Scope != "" || !serverProductUIRequestAllowed(r))) {
			http.Error(w, "product browser session not valid for this request", http.StatusForbidden)
			return
		}
		if cookieScope != "" {
			claims.Scope = cookieScope // Server-held session binding, never accepted from JWT claims.
		}

		// A linked OAuth identity and its password account are one account.
		// Canonicalize before any authorization or workspace lookup so an older
		// provider-subject JWT cannot create a second per-user chat tree.
		canonicalizeDirectoryUserClaims(claims)

		// A disabled account is refused even with a still-valid token, so an
		// admin switching someone off takes effect now, not at token expiry.
		if directoryUserIsDisabled(claims) {
			http.Error(w, `{"error": "This account is disabled"}`, http.StatusForbidden)
			return
		}
		// A token for an identity the directory does not know (a session
		// minted before the directory existed or before a migration renamed
		// its users) is refused so the browser signs in again, instead of
		// silently browsing as a ghost user with an empty workspace. The
		// wording contains "expired" on purpose: that is what the frontend
		// keys on to drop the stale token and show the login screen.
		if directoryUserIsUnknown(claims) {
			http.Error(w, `{"error": "Session expired: this sign-in predates the current user directory, please sign in again"}`, http.StatusUnauthorized)
			return
		}
		// A scoped token (report preview) is valid only for its own endpoints.
		if !scopeAllowsPath(claims.Scope, r.URL.Path) {
			http.Error(w, `{"error": "This token is not valid for this endpoint"}`, http.StatusForbidden)
			return
		}
		if claims.PreviewConnectionID != "" {
			connection, err := activeDashboardPreviewConnection(r.Context(), claims.PreviewConnectionID)
			if err != nil || connection.UserID != claims.UserID {
				http.Error(w, "preview connection expired or revoked", 401)
				return
			}
			claims.AccessToken = &connection
		}

		// A sign-in renews while it is used: a plain browser session older than a
		// day gets a fresh one, so only a week without use signs a person out.
		if headerCredential && plainSessionClaims(claims) && claims.IssuedAt != nil && time.Since(claims.IssuedAt.Time) > sessionRenewAfter {
			if renewed, err := GenerateJWTWithProvider(claims.UserID, claims.Username, claims.Email, claims.Provider); err == nil {
				w.Header().Set(renewedTokenHeader, renewed)
			}
		}

		// Token is valid, add claims to context
		ctx := productpolicy.WithSelection(context.WithValue(r.Context(), UserContextKey, claims), builderProductSelection(claims))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

const (
	sessionLifetime    = 7 * 24 * time.Hour
	sessionRenewAfter  = 24 * time.Hour
	renewedTokenHeader = "X-Renewed-Token"
)

// plainSessionClaims is a person's own sign-in: no scope, preview or bot
// route narrows it, so a renewal grants nothing the sign-in did not.
func plainSessionClaims(c *UserClaims) bool {
	return c.Scope == "" && c.ScopeWorkspace == "" && c.ScopeFile == "" && c.PreviewConnectionID == "" &&
		c.BotRouteGrant == "" && c.BotRouteWorkflowID == "" && c.BotRouteProfileID == "" && c.BotRouteConversationKey == "" && c.BotRouteWorkspacePath == ""
}

// headerOnlyEventStream covers the browser's chat and live-notice SSE routes.
func headerOnlyEventStream(path string) bool {
	return path == "/api/live" || strings.HasPrefix(path, "/api/sessions/") && strings.HasSuffix(path, "/events/stream")
}

// shouldSkipAuth returns true for paths that don't require authentication
func shouldSkipAuth(path string) bool {
	if path == browserExtensionConnectPath || path == browserPairingSetupPath || path == browserPairingPeekPath || path == browserPairingRedeemPath {
		return true
	} // WebSocket validates its relay credential; HTTP exchange validates a one-use handoff.
	if path == vaultMCPPath || path == vaultOAuthPrefix+"/register" || path == vaultOAuthPrefix+"/authorize" || path == vaultOAuthPrefix+"/token" {
		return true // handlers enforce PKCE or a resource-bound Vault token
	}
	// Public endpoints that don't require auth
	publicPaths := []string{
		"/api/auth/login",
		"/api/auth/register",
		"/api/auth/mode",
		"/api/auth/start",    // OAuth flow initiation (must be public)
		"/api/auth/callback", // Multi-provider auth callback
		"/api/auth/desktop/exchange",
		"/api/auth/providers", // Get available auth providers
		"/api/health",
		"/api/capabilities",
		"/api/hooks/",         // Only inbound webhook handlers live under /api/hooks/ (Crew and Goal, every address); each verifies its own secret/signature. Keep other routes out of it.
		"/api/shared/",        // Shared session links are public
		"/api/oauth/callback", // OAuth callback comes from external provider without our JWT
		// Google redirects the user's browser here after they consent. It is a
		// plain navigation, so it cannot carry our JWT. Safe to expose because
		// the request is worthless without the OAuth state parameter, which is
		// random, server-held, single-use, and expires in 15 minutes — a
		// callback with no valid state does nothing at all.
		"/api/human-feedback/gmail/auth/callback",
		"/api/downloads/",           // Chrome CDP launcher zip (macOS)
		"/api/desktop-model-proxy/", // A program on the Mac; the handler checks loopback and the account's own key
		mcpOAuthRegisterPath,
		mcpOAuthAuthorizePath,
		mcpOAuthTokenPath,
		cliOAuthDevicePath,
		cliOAuthTokenPath,
		cliOAuthRevokePath,
	}

	for _, p := range publicPaths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	// Profiling endpoints must never ride the static-file exemption below:
	// heap, goroutine, and cmdline dumps can carry secrets, tokens, and
	// workflow data. Local profilers authenticate like every other caller
	// (Bearer JWT/access token, or ?token= for curl).
	if strings.HasPrefix(path, "/debug/pprof/") {
		return false
	}

	// Static files don't need auth
	if !strings.HasPrefix(path, "/api/") {
		return true
	}

	return false
}

// GetUserFromContext extracts user claims from the request context
// Returns nil if no user is found (should not happen with middleware applied)
func GetUserFromContext(ctx context.Context) *UserClaims {
	if user, ok := ctx.Value(UserContextKey).(*UserClaims); ok {
		return user
	}
	return nil
}

// GetUserIDFromContext is a convenience function to get just the user ID
// Returns the default user ID if no user is found in context
func GetUserIDFromContext(ctx context.Context) string {
	user := GetUserFromContext(ctx)
	if user != nil {
		return user.UserID
	}
	return GetDefaultUserID()
}

// GenerateJWT creates a new JWT token for a user
func GenerateJWT(userID, username, email string) (string, error) {
	return GenerateJWTWithProvider(userID, username, email, "")
}

// GenerateJWTWithProvider creates a new JWT token for a user with provider information
func GenerateJWTWithProvider(userID, username, email, provider string) (string, error) {
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return "", err
	}

	claims := &UserClaims{
		UserID:   userID,
		Username: username,
		Email:    email,
		Provider: provider,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(sessionLifetime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "mcp-agent-builder",
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(GetAuthSecret())
}
