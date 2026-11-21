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
			binding := productConversationBinding{ResourceID: "crew", WorkspacePath: root, ManifestPath: root + "/product.json", ProjectMainAddress: true}
			metadata, _ := json.Marshal(productProjectManifest{ID: "crew", Product: "work", Title: "Crew", MainChatKey: key})
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
	binding := productConversationBinding{ConversationKey: "crew:chat:reviewer", ResourceID: "crew", ProjectMainChatKey: "crew:chat:reviewer"}
	if shouldRebindWorkConversation(profile, binding, ProductConversationRecord{SessionID: "current"}, "previous", true) {
		t.Fatal("stale browser retargeted a stable chat ID")
	}
}

func TestSharedCallerLegacyRootKeepsItsOwnMainRole(t *testing.T) {
	store, files := memoryProductConversationStore()
	root := "_users/owner/Chats/Work/projects/crew"
	files[root+"/product.json"] = `{"product":"work","id":"crew","main_chat_key":"crew:chat:owners-main"}`
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
		if chat.IsMainChat != (chat.SessionID == "run-person-main") {
			t.Fatalf("owner pointer changed Run person's role: %+v", chat)
		}
	}
}
