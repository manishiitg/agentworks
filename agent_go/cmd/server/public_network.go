package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The same installed product set controls the website, sign-in and tools.
func agentBridgeOnlyDeployment() bool {
	if !productEnabled("bullpen") {
		return false
	}
	for _, product := range []string{"agentworks", "work", "relays", "knowledgebase", "mcp-gateway", "llm-gateway", "sparkquill", "video-studio"} {
		if productEnabled(product) {
			return false
		}
	}
	return true
}

func publicNetworkSignupEnabled() bool {
	return IsMultiUserMode() && agentBridgeOnlyDeployment() && strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_PUBLIC_SIGNUP")), "true")
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
	if err != nil || len(name) < 3 || adminUsernamesFromEnv()[name] {
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
	return publicNetworkSignupEnabled() && ext != nil && ext.Provider == "supabase-google" && ext.EmailVerified && strings.TrimSpace(ext.Email) != "" && ext.ExternalID != ""
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
