package gmailinbound

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSenderConsentIsPrivateDurableAndConfigurationBound(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "email.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	r := Route{ID: "target", OwnerID: "owner", ProjectID: "code", WorkspacePath: "Chats/Code/projects/app", Address: "owner+agent-target@example.com", ConnectionID: "mail", Enabled: true, Reply: true, Filters: &Filters{SenderAllowlist: []string{"owner@example.com", "person@gmail.com"}}}
	if err := store.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	assert := func(required, approved bool) {
		t.Helper()
		status, err := store.SenderConsentStatus(ctx, r, "owner@example.com")
		if err != nil || status.Required != required || status.Approved != approved {
			t.Fatalf("status=%+v error=%v", status, err)
		}
	}
	assert(true, false) // Existing allowlists are never grandfathered into authority.
	if err := store.ConfirmSenderConsent(ctx, "other", r.ID, SenderPolicyHash(r), true); err == nil {
		t.Fatal("another owner approved the route")
	}
	if err := store.ConfirmSenderConsent(ctx, "owner", r.ID, "stale", true); err == nil {
		t.Fatal("stale approval accepted")
	}
	if err := store.ConfirmSenderConsent(ctx, "owner", r.ID, SenderPolicyHash(r), true); err != nil {
		t.Fatal(err)
	}
	assert(true, true)
	_ = store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	assert(true, true) // Restart preserves a human receipt, not agent claims.
	r.Name = "Renamed"
	if err := store.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	assert(true, true)
	previous := r
	r.Reply = false
	if err := store.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	assert(true, false)
	r = previous
	if err := store.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	assert(true, false) // Restoring old settings cannot revive a deleted receipt.
	if err := store.ConfirmSenderConsent(ctx, "owner", r.ID, SenderPolicyHash(r), true); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmSenderConsent(ctx, "owner", r.ID, SenderPolicyHash(r), false); err != nil {
		t.Fatal(err)
	}
	assert(true, false)
}

func TestSenderConsentHashCoversEveryAuthorityBearingField(t *testing.T) {
	base := Route{ID: "r", OwnerID: "owner", ProjectID: "project", WorkspacePath: "Workflow/a", ConnectionID: "mail", Address: "a@example.com", Enabled: true, Reply: true, Rules: []Rule{{ID: "rule", Name: "Rule", Instruction: "Run X", Filters: &Filters{SenderAllowlist: []string{"person@example.com"}}}}}
	for _, edit := range []func(*Route){
		func(r *Route) { r.OwnerID = "other" },
		func(r *Route) { r.ConnectionID = "other-mail" },
		func(r *Route) { r.ProjectID = "other-target" },
		func(r *Route) { r.WorkspacePath = "Workflow/b" },
		func(r *Route) { r.Reply = false },
		func(r *Route) { r.Enabled = false },
		func(r *Route) {
			r.Rules = []Rule{{ID: "rule", Name: "Rule", Instruction: "Run Y", Filters: base.Rules[0].Filters}}
		},
		func(r *Route) {
			r.Filters = &Filters{SenderAllowlist: []string{"@company.example"}, AllowAutomatic: true}
		},
	} {
		changed := base
		edit(&changed)
		if SenderPolicyHash(changed) == SenderPolicyHash(base) {
			t.Fatal("authority-bearing edit retained sender consent")
		}
	}
}

func TestBroadMailboxDomainsRequireExactAddresses(t *testing.T) {
	for _, domain := range []string{"@gmail.com", "@googlemail.com", "@outlook.com", "@yahoo.com", "@icloud.com", "@proton.me", "@co.uk", "@github.io"} {
		if _, err := NormalizeFilters(&Filters{SenderAllowlist: []string{domain}}); err == nil {
			t.Fatalf("broad domain accepted: %s", domain)
		}
	}
	for _, address := range []string{"person@gmail.com", "person@outlook.com", "@realtrainingsys.com"} {
		if _, err := NormalizeFilters(&Filters{SenderAllowlist: []string{address}}); err != nil {
			t.Fatalf("specific mailbox or organization domain rejected: %s %v", address, err)
		}
	}
}
