package utils

import (
	"path/filepath"
	"testing"
)

// PLAT-442 step 3: the workspace service reads the _users/<id> segment through workspaceref. Each helper is checked
// with the right user's logical and physical spelling, and with another user's physical path.
func TestUserTreeHelpersBothSpellings(t *testing.T) {
	docs := t.TempDir()
	physical := func(user, rel string) string { return filepath.Join(docs, "_users", user, rel) }

	t.Run("userTreeOwner", func(t *testing.T) {
		for _, tc := range []struct {
			path  string
			owner string
			in    bool
		}{
			{physical("alice", "Chats/x"), "alice", true},
			{physical("bob", "Chats/x"), "bob", true},
			{physical("alice", ""), "alice", true},
			{filepath.Join(docs, "_users"), "", true},
			{filepath.Join(docs, "Chats", "x"), "", false}, // the logical spelling lies in no user's tree
			{filepath.Join(docs, "Workflow", "w"), "", false},
		} {
			owner, in := userTreeOwner(docs, tc.path)
			if owner != tc.owner || in != tc.in {
				t.Errorf("userTreeOwner(%q) = %q,%v, want %q,%v", tc.path, owner, in, tc.owner, tc.in)
			}
		}
	})

	t.Run("ConvertToUserRelativePath", func(t *testing.T) {
		for _, owner := range []string{"alice", "bob"} {
			got, err := ConvertToUserRelativePath(physical(owner, "Chats/s.json"), docs)
			if err != nil || got != "Chats/s.json" {
				t.Errorf("%s physical: %q %v", owner, got, err)
			}
		}
		if got, _ := ConvertToUserRelativePath(filepath.Join(docs, "Chats", "s.json"), docs); got != "Chats/s.json" {
			t.Errorf("logical spelling changed: %q", got)
		}
		if got, _ := ConvertToUserRelativePath(physical("alice", ""), docs); got != "" {
			t.Errorf("bare user folder: %q", got)
		}
	})

	t.Run("ResolveUserPath", func(t *testing.T) {
		// The right user's logical spelling resolves into their own tree; the same user's physical spelling to the
		// same place.
		for _, spelling := range []string{"Chats/p/x", physical("alice", "Chats/p/x")} {
			got, err := ResolveUserPath(docs, spelling, "alice")
			if err != nil || got != physical("alice", "Chats/p/x") {
				t.Errorf("alice %q -> %q %v", spelling, got, err)
			}
		}
		// A logical spelling by another user never lands in alice's tree.
		got, err := ResolveUserPath(docs, "Chats/p/x", "bob")
		if err != nil || got != physical("bob", "Chats/p/x") {
			t.Errorf("bob logical -> %q %v", got, err)
		}
		// Escapes stay refused.
		if _, err := ResolveUserPath(docs, "Chats/../../outside", "alice"); err == nil {
			t.Error("an escaping path resolved")
		}
	})
}
