package server

import (
	"context"
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
