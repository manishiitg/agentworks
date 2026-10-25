package server

import "testing"

// An owner's "as a user" side chat runs as a Run-mode ask; nobody else's chat, and not the main chat, changes.
func TestUserViewChatPinsTheOwnersTurnToRunMode(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	crew := registerTestCrew(t, "demo-12345678", "alice", true)
	const asUser, builder = "p1:chat:asuser-ab12cd34", "p1:chat:ab12cd34"
	if !isUserViewConversationKey(asUser) || isUserViewConversationKey(builder) || isUserViewConversationKey("p1") {
		t.Fatal("only a side chat whose id starts with asuser- is a user view")
	}
	pinned := QueryRequest{}
	applyUserViewChat(&pinned, "alice", asUser, crew)
	if !pinned.PinRunMode || pinned.CrewGuestCaller != "alice" || !pinned.CrewAskReadOnly {
		t.Fatalf("owner's user view was not pinned to Run mode: %+v", pinned)
	}
	for name, tc := range map[string]struct{ user, key string }{"owner's builder side chat": {"alice", builder}, "another person": {"bob", asUser}} {
		got := QueryRequest{}
		applyUserViewChat(&got, tc.user, tc.key, crew)
		if got.PinRunMode || got.CrewGuestCaller != "" || got.CrewAskReadOnly {
			t.Fatalf("%s changed: %+v", name, got)
		}
	}
}
