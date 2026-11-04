package server

import "context"

// Only canonical external Ask dispatch creates this provenance. It follows a
// queued turn, but is never accepted from model arguments or a request body.
type crewAskInputScope struct {
	User, Connection, Workspace, Chat, Session, Query string
}
type crewAskInputKey struct{}

func crewAskInputFromContext(ctx context.Context) *crewAskInputScope {
	scope, _ := ctx.Value(crewAskInputKey{}).(*crewAskInputScope)
	return scope
}
