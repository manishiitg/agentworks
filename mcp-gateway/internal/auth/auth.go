// Package auth validates gateway callers.
//
// MCP clients authenticate with OAuth access tokens issued by the shared
// authorization server (same package AgentWorks uses). Humans approve consent
// through a session validated by HumanSession — M0 accepts a static token,
// M1/M2 validate the shared-IdP session here without changing the MCP layer.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcpoauth"
)

// Identity is the authenticated caller resolved on every request.
type Identity struct {
	UserID      string
	WorkspaceID string
	Email       string
	// ViaGroup is set when the caller authenticated with a group API key:
	// only that group's grants apply, never the key's own (nonexistent) user.
	ViaGroup string
}

// Authenticator resolves a bearer token to an Identity.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Identity, error)
}

var ErrUnauthenticated = errors.New("unauthenticated")

// OAuth validates MCP client tokens against the shared authorization server.
// M0 maps every valid grant into the single workspace; M1 provisions users
// and resolves workspace membership here.
//
// Group API keys (gwk_…) authenticate without the authorization server: the
// key carries exactly its group's grants, so it can be shared with someone
// outside the workspace login.
type OAuth struct {
	Server      *mcpoauth.Server
	WorkspaceID string
	Keys        *store.MemoryStore
}

func (o OAuth) Authenticate(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthenticated
	}
	if strings.HasPrefix(token, "gwk_") && o.Keys != nil {
		k, ok := o.Keys.APIKeyByToken(token)
		if !ok || k.WorkspaceID != o.WorkspaceID {
			return Identity{}, ErrUnauthenticated
		}
		o.Keys.TouchAPIKey(token)
		return Identity{UserID: "key:" + k.ID, WorkspaceID: k.WorkspaceID, ViaGroup: k.GroupID}, nil
	}
	grant, err := o.Server.AuthenticateRequest(ctx, token)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	return Identity{UserID: grant.UserID, WorkspaceID: o.WorkspaceID, Email: grant.Email}, nil
}

// HumanSession validates the logged-in human behind consent and connection
// management. M0 compares a static bearer token; the shared IdP plugs in here.
type HumanSession struct {
	Token string
	User  mcpoauth.User
}

// CurrentUser adapts the session to the authorization server hook.
func (h HumanSession) CurrentUser(r *http.Request) (*mcpoauth.User, bool) {
	if h.Token == "" {
		return nil, false
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != h.Token {
		return nil, false
	}
	u := h.User
	return &u, true
}

// contextKey carries the Identity through request and MCP handler contexts.
type contextKey struct{}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	return id, ok
}
