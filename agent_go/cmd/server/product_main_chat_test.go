package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
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
			metadata, _ := json.Marshal(productProjectManifest{ID: "crew", Product: profileID, Title: "Crew", SessionID: "original-session", Schedules: []productschedule.Schedule{{ID: "scheduled-review", ChatKey: "crew:chat:reviewer"}, {ID: "watcher", Condition: &conditiontrigger.Config{Target: conditiontrigger.Target{Kind: "agent", ChatID: "reviewer"}}}}})
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
			var metadataAfter productProjectManifest
			if err := json.Unmarshal([]byte(files[binding.ManifestPath]), &metadataAfter); err != nil {
				t.Fatal(err)
			}
			if metadataAfter.Schedules[0].ChatKey != "crew:chat:reviewer" || metadataAfter.Schedules[1].Condition.Target.ChatID != "reviewer" {
				t.Fatal("main pointer retargeted a schedule or watcher")
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

func TestMissingMainPointerRepairsOnlyThisCallersOrdinaryChats(t *testing.T) {
	for _, rootSurvives := range []bool{true, false} {
		t.Run(fmt.Sprint(rootSurvives), func(t *testing.T) {
			store, files := memoryProductConversationStore()
			ctx := context.Background()
			profile := routeTestProfile("work", true, "")
			root := "_users/u/Chats/Work/projects/crew"
			binding := productConversationBinding{ResourceID: "crew", WorkspacePath: root, ManifestPath: root + "/product.json", ProjectMainAddress: true}
			files[binding.ManifestPath] = `{"product":"work","id":"crew","main_chat_key":"crew:chat:missing","session_id":"lost"}`
			doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{}}
			for key, updated := range map[string]string{"crew:chat:old": "2026-01-01", "crew:chat:new": "2026-10-19", "crew:chat:fn.run": "9999", "other:chat:foreign": "9999"} {
				doc.Entries["work/"+key] = ProductConversationRecord{ProfileID: "work", ConversationKey: key, ConversationID: key, SessionID: "s-" + key, WorkspacePath: root, UpdatedAt: updated}
			}
			want := "crew:chat:new"
			if rootSurvives {
				doc.Entries["work/crew"] = ProductConversationRecord{ProfileID: "work", ConversationKey: "crew", ConversationID: "original", SessionID: "s-original", WorkspacePath: root}
				want = originalMainChatKey("crew", "original")
			}
			if err := store.writeDocument(ctx, productConversationRegistryPath("u"), doc); err != nil {
				t.Fatal(err)
			}
			resolved, err := store.resolveProjectMainAddress(ctx, "u", profile, binding)
			if err != nil || resolved.ConversationKey != want {
				t.Fatalf("repair = %+v, %v; want %s", resolved, err, want)
			}
			var manifest productProjectManifest
			if err := json.Unmarshal([]byte(files[binding.ManifestPath]), &manifest); err != nil || manifest.MainChatKey != want {
				t.Fatalf("repaired pointer was not saved: %+v %v", manifest, err)
			}
		})
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
