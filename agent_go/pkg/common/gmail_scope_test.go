package common

import (
	"context"
	"testing"
)

func TestGmailScopeFailsClosed(t *testing.T) {
	withSession := func(id string) context.Context {
		ctx := context.WithValue(context.Background(), UserIDKey, "alice")
		if id != "" {
			ctx = context.WithValue(ctx, ChatSessionIDKey, id)
		}
		return ctx
	}
	if _, _, err := GmailScopeFromContext(withSession("")); err == nil {
		t.Fatal("a call with no session got a scope")
	}
	if _, _, err := GmailScopeFromContext(withSession("gmail-scope-unknown")); err == nil {
		t.Fatal("a session with no configuration got the shared scope")
	}

	// A marked Code session keeps its Code even after its shell config is
	// cleared (delegation, restart).
	root := "_users/alice/Chats/Code/projects/app-1"
	MarkCodeSession("gmail-scope-code", root)
	defer codeSessionRoots.Delete("gmail-scope-code")
	if got, user, err := GmailScopeFromContext(withSession("gmail-scope-code")); err != nil || got != root || user != "alice" {
		t.Fatalf("Code scope = %q %q %v", got, user, err)
	}

	// Children inherit it through a copied folder guard.
	SetSessionFolderGuard("gmail-scope-code", []string{root}, []string{root})
	defer ClearSessionShellConfig("gmail-scope-code")
	CopySessionFolderGuard("gmail-scope-code", "gmail-scope-child")
	defer ClearSessionShellConfig("gmail-scope-child")
	defer codeSessionRoots.Delete("gmail-scope-child")
	if got, _, err := GmailScopeFromContext(withSession("gmail-scope-child")); err != nil || got != root {
		t.Fatalf("child scope = %q %v", got, err)
	}

	// A configured non-Code session is shared scope.
	SetSessionWorkingDir("gmail-scope-crew", "_users/alice/Chats/Work/projects/crew-1")
	defer ClearSessionShellConfig("gmail-scope-crew")
	if got, _, err := GmailScopeFromContext(withSession("gmail-scope-crew")); err != nil || got != "" {
		t.Fatalf("Crew scope = %q %v", got, err)
	}
}
