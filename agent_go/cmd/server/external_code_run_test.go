package server

import (
	"testing"
)

// A Code tab is an agent with a name: it is found by id or by name in any case, a name two tabs share finds nothing, and a
// name already used in the project is refused (owner 2026-10-16).
func TestCodeTabsAreFoundByNameAndNamesAreUnique(t *testing.T) {
	chats := []codeChat{{ID: "main", Name: "Main chat"}, {ID: "a1b2", Name: "Cowork"}, {ID: "c3d4", Name: "Reviewer"}, {ID: "e5f6", Name: "reviewer"}}
	if c := findCodeChat(chats, "COWORK"); c == nil || c.ID != "a1b2" {
		t.Fatalf("a tab was not found by its name in another case: %+v", c)
	}
	if c := findCodeChat(chats, "c3d4"); c == nil || c.Name != "Reviewer" {
		t.Fatalf("a tab was not found by its id: %+v", c)
	}
	if findCodeChat(chats, "Reviewer") != nil {
		t.Fatal("a name shared by two tabs must not pick one")
	}
	if !codeChatNameTaken(chats, "cowork", "") || codeChatNameTaken(chats, "Cowork", "a1b2") || codeChatNameTaken(chats, "Planner", "") {
		t.Fatal("name uniqueness is wrong")
	}
}
