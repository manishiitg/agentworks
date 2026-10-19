package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestMainPointerMigrationAndChangesKeepChatIdentities(t *testing.T) {
	for _, profileID := range []string{"work", "code"} {
		t.Run(profileID, func(t *testing.T) {
			store, files := memoryProductConversationStore()
			ctx := context.Background()
			root := "_users/u/Chats/Work/projects/crew"
			profile := routeTestProfile(profileID, true, "")
			binding := productConversationBinding{ConversationKey: "crew", ResourceID: "crew", WorkspacePath: root, ManifestPath: root + "/product.json", ProjectMainAddress: true}
			metadata, _ := json.Marshal(productProjectManifest{ID: "crew", Product: profileID, Title: "Crew", SessionID: "original-session"})
			files[binding.ManifestPath] = string(metadata)
			legacy, review := profileID+"/crew", profileID+"/crew:chat:reviewer"
			doc := productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{
				legacy: {ProfileID: profileID, ConversationKey: "crew", ConversationID: "original-conversation", SessionID: "original-session", ResourceID: "crew", WorkspacePath: root, ModelID: "original-runtime"},
				review: {ProfileID: profileID, ConversationKey: "crew:chat:reviewer", ConversationID: "review-conversation", SessionID: "review-session", ResourceID: "crew", WorkspacePath: root, ModelID: "review-runtime"},
			}, Agents: map[string]ProductAgentIdentity{legacy: {Name: "Lead", Engine: "codex-cli", ModelID: "own-original"}, review: {Name: "Reviewer", Purpose: "Review", Position: 3, Engine: "muse-cli", ModelID: "own-review"}}, Previous: map[string][]ProductConversationRecord{legacy: {{ConversationKey: "crew", SessionID: "prior-original"}}}}
			if err := store.writeDocument(ctx, productConversationRegistryPath("u"), doc); err != nil {
				t.Fatal(err)
			}
			before := files[productConversationRegistryPath("u")]
			resolved, err := store.resolveProjectMainAddress(ctx, "u", profile, binding)
			if err != nil {
				t.Fatal(err)
			}
			physical := originalMainChatKey("crew", "original-conversation")
			if resolved.ConversationKey != physical || files[productConversationRegistryPath("u")+".before-chat-ids.json"] != before {
				t.Fatal("legacy migration lost its stable ID or registry backup")
			}
			migrated := files[productConversationRegistryPath("u")]
			change, err := store.setMainChat(ctx, "u", profile, resolved, "crew:chat:reviewer", "original-session", "review-session", func(string, string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if change.Main.ConversationKey != "crew:chat:reviewer" || change.PreviousMain.ConversationKey != physical || change.PreviousMain.SessionID != "original-session" {
				t.Fatalf("changed a chat's identity: %+v", change)
			}
			if files[productConversationRegistryPath("u")] != migrated {
				t.Fatal("moving main changed registry entries, identities, models or histories")
			}
			current, err := store.resolveProjectMainAddress(ctx, "u", profile, binding)
			if err != nil || current.ConversationKey != "crew:chat:reviewer" {
				t.Fatalf("main alias did not follow pointer: %+v %v", current, err)
			}
			_, err = store.setMainChat(ctx, "u", profile, current, physical, "review-session", "original-session", func(string, string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if files[productConversationRegistryPath("u")] != migrated || files[productConversationRegistryPath("u")+".before-chat-ids.json"] != before {
				t.Fatal("second pointer move rewrote chat data or the backup")
			}
			saved, err := store.loadDocument(ctx, productConversationRegistryPath("u"))
			if err != nil {
				t.Fatal(err)
			}
			if _, legacyLeft := saved.Entries[legacy]; legacyLeft {
				t.Fatal("root address still owns chat contents")
			}
			if saved.Agents[review].Name != "Reviewer" || saved.Agents[review].ModelID != "own-review" || saved.Agents[review].Position != 3 || saved.Previous[profileID+"/"+physical][0].SessionID != "prior-original" {
				t.Fatal("pointer moves changed named callers or models/history")
			}
		})
	}
}

func TestMainPointerRejectsProtectedKeysAndBusyOrStaleRequests(t *testing.T) {
	store, files := memoryProductConversationStore()
	ctx := context.Background()
	profile := routeTestProfile("work", true, "")
	binding := productConversationBinding{ResourceID: "crew", ManifestPath: "manifest", WorkspacePath: "_users/u/Chats/Work/projects/crew"}
	for _, key := range []string{"other:chat:reviewer", "crew:chat:fn.run", "crew:chat:agent-id", "crew"} {
		if _, err := store.setMainChat(ctx, "u", profile, binding, key, "main", "target", nil); !errors.Is(err, errInvalidMainChat) {
			t.Fatalf("accepted protected key %q: %v", key, err)
		}
	}
	files[binding.ManifestPath] = `{"product":"work","id":"crew","main_chat_key":"crew:chat:root-a","session_id":"main"}`
	doc := productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{
		"work/crew:chat:root-a":   {ProfileID: "work", ConversationKey: "crew:chat:root-a", SessionID: "main", WorkspacePath: binding.WorkspacePath},
		"work/crew:chat:reviewer": {ProfileID: "work", ConversationKey: "crew:chat:reviewer", SessionID: "target", WorkspacePath: binding.WorkspacePath},
	}}
	if err := store.writeDocument(ctx, productConversationRegistryPath("u"), doc); err != nil {
		t.Fatal(err)
	}
	before := files[binding.ManifestPath]
	for _, user := range []string{"u", "another-person"} {
		if _, err := store.setMainChat(ctx, user, profile, binding, "crew:chat:reviewer", "stale", "target", nil); !errors.Is(err, errMainChatChanged) {
			t.Fatal("accepted stale or another person's chat", err)
		}
	}
	if _, err := store.setMainChat(ctx, "u", profile, binding, "crew:chat:reviewer", "main", "target", func(string, string) error { return errMainChatBusy }); !errors.Is(err, errMainChatBusy) {
		t.Fatal(err)
	}
	if files[binding.ManifestPath] != before {
		t.Fatal("refusal moved main")
	}

}

func TestStableProjectChatCannotBeReboundByAStaleBrowserSession(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	binding := productConversationBinding{ConversationKey: "crew:chat:reviewer", ResourceID: "crew", ProjectMainChatKey: "crew:chat:reviewer"}
	if shouldRebindWorkConversation(profile, binding, ProductConversationRecord{SessionID: "current"}, "previous", true) {
		t.Fatal("stale browser retargeted a stable chat ID")
	}
}
