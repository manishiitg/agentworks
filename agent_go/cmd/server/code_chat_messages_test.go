package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// Code messaging discovers only the owner's main and sibling chats, never
// isolated function/schedule conversations or another Code.
func TestCodeMessagingListsOnlySiblingChats(t *testing.T) {
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

	env := newCrewFunctionEnv(t)
	const codePath = "_users/owner/Chats/Code/projects/private-alpha"
	env.mock.mu.Lock()
	env.mock.files[codePath+"/product.json"] = `{"schema_version":1,"product":"code","id":"alpha","title":"Private project","session_id":"code-alpha"}`
	env.mock.mu.Unlock()
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	_, err = crewTriggerLinkCaller(codePath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	main := codeChat{Key: "alpha", ID: "main", Name: "Main chat", SessionID: "s-main"}
	side := codeChat{Key: "alpha:chat:aa", ID: "aa", Name: "Chat 2", SessionID: "s-aa"}
	project := func(self codeChat) codeChatProject {
		return codeChatProject{OwnerID: "owner", ProjectID: "alpha", Workspace: codePath, Chats: []codeChat{main, side}, Self: self}
	}
	if _, err := project(main).find("p2"); err == nil {
		t.Fatal("a chat outside this Code was found")
	}
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", main.SessionID, QueryRequest{SelectedFolder: codePath}, crewTriggerLinkCaller(codePath), nil); err != nil {
		t.Fatal(err)
	}
	if _, old := reg.tools["return_function_result"]; old {
		t.Fatal("obsolete result tool registered")
	}
	if _, exists := reg.tools["send_message"]; !exists {
		t.Fatal("explicit messaging tool absent")
	}
}
