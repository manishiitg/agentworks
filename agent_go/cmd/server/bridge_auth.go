package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// virtualScopeSession is the session a virtual-tool scope ID belongs to
// (mcpagent codeexec: "<session>" or "<session>:vt:<trace>").
func virtualScopeSession(scope string) string {
	if i := strings.Index(scope, ":vt:"); i >= 0 {
		return scope[:i]
	}
	return scope
}

// requireOwnBodySession guards the /api/{mcp,virtual}/execute routes, which
// take the session to act as from the request body behind only a user login:
// a session_id there must belong to the signed-in user, or any user could
// call another user's MCP servers with their credentials. Calls without a
// session (the MCP tool tester) are unchanged.
func (api *StreamingAPI) requireOwnBodySession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.Body == nil {
			next(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err != nil {
			http.Error(w, `{"success":false,"error":"cannot read request"}`, http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var probe struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(body, &probe)
		sessionID := strings.TrimSpace(probe.SessionID)
		if header := strings.TrimSpace(r.Header.Get("X-Session-ID")); header != "" {
			sessionID = header
		}
		if sessionID != "" {
			claims := GetUserFromContext(r.Context())
			owner := ""
			if api.eventStore != nil {
				owner = strings.TrimSpace(api.eventStore.GetSessionOwner(sessionID))
			}
			if claims == nil || strings.TrimSpace(claims.UserID) == "" || owner == "" || owner != strings.TrimSpace(claims.UserID) {
				log.Printf("[BRIDGE_AUTH] refused: %s for session %q not owned by the caller", r.URL.Path, sessionID)
				http.Error(w, `{"success":false,"error":"that session is not yours"}`, http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}

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
				// A virtual-tool scope ("<session>" or "<session>:vt:<trace>")
				// selects whose offloaded outputs and tool catalogue a virtual
				// tool reads, so it must belong to the token's session too.
				if scope := strings.TrimSpace(r.Header.Get("X-Virtual-Scope-ID")); scope != "" && virtualScopeSession(scope) != sessionID {
					log.Printf("[BRIDGE_AUTH] refused: token session %q named virtual scope %q (%s)", sessionID, scope, r.URL.Path)
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
