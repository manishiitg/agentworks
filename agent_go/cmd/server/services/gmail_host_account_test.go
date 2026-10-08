package services

import (
	"testing"
	"time"
)

// A host that was authenticated with `gws auth login` directly (no legacy
// gmail-config.json to migrate from) becomes the default connection, named by
// its address, with an empty ConfigHome so it keeps using the host's own gws
// directory and is never treated as a registry-provisioned dir to delete.
func TestHostAccountConnectionAdoptsAnAuthenticatedHost(t *testing.T) {
	now := time.Date(2026, 9, 3, 15, 30, 0, 0, time.UTC)
	conn, ok := hostAccountConnection(GmailAuthStatus{
		GwsInstalled: true, Authenticated: true, HasGmailScope: true,
		Email:  "owner@example.com",
		Scopes: []string{"https://www.googleapis.com/auth/gmail.modify", "openid"},
	}, now)
	if !ok {
		t.Fatal("an authenticated host with the Gmail scope must be adopted")
	}
	if conn.ID != "gmail_001" || conn.Email != "owner@example.com" || conn.DisplayName != conn.Email {
		t.Fatalf("unexpected connection %+v", conn)
	}
	if conn.ConfigHome != "" || !conn.Enabled || conn.Status != GmailConnectionConnected {
		t.Fatalf("host account must keep the host gws dir, be enabled and connected: %+v", conn)
	}
	if len(conn.Scopes) != 2 || !conn.CreatedAt.Equal(now) {
		t.Fatalf("scopes/timestamps not carried: %+v", conn)
	}
	if ownsGmailConnectionDir(conn.ConfigHome) {
		t.Fatal("the host's directory must never count as registry-owned")
	}
}

func TestHostAccountConnectionRefusesAnUnusableHost(t *testing.T) {
	now := time.Now()
	cases := map[string]GmailAuthStatus{
		"gws missing":       {Authenticated: true, HasGmailScope: true, Email: "a@b.c"},
		"not authenticated": {GwsInstalled: true, HasGmailScope: true, Email: "a@b.c"},
		"no gmail scope":    {GwsInstalled: true, Authenticated: true, Email: "a@b.c"},
		"no address":        {GwsInstalled: true, Authenticated: true, HasGmailScope: true},
	}
	for name, st := range cases {
		if _, ok := hostAccountConnection(st, now); ok {
			t.Errorf("%s: must not be adopted", name)
		}
	}
}
