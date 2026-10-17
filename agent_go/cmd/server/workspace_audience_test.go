package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"strings"
	"testing"
)

func TestWorkspaceAudienceKeepsLegacyCodePrivate(t *testing.T) {
	_, profile := newCodePrivacyFixture(t)
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	if crewSharedWithOthers(codePrivacyOwnerRoot) || crewSharedWith(codePrivacyOwnerRoot, "other") {
		t.Fatal("unregistered legacy Code became visible")
	}
	const folder = "app-c0de0001"
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: folder, OwnerID: "owner", ProjectID: "c0de0001-0000"}); err != nil {
		t.Fatal(err)
	}
	if crewSharedWithOthers(codePrivacyOwnerRoot) {
		t.Fatal("registered legacy Code became public")
	}
	if _, err := resolveCrewProjectBinding(context.Background(), "other", profile, "c0de0001-0000", codePrivacyOwnerRoot); err == nil {
		t.Fatal("private binding admitted another person")
	}
	if err := registry.SetRunPeople("code", folder, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if crewSharedWithOthers(codePrivacyOwnerRoot) || !crewSharedWith(codePrivacyOwnerRoot, "other") {
		t.Fatal("selected audience is not respected")
	}
	binding, err := resolveCrewProjectBinding(context.Background(), "other", profile, "c0de0001-0000", codePrivacyOwnerRoot)
	if err != nil || binding.OwnedByCaller || binding.Binding.ManifestPath != "" || binding.Binding.AuthoritativeSessionID != "" {
		t.Fatalf("Run binding inherited owner state: %+v %v", binding, err)
	}
	if err := registry.SetRunPeople("code", folder, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveCrewProjectBinding(context.Background(), "other", profile, "c0de0001-0000", codePrivacyOwnerRoot); err == nil {
		t.Fatal("removed person kept access")
	}
	// The old zero-value record must not be resurrected by a later registration.
	if err := registry.SetPrivate("code", folder, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: folder, OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if !crewSharedWithOthers(codePrivacyOwnerRoot) {
		t.Fatal("explicit everyone audience was reset")
	}
}

// The first upgrade preserves legacy Crew audiences; later startup discovery
// and lazy registration must never company-share newly created projects.
func TestWorkspaceAudienceBaselineAndLazyCreation(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	docs := t.TempDir()
	writeOwnerFixture(t, docs, "_users/owner/Chats/Work/projects/legacy/product.json", `{"product":"work","id":"legacy"}`)
	if report := migrateProductOwners(docs); len(report.Failures) != 0 {
		t.Fatal(report.Failures)
	}
	if rec, ok := registry.Lookup("work", "legacy"); !ok || rec.Private {
		t.Fatal("legacy Crew audience changed")
	}
	writeOwnerFixture(t, docs, "_users/owner/Chats/Work/projects/new/product.json", `{"product":"work","id":"new"}`)
	if report := migrateProductOwners(docs); len(report.Failures) != 0 {
		t.Fatal(report.Failures)
	}
	if rec, ok := registry.Lookup("work", "new"); !ok || !rec.Private || !rec.AudienceConfigured {
		t.Fatal("startup shared a new Crew")
	}
	ensureProjectOwnerID(context.Background(), "_users/owner/Chats/Work/projects/lazy", "owner")
	if rec, ok := registry.Lookup("work", "lazy"); !ok || !rec.Private || !rec.AudienceConfigured {
		t.Fatal("lazy registration shared a new Crew")
	}
}

func TestWorkspaceRejectsMismatchedConversationProfile(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	for _, req := range []QueryRequest{
		{AgentProfileID: "work", SelectedFolder: codePrivacyOwnerRoot},
		{AgentProfileID: "code", SelectedFolder: "Crew/team-a"},
	} {
		if _, access, err := api.admitQueryTarget(ctx, &req, "other", "test"); err == nil || access != WorkflowAccessNone || !strings.Contains(err.Error(), "matching conversation profile") {
			t.Fatalf("mismatched profile admitted: %v %v", access, err)
		}
	}
}

func TestWorkspaceCodeDashboardKeepsCodeOnlyTokenAndCrewBounds(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "code:run"}}}
	if _, err := api.dashboardTarget(context.Background(), claims, codePrivacyOwnerRoot); err != nil {
		t.Fatalf("Code-only dashboard refused: %v", err)
	}
	claims.AccessToken.CrewIDs = []string{"another"}
	if _, err := api.dashboardTarget(context.Background(), claims, codePrivacyOwnerRoot); err == nil {
		t.Fatal("Crew-limited token escaped its bound")
	}
}

func TestWorkspaceCodeCoOwnerUsesVerifiedOwnerFolder(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "app-c0de0001", OwnerID: "owner", ProjectID: "c0de0001-0000"}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AddCoOwner("code", "app-c0de0001", "other"); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "code", AgentProfileConversationKey: "c0de0001-0000", SelectedFolder: codePrivacyOwnerRoot}
	req.AgentProfileContext.ProjectTitle = "App"
	resolved, err := api.resolveAgentProfileForQuery(ctx, &req, "other", "co-owner-test")
	if err != nil || resolved == nil || req.SelectedFolder != codePrivacyOwnerRoot {
		t.Fatalf("co-owner lost its verified project: %v %+v", err, req)
	}
	sibling := req
	sibling.SelectedFolder = "_users/owner/Chats/Code/projects/sibling"
	if _, err := api.resolveAgentProfileForQuery(ctx, &sibling, "other", "co-owner-sibling"); err == nil {
		t.Fatal("co-owner of one project reached its owner's sibling folder")
	}
	if _, err := registry.RemoveCoOwner("code", "app-c0de0001", "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.resolveAgentProfileForQuery(ctx, &req, "other", "co-owner-revoked"); err == nil {
		t.Fatal("removed co-owner kept next-turn access")
	}
}
