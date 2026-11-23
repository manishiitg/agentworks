package executor

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// GenerateAPIToken generates a cryptographically random 32-byte hex token
// for bearer token authentication on the executor API.
func GenerateAPIToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate API token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// AuthMiddleware returns HTTP middleware that validates Bearer token authentication.
// Requests must include an "Authorization: Bearer <token>" header matching the provided token.
// Returns 401 Unauthorized on mismatch or missing header.
// OPTIONS requests are passed through for CORS preflight support.
func AuthMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow CORS preflight requests through
			if r.Method == "OPTIONS" {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"success":false,"error":"missing Authorization header"}`, http.StatusUnauthorized)
				return
			}

			// Expect "Bearer <token>"
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"success":false,"error":"invalid Authorization header format, expected Bearer token"}`, http.StatusUnauthorized)
				return
			}

			providedToken := strings.TrimPrefix(authHeader, "Bearer ")
			if subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
				http.Error(w, `{"success":false,"error":"invalid API token"}`, http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
