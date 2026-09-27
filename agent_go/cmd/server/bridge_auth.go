package server

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// envBridgeAllowGlobalToken re-admits the process-wide token on the agent
// tool routes. It is a rollback switch for a missed hand-off, not a mode.
const envBridgeAllowGlobalToken = "AGENTWORKS_BRIDGE_ALLOW_GLOBAL_TOKEN"

// bridgeAuthMiddleware authenticates the agent tool routes (/tools/... and
// /s/{session_id}/tools/...). Each agent holds a token for its own session
// (pkg/common/bridge_token.go); the session a request acts as must be that
// session:
//   - a /s/{session_id} path or X-Session-ID header naming another session is
//     refused;
//   - X-Session-ID and the request context are set to the token's session, so
//     a session_id in the body cannot override it either.
//
// The process-wide API token is refused here unless the rollback switch is
// set: every agent-facing hand-off now carries a session token, and the
// global token would let a caller name any session (reported 2026-09-27, H1).
func bridgeAuthMiddleware(globalToken string) func(http.Handler) http.Handler {
	allowGlobal := strings.TrimSpace(os.Getenv(envBridgeAllowGlobalToken)) == "1"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"success":false,"error":"missing or invalid Authorization header, expected Bearer token"}`, http.StatusUnauthorized)
				return
			}
			provided := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))

			if sessionID, ok := common.VerifyBridgeToken(provided); ok {
				pathSession := strings.TrimSpace(mux.Vars(r)["session_id"])
				headerSession := strings.TrimSpace(r.Header.Get("X-Session-ID"))
				if (pathSession != "" && pathSession != sessionID) || (headerSession != "" && headerSession != sessionID) {
					log.Printf("[BRIDGE_AUTH] refused: token session %q named session path=%q header=%q (%s)", sessionID, pathSession, headerSession, r.URL.Path)
					http.Error(w, `{"success":false,"error":"this token belongs to a different session"}`, http.StatusForbidden)
					return
				}
				r.Header.Set("X-Session-ID", sessionID)
				r = r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, sessionID))
				next.ServeHTTP(w, r)
				return
			}

			if globalToken != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(globalToken)) == 1 {
				if allowGlobal {
					log.Printf("[BRIDGE_AUTH] WARNING: process-wide token used on %s (session header=%q); %s=1 is set", r.URL.Path, r.Header.Get("X-Session-ID"), envBridgeAllowGlobalToken)
					next.ServeHTTP(w, r)
					return
				}
				log.Printf("[BRIDGE_AUTH] refused: process-wide token on %s (session header=%q); this caller needs its session token", r.URL.Path, r.Header.Get("X-Session-ID"))
				http.Error(w, `{"success":false,"error":"this route needs the session's own token (MCP_API_TOKEN from this session's environment)"}`, http.StatusUnauthorized)
				return
			}
			http.Error(w, `{"success":false,"error":"invalid API token"}`, http.StatusUnauthorized)
		})
	}
}
