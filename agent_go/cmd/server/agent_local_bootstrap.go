package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A local Mac install belongs to its machine owner. Native Claude Code and
// Codex chats on this Mac reach the app through a private socket (0700 folder,
// 0600 socket, this OS user only); browsers cannot open it. Socket requests
// without their own credential get the owner's messaging-only CLI login, which
// lives in this process and the OAuth store, never in a file or a reply.
// Per-agent child connections are derived from it the normal way.
const localAppResource = agentworksclient.LocalAppServer + "/api/external/v1"

type localAppSocketRequest struct{}

func fromLocalAppSocket(r *http.Request) bool {
	return r != nil && r.Context().Value(localAppSocketRequest{}) != nil
}

var localAppLogin struct {
	sync.Mutex
	family, access string
	expires        time.Time
}

func startLocalAgentSocket(handler http.Handler, address *net.TCPAddr) (net.Listener, error) {
	if runtime.GOOS != "darwin" || !IsLocalMode() || IsMultiUserMode() || !address.IP.IsLoopback() {
		return nil, nil
	}
	path, err := agentworksclient.LocalAppSocketPath()
	if err != nil {
		return nil, err
	}
	return serveLocalAgentSocket(handler, path)
}

func serveLocalAgentSocket(handler http.Handler, path string) (net.Listener, error) {
	listener, err := listenPrivateSocket(path)
	if err != nil {
		return nil, err
	}
	if _, err = localAppAccess(context.Background()); err != nil {
		listener.Close()
		return nil, fmt.Errorf("issue the Mac owner's messaging login: %w", err)
	}
	go func() { _ = http.Serve(listener, localAppSocketHandler(handler)) }()
	log.Printf("[AGENTBRIDGE] event=local_bootstrap owner=%s connection=%s result=ready", GetDefaultUserID(), localAppLogin.family)
	return listener, nil
}

func localAppSocketHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Messaging API and CLI token renewal only; no browser pages, cookies or query sign-in.
		if !strings.HasPrefix(r.URL.Path, "/api/external/v1/") && r.URL.Path != cliOAuthTokenPath && r.URL.Path != cliOAuthRevokePath {
			http.NotFound(w, r)
			return
		}
		r.Header.Del("Cookie")
		q := r.URL.Query()
		q.Del("token")
		r.URL.RawQuery = q.Encode()
		// A parent request carries no credential (or an empty bearer); a chat's
		// own child connection keeps its token and is checked as usual.
		if strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer")) == "" {
			access, err := localAppAccess(r.Context())
			if err != nil {
				log.Printf("[AGENTBRIDGE] event=local_bootstrap result=failed reason=%q", err.Error())
				http.Error(w, `{"error":"The AgentWorks app could not connect this chat. Restart the app and try again."}`, http.StatusServiceUnavailable)
				return
			}
			r.Header.Set("Authorization", "Bearer "+access)
		}
		r.Host = strings.TrimPrefix(agentworksclient.LocalAppServer, "http://")
		r.RemoteAddr = "127.0.0.1:0" // same machine; Unix sockets have no peer address
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localAppSocketRequest{}, true)))
	})
}

// localAppAccess returns the owner's messaging login, renewing it shortly before
// it expires. One login family per Mac: it is reused across restarts so attached
// chats' child connections survive, and older parent tokens are revoked on reissue.
func localAppAccess(ctx context.Context) (string, error) {
	l := &localAppLogin
	l.Lock()
	defer l.Unlock()
	if l.access != "" && time.Until(l.expires) > 5*time.Minute {
		return l.access, nil
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		return "", err
	}
	defer store.Close()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	user := GetDefaultUserID()
	const parents = `client_id=? AND resource=? AND user_id=? AND family_id NOT IN (SELECT family_id FROM agent_connections)`
	family := l.family
	if family == "" {
		err = tx.QueryRowContext(ctx, `SELECT family_id FROM tokens WHERE `+parents+` ORDER BY expires_at DESC LIMIT 1`, cliOAuthClientID, localAppResource, user).Scan(&family)
		if errors.Is(err, sql.ErrNoRows) {
			family, err = uuid.NewString(), nil
		}
		if err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tokens SET revoked_at=? WHERE revoked_at IS NULL AND `+parents, time.Now().Unix(), cliOAuthClientID, localAppResource, user); err != nil {
		return "", err
	}
	grant := mcpOAuthGrant{FamilyID: family, ClientID: cliOAuthClientID, Resource: localAppResource, UserID: user, Username: "This Mac", Scopes: []string{"messages:read", "messages:send"}}
	// The refresh token is never handed out; it only keeps the family live for
	// child connections while the app is idle.
	access, _, err := issueMCPOAuthPair(ctx, tx, &grant)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	l.family, l.access, l.expires = family, access, grant.Expires
	return access, nil
}
