package main

// Per-user tokens for a workspace-api that serves workflows to other people's
// laptops (a remote workspace server). When WORKSPACE_SERVER_USER_TOKENS_FILE
// is set, every /api request must carry a known token, and the server derives
// the user from it: a client-sent X-User-ID is overwritten, never trusted.
// Unset (the default for a laptop's own workspace-api) this is a no-op.
//
// File format (tokens stored as SHA-256 hex, never in the clear):
//
//	{"users": {"alice": "<sha256 of alice's token>", "bob": "..."}}

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	serverUserTokensEnv         = "WORKSPACE_SERVER_USER_TOKENS_FILE"
	serverUserAuthenticatedFlag = "workspace_server_user_authenticated"
)

type serverUserTokens struct {
	mu       sync.Mutex
	path     string
	modTime  time.Time
	checked  time.Time
	byDigest map[string]string // sha256 hex -> user id
}

func (s *serverUserTokens) lookup(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.checked) >= time.Second {
		s.checked = time.Now()
		if info, err := os.Stat(s.path); err == nil && !info.ModTime().Equal(s.modTime) {
			if raw, err := os.ReadFile(s.path); err == nil { // #nosec G304 -- operator-configured path
				var f struct {
					Users map[string]string `json:"users"`
				}
				if err := json.Unmarshal(raw, &f); err == nil {
					next := make(map[string]string, len(f.Users))
					for user, digest := range f.Users {
						if d := strings.ToLower(strings.TrimSpace(digest)); d != "" && strings.TrimSpace(user) != "" {
							next[d] = strings.TrimSpace(user)
						}
					}
					s.byDigest, s.modTime = next, info.ModTime()
				} else {
					log.Printf("[SERVER_USER_AUTH] parse %s: %v", s.path, err)
				}
			}
		}
	}
	sum := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(sum[:])
	for known, user := range s.byDigest {
		if subtle.ConstantTimeCompare([]byte(known), []byte(digest)) == 1 {
			return user, true
		}
	}
	return "", false
}

func requestToken(c *gin.Context) string {
	if token := strings.TrimSpace(c.GetHeader("X-Workspace-Token")); token != "" {
		return token
	}
	if auth := strings.TrimSpace(c.GetHeader("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

// requireServerUserToken authenticates every /api request in server mode and
// pins X-User-ID to the token's user.
func requireServerUserToken() gin.HandlerFunc {
	path := strings.TrimSpace(os.Getenv(serverUserTokensEnv))
	if path == "" {
		return func(c *gin.Context) { c.Next() }
	}
	tokens := &serverUserTokens{path: path}
	log.Printf("[SERVER_USER_AUTH] per-user tokens required on /api (users file %s)", path)
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		user, ok := tokens.lookup(requestToken(c))
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "workspace server authorization required"})
			return
		}
		c.Request.Header.Set("X-User-ID", user)
		c.Set(serverUserAuthenticatedFlag, true)
		c.Next()
	}
}
