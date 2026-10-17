package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNonOwnerCodeUsesIsolatedBridgeRuntime(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	api := f.API
	registry := defaultProjectOwners()
	withProjectSharing(t, true)
	root := f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)
	if err := registry.SetRunPeople("code", fixtureCodeFolder, []string{fixtureUserB}); err != nil {
		t.Fatal(err)
	}
	for _, co := range []bool{false, true} {
		if co {
			if _, err := registry.AddCoOwner("code", fixtureCodeFolder, fixtureUserB); err != nil {
				t.Fatal(err)
			}
		}
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: fixtureUserB, Username: fixtureUserB})
		req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "code", AgentProfileConversationKey: fixtureCodeID, SelectedFolder: root}
		profile, err := api.resolveAgentProfileForQuery(ctx, &req, fixtureUserB, "qa-code")
		if err != nil {
			t.Fatal(err)
		}
		if !profile.IsolateCodeCLI || profile.Definition.Runtime.AgentTools.Mode != "mcp_only" {
			t.Fatalf("co-owner=%v inherited native owner tools: %+v", co, profile.Definition.Runtime.AgentTools)
		}
		cli, source, err := turnCLIWorkingDir("code", false, req.SelectedFolder, fixtureUserB, "qa-code", "codex-cli", "", !co, profile.IsolateCodeCLI)
		if err != nil {
			t.Fatal(err)
		}
		if cli == source {
			t.Fatal("non-owner launched in owner source folder")
		}
		got := decideTurnRunAs(ctx, turnRunAsInput{ProfileID: "code", WorkingFolder: req.SelectedFolder, CallerID: fixtureUserB, CLIWorkingDir: cli, SharedWorkingDir: source}, runAsDeps{projectOwner: func(context.Context, string) string { return fixtureUserA }, slotOf: func(string) string { return "owner-slot" }})
		if got.User != "" || got.Slot != "" {
			t.Fatalf("non-owner inherited owner slot: %+v", got)
		}
		if target, err := os.Readlink(filepath.Join(cli, "project")); err != nil || target != source {
			t.Fatalf("isolated project link: %s %v", target, err)
		}
		local := req
		local.CodeChatMode = "local"
		if _, err := api.resolveAgentProfileForQuery(ctx, &local, fixtureUserB, "qa-code"); err == nil {
			t.Fatal("non-owner selected Local mode")
		}
	}
}

func TestProductTriggerRechecksShareAndLegacyAccountIdentity(t *testing.T) {
	fx := newCrewRunModeFixture(t)
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	ref, ok := resolveCrewPath(context.Background(), "owner", crewRunModeOwnerRoot)
	if !ok {
		t.Fatal("fixture root missing")
	}
	_, folder, _ := projectProductForPath(ref.Root)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: "owner", ProjectID: "crew-aaa", Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetRunPeople("work", folder, []string{"reader"}); err != nil {
		t.Fatal(err)
	}
	svc := NewProductScheduleService(nil, fx.api.agentProfiles)
	if _, _, _, _, err := svc.findInternalProductTrigger(context.Background(), "reader", "work", "crew-aaa", "trig-internal"); err != nil {
		t.Fatalf("shared bound trigger refused: %v", err)
	}
	if err := registry.SetRunPeople("work", folder, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := svc.findInternalProductTrigger(context.Background(), "reader", "work", "crew-aaa", "trig-internal"); err == nil {
		t.Fatal("bound trigger survived Share revocation")
	}
	if currentProductCallerHasAccess(context.Background(), "reader", crewRunModeOwnerRoot) {
		t.Fatal("queued delivery kept revoked share")
	}
	claims := currentProjectCallerClaims(context.Background(), "reader")
	if claims == nil || claims.Username != "vaibhav" || claims.AccessToken != nil {
		t.Fatalf("runtime identity omitted current username: %+v", claims)
	}
}
