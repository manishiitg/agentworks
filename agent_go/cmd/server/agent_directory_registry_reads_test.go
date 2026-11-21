package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// #2980: repeated directory rows must decode a person's registry once, but
// still revalidate every read, observe changes, and never reuse another owner.
func TestDirectoryRegistryReadsKeepCurrentOwnerAndChatScope(t *testing.T) {
	store, files := memoryProductConversationStore()
	ctx, finish := startDirectoryRegistryReads(t.Context())
	defer finish()
	makeRaw := func(session string) string {
		raw, err := json.Marshal(productConversationRegistryDocument{
			Entries: map[string]ProductConversationRecord{
				"work/project:chat:one": {ProfileID: "work", ConversationKey: "project:chat:one", WorkspacePath: "Crew/example", SessionID: session, SelectedSkills: []string{"original"}, CurrentWorkflowRun: &productConversationWorkflowRun{WorkflowID: "workflow", Folder: "original"}},
				"work/other:chat:two":   {ProfileID: "work", ConversationKey: "other:chat:two", WorkspacePath: "Crew/other", SessionID: "other-project"},
			},
			Agents: map[string]ProductAgentIdentity{"work/project:chat:one": {Name: "Named", Examples: []string{"original"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	path := productConversationRegistryPath("owner")
	files[path] = makeRaw("owner-session")
	files[productConversationRegistryPath("other")] = makeRaw("other-owner-session")
	chats, err := store.projectChats(ctx, "owner", "work", "project")
	if err != nil || len(chats) != 1 {
		t.Fatalf("owner chats: %v %v", chats, err)
	}
	chats[0].SelectedSkills[0] = "changed-output"
	chats[0].CurrentWorkflowRun.Folder = "changed-output"
	identities, err := store.agentIdentities(ctx, "owner", "work", "project")
	if err != nil {
		t.Fatal(err)
	}
	identities["project:chat:one"].Examples[0] = "changed-output"
	chats, err = store.projectChats(ctx, "owner", "work", "project")
	if err != nil || chats[0].SessionID != "owner-session" || chats[0].SelectedSkills[0] != "original" || chats[0].CurrentWorkflowRun.Folder != "original" {
		t.Fatalf("snapshot changed: %v %v", chats, err)
	}
	identity, err := store.agentIdentity(ctx, "owner", "work", "project:chat:one")
	if err != nil || identity.Examples[0] != "original" {
		t.Fatalf("identity changed: %v %v", identity, err)
	}
	scope := ctx.Value(directoryRegistryReadsKey{}).(*directoryRegistryReads)
	if scope.decodes != 1 || scope.hits != 3 {
		t.Fatalf("unchanged registry decoded repeatedly: %d decodes / %d hits", scope.decodes, scope.hits)
	}
	other, err := store.projectChats(ctx, "other", "work", "project")
	if err != nil || other[0].SessionID != "other-owner-session" {
		t.Fatalf("owner boundary lost: %v %v", other, err)
	}
	otherProject, err := store.projectChats(ctx, "owner", "work", "other")
	if err != nil || len(otherProject) != 1 || otherProject[0].SessionID != "other-project" {
		t.Fatalf("project boundary lost: %v %v", otherProject, err)
	}
	wrongProfile, err := store.projectChats(ctx, "owner", "different", "project")
	if err != nil || len(wrongProfile) != 0 {
		t.Fatalf("profile boundary lost: %v %v", wrongProfile, err)
	}
	// An ordinary mutation loader must never receive the borrowed maps.
	mutable, err := store.loadDocument(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	delete(mutable.Entries, "work/project:chat:one")
	chats, err = store.projectChats(ctx, "owner", "work", "project")
	if err != nil || len(chats) != 1 {
		t.Fatal("mutation loader reused scoped map", err)
	}
	files[path] = makeRaw("replacement")
	chats, err = store.projectChats(ctx, "owner", "work", "project")
	if err != nil || chats[0].SessionID != "replacement" {
		t.Fatal("within-request change missed", err)
	}
	read := store.read
	store.read = func(context.Context, string) (string, bool, error) { return "", false, errors.New("read denied") }
	if _, err = store.projectChats(ctx, "owner", "work", "project"); err == nil {
		t.Fatal("cached data bypassed read refusal")
	}
	store.read = read
	files[path] = "invalid JSON"
	if _, err = store.projectChats(ctx, "owner", "work", "project"); err == nil {
		t.Fatal("cached data bypassed malformed registry")
	}
	delete(files, path)
	chats, err = store.projectChats(ctx, "owner", "work", "project")
	if err != nil || len(chats) != 0 {
		t.Fatal("deleted chat served", err)
	}
	files[path] = makeRaw("next-request")
	next, done := startDirectoryRegistryReads(t.Context())
	defer done()
	chats, err = store.projectChats(next, "owner", "work", "project")
	if err != nil || chats[0].SessionID != "next-request" {
		t.Fatal("new request reused old registry", err)
	}
}

func BenchmarkDirectoryRegistryProjectReads(b *testing.B) {
	store, files := memoryProductConversationStore()
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{}}
	for project := 0; project < 20; project++ {
		for chat := 0; chat < 20; chat++ {
			key := fmt.Sprintf("project-%d:chat:chat-%d", project, chat)
			doc.Entries["work/"+key] = ProductConversationRecord{ProfileID: "work", ConversationKey: key, SessionID: key, WorkspacePath: fmt.Sprintf("Crew/project-%d", project)}
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		b.Fatal(err)
	}
	files[productConversationRegistryPath("owner")] = string(raw)
	for _, scoped := range []bool{false, true} {
		b.Run(fmt.Sprintf("scoped=%t", scoped), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ctx := context.Background()
				if scoped {
					ctx = context.WithValue(ctx, directoryRegistryReadsKey{}, &directoryRegistryReads{documents: map[string]directoryRegistryRead{}})
				}
				for project := 0; project < 20; project++ {
					if _, err := store.projectChats(ctx, "owner", "work", fmt.Sprintf("project-%d", project)); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.Logf("VERIFY-METRIC: registry_20projects scoped=%t ns_per_list=%d", scoped, b.Elapsed().Nanoseconds()/int64(b.N))
		})
	}
}
