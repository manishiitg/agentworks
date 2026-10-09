package server

import (
	"context"
	"testing"
	"time"
)

// Two asks into one chat must take turns (the second used to join the running turn and fail "was not registered"); asks into
// different chats must not wait for each other.
func TestCrewAsksIntoOneChatTakeTurnsAndDifferentChatsDoNot(t *testing.T) {
	first, ok := acquireCrewAskChat(context.Background(), "chat-a")
	if !ok {
		t.Fatal("the first ask must get the chat")
	}
	waiting := make(chan bool, 1)
	go func() {
		release, ok := acquireCrewAskChat(context.Background(), "chat-a")
		if ok {
			release()
		}
		waiting <- ok
	}()
	select {
	case <-waiting:
		t.Fatal("a second ask into the same chat must wait for the first")
	case <-time.After(150 * time.Millisecond):
	}
	other, ok := acquireCrewAskChat(context.Background(), "chat-b")
	if !ok {
		t.Fatal("another chat must not wait")
	}
	other()
	first()
	select {
	case ok := <-waiting:
		if !ok {
			t.Fatal("the waiting ask must get the chat once the first is done")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting ask never got the chat")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	held, _ := acquireCrewAskChat(context.Background(), "chat-c")
	defer held()
	if _, ok := acquireCrewAskChat(ctx, "chat-c"); ok {
		t.Fatal("an ask whose time runs out while waiting must give up, not take the chat")
	}
}
