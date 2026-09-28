package server

import (
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
