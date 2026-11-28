package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// PLAT-746-885: opening a project must never resurrect or replace a deleted Main.
func TestProjectMainResolutionNeverCreatesOrRepairsAnAgent(t *testing.T) {
	for _, key := range []string{"", "crew:chat:missing", "crew:chat:existing"} {
		t.Run(key, func(t *testing.T) {
			store, files := memoryProductConversationStore()
			ctx := context.Background()
			root := "_users/u/Chats/Work/projects/crew"
			binding := productConversationBinding{ResourceID: "crew", WorkspacePath: root, ManifestPath: root + "/product.json", ProjectDefaultAddress: true}
			metadata, _ := json.Marshal(productProjectManifest{ID: "crew", Product: "work", Title: "Crew", DefaultAgentKey: key})
			files[binding.ManifestPath] = string(metadata)
			doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{
				"work/crew:chat:existing": {ProfileID: "work", ConversationKey: "crew:chat:existing", ConversationID: "existing", SessionID: "saved-session", WorkspacePath: root},
			}}
			if err := store.writeDocument(ctx, productConversationRegistryPath("u"), doc); err != nil {
				t.Fatal(err)
			}
			before := files[productConversationRegistryPath("u")]
			result, err := store.resolveProjectMainAddress(ctx, "u", routeTestProfile("work", true, ""), binding)
			if key == "crew:chat:existing" {
				if err != nil || result.ConversationKey != key {
					t.Fatalf("saved agent not resolved: %+v %v", result, err)
				}
			} else if !errors.Is(err, errChooseProjectAgent) {
				t.Fatalf("missing agent must require a choice: %v", err)
			}
			if files[binding.ManifestPath] != string(metadata) || files[productConversationRegistryPath("u")] != before {
				t.Fatal("resolution changed saved agents or project")
			}
		})
	}
}

func TestFixedMainRecordRejectsProtectedOrForeignChats(t *testing.T) {
	workspace := "_users/u/Chats/Work/projects/crew"
	for _, key := range []string{"other:chat:reviewer", "crew:chat:fn.run", "crew:chat:agent-id"} {
		record := ProductConversationRecord{ProfileID: "work", ConversationKey: key, SessionID: "session", WorkspacePath: workspace}
		if validProjectMainRecord("u", "work", "crew", workspace, record) {
			t.Fatalf("accepted protected or foreign main record %q", key)
		}
	}
	for _, record := range []ProductConversationRecord{
		{ProfileID: "code", ConversationKey: "crew:chat:root-a", SessionID: "session", WorkspacePath: workspace},
		{ProfileID: "work", ConversationKey: "crew:chat:root-a", SessionID: "session", WorkspacePath: "_users/another/Chats/Work/projects/crew"},
	} {
		if validProjectMainRecord("u", "work", "crew", workspace, record) {
			t.Fatal("accepted another profile's or caller's chat")
		}
	}
}

func TestStableProjectChatCannotBeReboundByAStaleBrowserSession(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	binding := productConversationBinding{ConversationKey: "crew:chat:reviewer", ResourceID: "crew", ProjectDefaultAgentKey: "crew:chat:reviewer"}
	if shouldRebindWorkConversation(profile, binding, ProductConversationRecord{SessionID: "current"}, "previous", true) {
		t.Fatal("stale browser retargeted a stable chat ID")
	}
}

// The owner's default-agent pointer names the owner's chat; it never makes a Run person's own chats the default
// agent, and an old project root is not the default agent on its own (only the saved pointer says so).
func TestOwnerDefaultAgentPointerNeverMarksRunPersonChats(t *testing.T) {
	store, files := memoryProductConversationStore()
	root := "_users/owner/Chats/Work/projects/crew"
	files[root+"/product.json"] = `{"product":"work","id":"crew","default_agent_key":"crew:chat:owners-main"}`
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{
		"work/crew":           {ProfileID: "work", ConversationKey: "crew", SessionID: "run-person-main", WorkspacePath: root},
		"work/crew:chat:mine": {ProfileID: "work", ConversationKey: "crew:chat:mine", SessionID: "run-person-side", WorkspacePath: root},
	}}
	if err := store.writeDocument(context.Background(), productConversationRegistryPath("run-person"), doc); err != nil {
		t.Fatal(err)
	}
	chats, err := store.projectChats(context.Background(), "run-person", "work", "crew")
	if err != nil {
		t.Fatal(err)
	}
	for _, chat := range chats {
		if chat.IsDefaultAgent {
			t.Fatalf("owner pointer changed Run person's role: %+v", chat)
		}
	}
}

// A project saved before #4688 kept its default agent as main_chat_key; reading it must keep that agent (one-off
// migration until every server has rewritten its projects).
func TestProjectManifestReadsOldDefaultAgentKey(t *testing.T) {
	for _, tc := range []struct{ saved, want string }{
		{`{"id":"p","main_chat_key":"p:chat:old"}`, "p:chat:old"},
		{`{"id":"p","main_chat_key":"p:chat:old","default_agent_key":"p:chat:new"}`, "p:chat:new"},
	} {
		var m productProjectManifest
		if err := json.Unmarshal([]byte(tc.saved), &m); err != nil || m.DefaultAgentKey != tc.want || m.ID != "p" {
			t.Fatalf("%s: got %q id=%q err=%v, want %q", tc.saved, m.DefaultAgentKey, m.ID, err, tc.want)
		}
	}
}
