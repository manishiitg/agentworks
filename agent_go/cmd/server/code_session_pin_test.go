package server

import (
	"context"
	"errors"
	"testing"
)

// A Code chat's person is pinned by its first turn: the same person keeps
// using it, anyone else (an editor posting into the owner's chat, a changed
// session owner) is refused, and a sub-agent works for its parent's person.
func TestCodeSessionPersonIsPinnedOnce(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	code := "_users/alice/Chats/Code/projects/x"
	if _, ok, _ := codeSessionPinFor("s1"); ok {
		t.Fatal("unpinned session reported as Code")
	}
	if err := pinCodeSession("s1", "alice", code); err != nil {
		t.Fatal(err)
	}
	if err := pinCodeSession("s1", "alice", code); err != nil {
		t.Fatalf("the same person was refused: %v", err)
	}
	if err := pinCodeSession("s1", "bob", code); !errors.Is(err, errCodeSessionPinnedToAnother) {
		t.Fatalf("another person took the chat: %v", err)
	}
	if err := pinCodeSession("s1", "alice", "_users/alice/Chats/Code/projects/y"); !errors.Is(err, errCodeSessionPinnedToAnother) {
		t.Fatalf("the chat moved to another Code: %v", err)
	}
	if pin, ok, err := codeSessionPinFor("s1"); err != nil || !ok || pin.Person != "alice" || pin.CodeRoot != code {
		t.Fatalf("pin = %+v %v %v", pin, ok, err)
	}
	if err := inheritCodeSessionPin("s1", "s1-sub"); err != nil {
		t.Fatal(err)
	}
	if pin, ok, _ := codeSessionPinFor("s1-sub"); !ok || pin.Person != "alice" {
		t.Fatalf("sub-agent pin = %+v %v", pin, ok)
	}
	if err := inheritCodeSessionPin("not-code", "child"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := codeSessionPinFor("child"); ok {
		t.Fatal("a non-Code parent pinned its child")
	}
}

// Every entry that adds a turn to an existing Code chat (handleQuery before
// live-input delivery, /live-input) refuses anyone but the pinned person,
// and any channel-route principal.
func TestOnlyThePinnedPersonAddsTurnsToACodeChat(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	if err := pinCodeSession("chat", "alice", "_users/alice/Chats/Code/projects/x"); err != nil {
		t.Fatal(err)
	}
	for name, claims := range map[string]*UserClaims{
		"editor":     {UserID: "bob"},
		"no claims":  nil,
		"bot route":  {UserID: "alice", Provider: "bot_route"},
		"empty user": {UserID: ""},
	} {
		if codeSessionTurnRefusal("chat", claims) == "" {
			t.Errorf("%s added a turn to alice's Code chat", name)
		}
	}
	if refusal := codeSessionTurnRefusal("chat", &UserClaims{UserID: "alice"}); refusal != "" {
		t.Fatalf("alice refused in her own chat: %s", refusal)
	}
	if refusal := codeSessionTurnRefusal("crew-chat", &UserClaims{UserID: "bob"}); refusal != "" {
		t.Fatalf("a non-Code chat was refused: %s", refusal)
	}
}

// A deleted session's pin goes with it, and the sweep removes pins of Codes
// that no longer exist while keeping live ones.
func TestCodeSessionPinsAreCleanedUp(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	if err := pinCodeSession("live", "owner", codePrivacyOwnerRoot); err != nil {
		t.Fatal(err)
	}
	if err := pinCodeSession("orphan", "owner", "_users/owner/Chats/Code/projects/deleted-1"); err != nil {
		t.Fatal(err)
	}
	if err := pinCodeSession("deleted-chat", "owner", codePrivacyOwnerRoot); err != nil {
		t.Fatal(err)
	}
	deleteCodeSessionPin("deleted-chat")
	api.sweepOrphanCodeSessionPins(context.Background())
	for session, want := range map[string]bool{"live": true, "orphan": false, "deleted-chat": false} {
		if _, ok, _ := codeSessionPinFor(session); ok != want {
			t.Errorf("%s pinned = %v, want %v", session, ok, want)
		}
	}
}
