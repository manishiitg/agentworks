package services

import (
	"context"
	"strings"
	"testing"
)

// A Google account connected in a Code is that Code's alone: its owner uses
// it there, and nothing else -- Crews, workflows, other Codes, notifications,
// the default -- ever sees or uses it. A Code uses only its own accounts.
func TestPrivateGmailConnectionsStayInTheirCode(t *testing.T) {
	code := "_users/alice/Chats/Code/projects/app-1"
	g := &GmailService{config: &GmailConfig{
		DefaultConnectionID: "shared",
		Connections: []GmailConnection{
			{ID: "shared", Email: "team@example.com", Enabled: true},
			{ID: "mine", Email: "alice@example.com", Enabled: true, ScopeWorkspace: code, OwnerID: "alice"},
		},
	}}
	own := GmailUseScope{CodeWorkspace: code, UserID: "alice"}
	editor := GmailUseScope{CodeWorkspace: code, UserID: "bob"}
	otherCode := GmailUseScope{CodeWorkspace: "_users/alice/Chats/Code/projects/other-2", UserID: "alice"}
	outside := GmailUseScope{}

	ids := func(conns []GmailConnection) string {
		var out []string
		for _, c := range conns {
			out = append(out, c.ID)
		}
		return strings.Join(out, ",")
	}
	for name, tc := range map[string]struct {
		scope GmailUseScope
		want  string
	}{
		"owner in the Code":  {own, "mine"},
		"editor in the Code": {editor, ""},
		"another Code":       {otherCode, ""},
		"Crew or workflow":   {outside, "shared"},
	} {
		if got := ids(g.ConnectionsUsableFrom(tc.scope)); got != tc.want {
			t.Errorf("%s lists %q, want %q", name, got, tc.want)
		}
	}

	// With no id, a Code uses its own account, never the shared default.
	if conn, err := g.ConnectionForScope("", own); err != nil || conn.ID != "mine" {
		t.Fatalf("owner default = %+v %v", conn, err)
	}
	if _, err := g.ConnectionForScope("", editor); err == nil {
		t.Fatal("an editor's chat reached the owner's account")
	}
	if _, err := g.ConnectionForScope("shared", own); err == nil {
		t.Fatal("a Code used a shared account")
	}
	if _, err := g.ConnectionForScope("mine", outside); err == nil {
		t.Fatal("a Crew or workflow used a Code's private account")
	}
	if conn, err := g.ConnectionForScope("", outside); err != nil || conn.ID != "shared" {
		t.Fatalf("outside default = %+v %v", conn, err)
	}

	// Never the default, and never platform mail.
	g.config.DefaultConnectionID = "mine"
	if _, ok := g.DefaultConnection(); ok {
		t.Fatal("a private account became the default")
	}
	if err := g.SetDefaultConnection(context.Background(), "mine"); err == nil {
		t.Fatal("a private account was set as the default")
	}
	if _, _, err := g.resolveSendConfig("mine"); err == nil {
		t.Fatal("platform mail was sent from a private account")
	}
	// The Google CLI refuses it outside its Code too.
	if _, err := g.GoogleCLIAccessForConnection(context.Background(), "mine"); err == nil {
		t.Fatal("the unscoped Google CLI reached a private account")
	}
}
