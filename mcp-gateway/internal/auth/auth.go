// Package auth validates gateway callers.
//
// M0 accepts a single static bearer token behind the Authenticator interface.
// M1/M2 replace it with shared-IdP token validation (see the M0 auth spike in
// the product brief) without changing the MCP layer.
package auth

import (
	"context"
	"errors"
)

// Identity is the authenticated caller resolved on every request.
type Identity struct {
	UserID      string
	WorkspaceID string
	Email       string
}

// Authenticator resolves a bearer token to an Identity.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Identity, error)
}

var ErrUnauthenticated = errors.New("unauthenticated")

// StaticToken is the M0 authenticator: one token, one identity.
type StaticToken struct {
	Token    string
	Identity Identity
}

func (s StaticToken) Authenticate(_ context.Context, token string) (Identity, error) {
	if token == "" || token != s.Token {
		return Identity{}, ErrUnauthenticated
	}
	return s.Identity, nil
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
