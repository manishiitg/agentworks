package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The same installed product set controls the website, sign-in and tools.
func agentBridgeOnlyDeployment() bool {
	if !productEnabled("bullpen") {
		return false
	}
	for _, product := range []string{"agentworks", "work", "relays", "knowledgebase", "mcp-gateway", "llm-gateway"} {
		if productEnabled(product) {
			return false
		}
	}
	return true
}

func publicNetworkSignupEnabled() bool {
	return IsMultiUserMode() && agentBridgeOnlyDeployment() && strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_PUBLIC_SIGNUP")), "true")
}

func publicNetworkSSOOnly() bool {
	return IsMultiUserMode() && agentBridgeOnlyDeployment() && strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_PUBLIC_SSO_ONLY")), "true")
}

// This is the one network sign-in policy used by discovery and execution.
func publicNetworkSSOProviderAllowed(name string) bool {
	return slices.Contains([]string{"supabase-google", "supabase-github"}, name) &&
		slices.Contains(getEnabledProviderNames(), name)
}

func publicNetworkPasswordSignupEnabled() bool {
	return publicNetworkSignupEnabled() && !publicNetworkSSOOnly()
}

// Internal password access is an explicit server-side exception for existing
// directory identities, never a property a public account can select.
func publicNetworkInternalTestUser(username string) *UserRecord {
	dir, err := readUserDirectoryFile()
	if err != nil {
		return nil
	}
	rec := dir.byUsername(username)
	if rec == nil || rec.Disabled || rec.ID == "" || rec.PasswordHash == "" {
		return nil
	}
	for _, id := range strings.Split(os.Getenv("AUTH_INTERNAL_TEST_USER_IDS"), ",") {
		if strings.TrimSpace(id) == rec.ID {
			return rec
		}
	}
	return nil
}

// A messaging-only installation has no hosted project, file or model APIs.
// Company installations keep their existing route-level authorization.
func agentBridgeAPIMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !agentBridgeOnlyDeployment() || agentBridgeAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		externalError(w, http.StatusForbidden, "product_unavailable", "This network supports AgentBridge messaging only. Open AgentBridge to connect an agent.")
	})
}

func agentBridgeAPIPath(path string) bool {
	for _, prefix := range []string{"/api/bullpen/", "/api/downloads/cli/", "/api/oauth/mcp/connections/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	switch path {
	case "/api/health", "/api/live",
		"/api/auth/mode", "/api/auth/register", "/api/auth/login", "/api/auth/logout", "/api/auth/me", "/api/auth/password", "/api/auth/start", "/api/auth/callback",
		"/api/oauth/cli/device", "/api/oauth/cli/consent", "/api/oauth/cli/token", "/api/oauth/cli/revoke",
		"/api/oauth/mcp/register", "/api/oauth/mcp/authorize", "/api/oauth/mcp/consent", "/api/oauth/mcp/token", "/api/oauth/mcp/connections",
		"/api/external/v1/tools", "/api/external/v1/call", "/api/external/v1/mcp",
		"/api/external/v1/agents/events", "/api/external/v1/agents/attachment", "/api/external/v1/agents/sharing", "/api/external/v1/agents/usage",
		"/api/external/v1/skill.md", "/api/external/v1/skill.zip", "/api/external/v1/agentworks.plugin":
		return true
	}
	return false
}

var publicSignupLimiter = &loginRateLimiter{failures: map[string]*loginFailures{}, limit: 10, window: 10 * time.Minute, lockout: 10 * time.Minute, now: time.Now}

func (api *StreamingAPI) registerPublicNetworkUser(w http.ResponseWriter, r *http.Request) {
	if !publicSignupLimiter.allow(clientIPOf(r)) {
		writeUsersError(w, http.StatusTooManyRequests, "Too many sign-up attempts. Try again in ten minutes.")
		return
	}
	publicSignupLimiter.fail(clientIPOf(r))
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		writeUsersError(w, http.StatusBadRequest, "Enter a username and password.")
		return
	}
	name, err := normalizeMessageAgentName(req.Username)
	if err != nil || len(name) < 3 || publicNetworkReservedUsername(name) {
		writeUsersError(w, http.StatusBadRequest, "Choose a username of 3 to 64 letters, digits, dots, underscores or hyphens, starting with a letter or digit.")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 256 {
		writeUsersError(w, http.StatusBadRequest, "Use a password of 8 to 256 characters.")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, "Could not create the account. Try again.")
		return
	}
	userDirectoryMutationMu.Lock()
	defer userDirectoryMutationMu.Unlock()
	dir, err := readUserDirectoryFile()
	if err != nil {
		writeUsersError(w, http.StatusServiceUnavailable, "Sign-up is temporarily unavailable. Try again shortly.")
		return
	}
	if dir.byUsername(name) != nil {
		writeUsersError(w, http.StatusConflict, "That username is taken. Choose another.")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Password sign-up does not claim an unverified email or link an SSO identity.
	rec := UserRecord{ID: uuid.NewString(), Username: name, PasswordHash: hash, Products: []string{"bullpen"}, CreatedAt: now, UpdatedAt: now}
	dir.Users = append(dir.Users, rec)
	if err := saveUserDirectory(dir); err != nil {
		writeUsersError(w, http.StatusServiceUnavailable, "Could not save the account. Try again shortly.")
		return
	}
	log.Printf("[PUBLIC_NETWORK] account_created user_id=%s provider=simple", rec.ID)
	writeUsersJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func publicNetworkExternalSignup(ext *ExternalUser) bool {
	return publicNetworkSignupEnabled() && ext != nil && publicNetworkSSOProviderAllowed(ext.Provider) && ext.EmailVerified && strings.TrimSpace(ext.Email) != "" && ext.ExternalID != ""
}

func cliPersonScopes(user *UserClaims) []string {
	scopes := mcpOAuthPersonScopes(user)
	if !agentBridgeOnlyDeployment() {
		scopes = append(scopes, "devices:connect")
	}
	return scopes
}

func mcpOAuthAdvertisedScopes() []string {
	if agentBridgeOnlyDeployment() {
		return []string{"messages:read", "messages:send"}
	}
	return mcpOAuthScopes
}

func publicNetworkMessageQuotaErr(s agentMessageStore, userID string, now time.Time) error {
	if !publicNetworkSignupEnabled() {
		return nil
	}
	count := 0
	for _, c := range s.Conversations {
		for _, m := range c.Messages {
			if c.Endpoints[m.From].UserID == userID && now.Sub(m.SentAt) < time.Minute {
				count++
			}
		}
	}
	if count >= 60 {
		return fmt.Errorf("You can send up to 60 messages per minute. Wait a minute, then send again.")
	}
	return nil
}

func publicNetworkReservedUsername(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "admin", "administrator", "support", "help", "security", "system", "agentworks", "agentbridge", "bullpen", "root", "owner", "platform", "moderator":
		return true
	}
	return adminUsernamesFromEnv()[name]
}

func publicNetworkUsername(dir *userDirectory, userID, displayName string) string {
	name, err := normalizeMessageAgentName(displayName)
	if err != nil || len(name) < 3 || publicNetworkReservedUsername(name) {
		sum := sha256.Sum256([]byte(userID))
		name = fmt.Sprintf("member-%x", sum[:6])
	}
	return dir.uniqueDirectoryUsername(name, "")
}
