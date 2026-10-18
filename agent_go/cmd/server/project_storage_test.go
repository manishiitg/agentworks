package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Pin the approved storage boundary once: a location changes neither origin,
// audience, mode, memory nor the owner's launch identity, in either direction.
func TestCanonicalProjectStoragePreservesIdentityAndAccess(t *testing.T) {
	_, ws, ctx := newCrewCreationTestEnv(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work","code"]},{"id":"builder","username":"builder","products":["work","code"]},{"id":"run","username":"run","products":["work","code"]},{"id":"other","username":"other","products":["work","code"]}]}`)
	registry := defaultProjectOwners()
	owners, aliases := map[string]string{}, map[string]string{}
	stubCrewLookups(t, owners, aliases)
	for _, tc := range []struct{ origin, mode string }{{"work", "dev"}, {"code", "dev"}, {"code", "cowork"}, {"code", "local"}} {
		t.Run(tc.origin+"/"+tc.mode, func(t *testing.T) {
			product, _ := projectProductForProfile(tc.origin)
			folder := tc.origin + "-" + tc.mode + "-12345678"
			legacy := workspaceref.PhysicalPathOf("owner", product.ProjectsRoot, folder)
			canonical := workspaceref.SharedProjectPath(folder)
			rec := projectOwnerRecord{Product: tc.origin, Folder: folder, OwnerID: "owner", ProjectID: folder, Shared: true, CanonicalRoot: canonical, Aliases: []string{legacy}, Private: true, AudienceConfigured: true, RunPeople: []string{"run"}, CoOwners: []string{"builder"}}
			if err := registry.Register(rec); err != nil {
				t.Fatal(err)
			}
			owners[canonical], aliases[legacy] = "owner", canonical
			// The editable product field is deliberately wrong: the registry wins.
			ws.files[canonical+"/product.json"] = fmt.Sprintf(`{"product":"untrusted","mode":%q}`, tc.mode)
			ws.files[canonical+"/workflow.json"] = `{"capabilities":{"memory_enabled":true}}`
			for _, path := range []string{canonical, legacy, product.ProjectsRoot + "/" + folder} {
				ref, ok := resolveCrewPath(ctx, "owner", path+"/MEMORY.md")
				gotProduct, gotFolder, known := projectProductForPath(ref.Path())
				if !ok || ref.Root != canonical || ref.Rest != "MEMORY.md" || ref.OwnerID != "owner" || !known || gotProduct.ProfileID != tc.origin || gotFolder != folder {
					t.Fatalf("%s resolved %+v, product %+v", path, ref, gotProduct)
				}
				if costOverviewIsCode(path) != (tc.origin == "code") {
					t.Fatalf("cost origin changed through %s", path)
				}
				if tc.origin == "code" {
					if writes, reads := splitCrewReferenceFolders([]string{path}); len(writes) != 0 || len(reads) != 1 {
						t.Fatalf("Code context gained write access through %s", path)
					}
					if root := common.CodeProjectRoot("owner", path+"/code/script.py"); root != canonical {
						t.Fatalf("private Google account scope through %s = %q", path, root)
					}
					if scope := gmailToolScope(common.CodeProjectRoot("owner", path)); scope.UserID != "owner" || scope.CodeWorkspace != canonical {
						t.Fatalf("Code used company Google scope: %+v", scope)
					}
					for _, suffix := range []string{"", "/product.json", "/workflow.json"} {
						if !codeFilesDeletionProtected(path + suffix) {
							t.Fatalf("Files bypassed Code deletion through %s%s", path, suffix)
						}
					}
				}
			}
			if target, moved := crewAliasForWorkspaceIO("owner", legacy+"/MEMORY.md"); !moved || target != canonical+"/MEMORY.md" {
				t.Fatalf("workspace alias = %q, %v", target, moved)
			}
			if owner := resolveProjectOwner(ctx, canonical); owner != "owner" {
				t.Fatalf("registered owner = %q", owner)
			}
			ref, _ := resolveCrewPath(ctx, "owner", canonical)
			for user, want := range map[string]crewAccessLevel{"owner": crewAccessOwner, "builder": crewAccessOwner, "run": crewAccessReader, "other": crewAccessNone} {
				if tc.mode == "local" && user != "owner" {
					want = crewAccessNone
				}
				if got := crewAccessFor(&UserClaims{UserID: user, Username: user}, ref); got != want {
					t.Fatalf("%s access = %v, want %v", user, got, want)
				}
				if denied := workspaceProxyPathIsOtherUser(canonical+"/MEMORY.md", user); denied != (want != crewAccessOwner) {
					t.Fatalf("%s raw workspace access = %v", user, denied)
				}
			}
			if enabled := workspaceMemoryEnabled(ctx, tc.origin, canonical); enabled != (tc.mode != "local") {
				t.Fatalf("effective memory changed: %v", enabled)
			}
			if tc.origin == "code" {
				in := turnRunAsInput{ProfileID: tc.origin, WorkingFolder: canonical, CallerID: "owner", CLIWorkingDir: "/docs/" + canonical, SharedWorkingDir: "/docs/" + canonical}
				deps := runAsDeps{projectOwner: resolveProjectOwner, slotOf: func(user string) string { return "slot-" + user }}
				if got := decideTurnRunAs(ctx, in, deps); got.User != "owner" || got.Slot != "slot-owner" {
					t.Fatalf("Code launch identity changed: %+v", got)
				}
				in.CallerID, in.ConversationKey = "bot-channel", folder
				bot := &UserClaims{UserID: in.CallerID, Provider: "bot_route", BotRouteProfileID: tc.origin, BotRouteConversationKey: folder, BotRouteWorkspacePath: canonical, BotRouteGrant: "run"}
				botCtx := context.WithValue(ctx, UserContextKey, bot)
				if err := checkProjectLaunchOwner(botCtx, in, deps); (err == nil) != (tc.mode != "local") {
					t.Fatalf("existing scoped channel launch changed: %v", err)
				}
				in.ConversationKey = "other-chat"
				if err := checkProjectLaunchOwner(botCtx, in, deps); err == nil {
					t.Fatal("channel route crossed its conversation grant")
				}
			}
			// Another origin cannot claim the same canonical folder.
			conflict := rec
			if conflict.Product == "work" {
				conflict.Product = "code"
			} else {
				conflict.Product = "work"
			}
			conflict.Aliases = nil
			if err := registry.Register(conflict); err == nil {
				t.Fatal("two origins claimed one folder")
			}
			// A rollback reverses the alias, preserving the same identity/audience.
			if err := registry.modify(func(all map[string]projectOwnerRecord) (bool, error) {
				key := projectOwnerKey(tc.origin, folder)
				current := all[key]
				current.CanonicalRoot, current.Shared = legacy, false
				current.Aliases = append(current.Aliases, canonical)
				all[key] = current
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
			delete(aliases, legacy)
			aliases[canonical], owners[legacy] = legacy, "owner"
			ws.files[legacy+"/product.json"] = ws.files[canonical+"/product.json"]
			ws.files[legacy+"/workflow.json"] = ws.files[canonical+"/workflow.json"]
			if target, moved := crewAliasForWorkspaceIO("owner", canonical+"/MEMORY.md"); !moved || target != legacy+"/MEMORY.md" {
				t.Fatalf("rollback alias = %q, %v", target, moved)
			}
			if owner := resolveProjectOwner(context.Background(), canonical); owner != "owner" {
				t.Fatalf("rollback lost owner: %q", owner)
			}
			rolled, ok := resolveCrewPath(ctx, "run", canonical+"/MEMORY.md")
			if !ok || rolled.Root != legacy || rolled.Shared || rolled.OwnerID != "owner" || rolled.Rest != "MEMORY.md" {
				t.Fatalf("rollback resolution = %+v", rolled)
			}
			if got := crewTurnWorkspace(ctx, "owner", canonical, legacy); got != legacy {
				t.Fatalf("rollback turn retained retired root: %s", got)
			}
			for user, allowed := range map[string]bool{"owner": true, "builder": tc.mode != "local", "run": false, "other": false} {
				for _, root := range []string{canonical, legacy} {
					if denied := workspaceProxyPathIsOtherUser(root+"/MEMORY.md", user); denied == allowed {
						t.Fatalf("rollback raw access for %s through %s: denied=%v", user, root, denied)
					}
				}
			}
		})
	}
}
