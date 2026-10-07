package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// PLAT-648: only a Code's main chat and side chats are siblings (never its
// isolated trigger/schedule chats or another Code), and a chain of messages
// can never come back to a chat already in it or grow past the hop limit.
func TestCodeChatSiblingsAndLoopGuard(t *testing.T) {
	files := map[string]string{}
	store := productConversationRegistryStore{
		read: func(_ context.Context, path string) (string, bool, error) {
			raw, ok := files[path]
			return raw, ok, nil
		},
		write: func(_ context.Context, path, content string) error { files[path] = content; return nil },
		now:   time.Now, newID: func() string { return "x" },
	}
	entries := map[string]ProductConversationRecord{}
	for _, key := range []string{"p1", "p1:chat:aa", "p1:chat:bb", "p1:trigger:t1", "p1:chat:aa:x", "p2", "p2:chat:cc"} {
		entries["code/"+key] = ProductConversationRecord{ConversationKey: key, SessionID: "s-" + key}
	}
	entries["work/p1:chat:zz"] = ProductConversationRecord{ConversationKey: "p1:chat:zz", SessionID: "s-work"}
	raw, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: entries})
	files[productConversationRegistryPath("u1")] = string(raw)
	chats, err := store.projectChats(context.Background(), "u1", "code", "p1")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, chat := range chats {
		keys = append(keys, chat.ConversationKey)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "p1,p1:chat:aa,p1:chat:bb" {
		t.Fatalf("siblings = %s", got)
	}

	main := codeChat{Key: "p1", ID: "main", Name: "Main chat", SessionID: "s-main"}
	a := codeChat{Key: "p1:chat:aa", ID: "aa", Name: "Chat 2", SessionID: "s-a"}
	b := codeChat{Key: "p1:chat:bb", ID: "bb", Name: "Chat 3", SessionID: "s-b"}
	project := func(self codeChat) codeChatProject {
		return codeChatProject{OwnerID: "u1", ProjectID: "p1", Self: self, Chats: []codeChat{main, a, b}}
	}
	now := time.Now()
	if _, _, err := admitCodeChatMessage(project(main), main, now); err == nil {
		t.Fatal("a chat messaged itself")
	}
	_, releaseA, err := admitCodeChatMessage(project(main), a, now)
	if err != nil {
		t.Fatal(err)
	}
	// While main's message runs in a, a may not message main back.
	if _, _, err := admitCodeChatMessage(project(a), main, now); err == nil || !strings.Contains(err.Error(), "already took part") {
		t.Fatalf("echo back to the sender allowed: %v", err)
	}
	_, releaseB, err := admitCodeChatMessage(project(a), b, now)
	if err != nil {
		t.Fatal(err)
	}
	// main -> a -> b is the hop limit: b may not pass it on.
	if _, _, err := admitCodeChatMessage(project(b), codeChat{Key: "p1:chat:cc", Name: "Chat 4", SessionID: "s-c"}, now); err == nil {
		t.Fatal("chain grew past the hop limit")
	}
	releaseB()
	releaseA()
	// Once the turns finish, a fresh exchange may start, up to the rate
	// limit (two of which were used above).
	for i := 2; i < codeChatRateLimit; i++ {
		_, release, err := admitCodeChatMessage(project(a), main, now)
		if err != nil {
			t.Fatalf("send %d refused: %v", i, err)
		}
		release()
	}
	if _, _, err := admitCodeChatMessage(project(a), main, now); err == nil {
		t.Fatal("rate limit not applied")
	}
	codeChatRelay.Lock()
	delete(codeChatRelay.sent, "u1/p1")
	codeChatRelay.Unlock()
}
