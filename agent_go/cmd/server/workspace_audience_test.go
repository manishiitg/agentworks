package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestWorkspaceAudienceKeepsPrivateCrewPrivate(t *testing.T) {
	_, profile := newCodePrivacyFixture(t)
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	if crewSharedWithOthers(codePrivacyOwnerRoot) || crewSharedWith(codePrivacyOwnerRoot, "other") {
		t.Fatal("unregistered private Crew became visible")
	}
	const folder = "app-c0de0001"
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: "owner", ProjectID: "c0de0001-0000", Shared: true, Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	if crewSharedWithOthers(codePrivacyOwnerRoot) {
		t.Fatal("registered private Crew became public")
	}
	if _, err := resolveCrewProjectBinding(context.Background(), "other", profile, "c0de0001-0000", codePrivacyOwnerRoot); err == nil {
		t.Fatal("private binding admitted another person")
	}
	if err := registry.SetRunPeople("work", folder, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if crewSharedWithOthers(codePrivacyOwnerRoot) || !crewSharedWith(codePrivacyOwnerRoot, "other") {
		t.Fatal("selected audience is not respected")
	}
	binding, err := resolveCrewProjectBinding(context.Background(), "other", profile, "c0de0001-0000", codePrivacyOwnerRoot)
	if err != nil || binding.OwnedByCaller || binding.Binding.ManifestPath != "" || binding.Binding.AuthoritativeSessionID != "" {
		t.Fatalf("Run binding inherited owner state: %+v %v", binding, err)
	}
	if err := registry.SetRunPeople("work", folder, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveCrewProjectBinding(context.Background(), "other", profile, "c0de0001-0000", codePrivacyOwnerRoot); err == nil {
		t.Fatal("removed person kept access")
	}
	// The old zero-value record must not be resurrected by a later registration.
	if err := registry.SetPrivate("work", folder, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if !crewSharedWithOthers(codePrivacyOwnerRoot) {
		t.Fatal("explicit everyone audience was reset")
	}
}

func TestWorkspaceRejectsRetiredConversationProfile(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	allowed := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", AgentProfileConversationKey: "c0de0001-0000", SelectedFolder: codePrivacyOwnerRoot}
	if _, access, err := api.admitQueryTarget(ctx, &allowed, "owner", "test"); err != nil || access != WorkflowAccessOwner {
		t.Fatalf("current Crew profile refused: %v %v", access, err)
	}
	retired := allowed
	retired.AgentProfileID = "code"
	if _, access, err := api.admitQueryTarget(ctx, &retired, "owner", "test"); err == nil || access != WorkflowAccessNone {
		t.Fatalf("retired profile admitted: %v %v", access, err)
	}
}

func TestWorkspaceCrewDashboardKeepsConnectionBounds(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "crews:read"}, CrewIDs: []string{"c0de0001-0000"}}}
	if _, err := api.dashboardTarget(context.Background(), claims, codePrivacyOwnerRoot); err != nil {
		t.Fatalf("Crew dashboard refused: %v", err)
	}
	claims.AccessToken.CrewIDs = []string{"another"}
	if _, err := api.dashboardTarget(context.Background(), claims, codePrivacyOwnerRoot); err == nil {
		t.Fatal("Crew-limited token escaped its bound")
	}
}
