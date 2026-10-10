package knowledgebase

import (
	"context"
	"testing"
)

// A member who is not an administrator has no folder to save into in a new Brain; the default folder fixes that once, for everyone,
// and a later revoke by an Owner is not undone.
func TestDefaultFolderIsOfferedOnceToEveryone(t *testing.T) {
	s, admin, member := fixture(t, false)
	people := []Identity{{ID: "admin", Name: "Admin", Type: "user"}, {ID: "priya", Name: "Priya", Type: "user"}, {ID: "editor", Name: "Editor", Type: "user"}}
	if err := s.EnsureDefaultFolder(context.Background(), admin, "Shared", "Editor", people); err != nil {
		t.Fatal(err)
	}
	// Priya can now save into it; before, she had no folder at all.
	create(t, s, member, "Shared", "note.md", "hello", "n1")

	// Owner revokes Priya; running again (a new person arrives) does not give it back.
	call(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "revoke", "folder_path": "Shared", "identity_id": "priya", "request_id": "r1",
		"expected_acl_version": call(t, s, admin, "get_knowledgebase_access", map[string]any{"folder_path": "Shared"})["acl_version"]})
	people = append(people, Identity{ID: "newbie", Name: "Newbie", Type: "user"})
	if err := s.SyncPlatformIdentities(context.Background(), people); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDefaultFolder(context.Background(), admin, "Shared", "Editor", people); err != nil {
		t.Fatal(err)
	}
	code(t, s, member, "create_knowledgebase", map[string]any{"folder_path": "Shared", "filename": "again.md", "type": "note", "title": "t", "content": "x", "request_id": "n2"}, "NOT_FOUND")
	create(t, s, Principal{IdentityID: "newbie"}, "Shared", "mine.md", "hi", "n3")
}
