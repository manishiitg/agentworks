package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// A Code's Google account lives in its own gog store beside the shared one,
// never inside it: the shared store is what terminals, workflows and Crews
// are handed. Every gog call for the connection names that store, and
// deleting the connection removes it.
func TestPrivateGmailConnectionsHaveTheirOwnStore(t *testing.T) {
	binDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"---\" >> " + shellQuote(argvFile) + "\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + shellQuote(argvFile) + "; done\n"
	if err := os.WriteFile(filepath.Join(binDir, "gog"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GOG_HOME", filepath.Join(t.TempDir(), "agentworks", "gog"))
	clientsDir := t.TempDir()
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", clientsDir)
	if err := os.MkdirAll(filepath.Join(clientsDir, "primary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clientsDir, "primary", "client_secret.json"), []byte(`{"installed":{"client_id":"id","client_secret":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	shared := GmailConnection{ID: "shared", Email: "team@example.com", ClientName: "primary", AuthBackend: "gog"}
	mine := GmailConnection{ID: "mine", Email: "alice@example.com", ClientName: "primary", AuthBackend: "gog", ScopeWorkspace: "_users/alice/Chats/Code/projects/app-1", OwnerID: "alice"}
	other := mine
	other.ID, other.ScopeWorkspace = "theirs", "_users/alice/Chats/Code/projects/other-2"
	home := gogHomeForConnection(mine)
	if gogHomeForConnection(shared) != gogHomeDir() {
		t.Fatal("a shared connection left the shared store")
	}
	if rel, err := filepath.Rel(gogHomeDir(), home); err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("private store %s is inside the shared store %s", home, gogHomeDir())
	}
	if home == gogHomeForConnection(other) {
		t.Fatal("two Codes share a private store")
	}

	if err := ImportRefreshTokenIntoGog(context.Background(), home, mine.Email, mine.ClientName, "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if got := gmailConnectionConfig(mine).home(); got != home {
		t.Fatalf("status/send run against %s, want %s", got, home)
	}
	for _, call := range strings.Split(strings.Join(readArgvLines(t, argvFile), "\n"), "---") {
		if strings.TrimSpace(call) != "" && !strings.Contains(call, "--home\n"+home+"\n") {
			t.Fatalf("gog call not pointed at the private store:\n%s", call)
		}
	}

	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	var saved string
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			var put struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(body, &put)
			saved = put.Content
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		out, _ := json.Marshal(map[string]any{"success": true, "data": map[string]string{"content": saved}})
		_, _ = w.Write(out)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	g := &GmailService{config: &GmailConfig{Connections: []GmailConnection{shared, mine}}}
	if err := g.DeleteConnection(context.Background(), "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("private store survived its connection: %v", err)
	}
	if strings.Contains(saved, "alice@example.com") {
		t.Fatal("the deleted connection is still in the saved registry")
	}
}

// The service refuses credential paths on a private account too, and deleting
// a connection never removes a gws dir another connection still uses.
func TestPrivateGmailConnectionsTakeNoCredentialPaths(t *testing.T) {
	g := &GmailService{config: &GmailConfig{Connections: []GmailConnection{
		{ID: "mine", ClientName: "primary", ScopeWorkspace: "_users/alice/Chats/Code/projects/app-1", OwnerID: "alice"},
	}}}
	if _, err := g.CreateConnection(context.Background(), GmailConnectionInput{DisplayName: "x", ClientName: "primary", ScopeWorkspace: "_users/alice/Chats/Code/projects/app-1", OwnerID: "alice", ConfigHome: "/srv/shared"}); err != ErrPrivateGmailCredentialPaths {
		t.Fatalf("private create with config_home = %v", err)
	}
	if _, err := g.UpdateConnection(context.Background(), "mine", GmailConnectionInput{CredentialsFile: "/srv/keys/org.json"}); err != ErrPrivateGmailCredentialPaths {
		t.Fatalf("private update with credentials_file = %v", err)
	}
	dir := "/srv/gmail/connections/gmail_001"
	if !gmailConfigHomeReferenced([]GmailConnection{{ID: "shared", ConfigHome: dir + "/"}}, dir) {
		t.Fatal("a dir another connection uses would be removed")
	}
	if gmailConfigHomeReferenced([]GmailConnection{{ID: "shared", ConfigHome: dir + "-2"}}, dir) {
		t.Fatal("an unrelated dir counted as referenced")
	}
}

// Connecting the same Google account again in a Code (to change its access) replaces the older
// connection; another Code, another person, another account or a shared connection is kept.
func TestPrivateDuplicatesOfSameAccountInSameCode(t *testing.T) {
	code := "_users/alice/Chats/Code/projects/app-1"
	g := &GmailService{config: &GmailConfig{Connections: []GmailConnection{
		{ID: "old", Email: "Alice@example.com", ScopeWorkspace: code, OwnerID: "alice"},
		{ID: "new", Email: "alice@example.com", ScopeWorkspace: code, OwnerID: "alice"},
		{ID: "other-code", Email: "alice@example.com", ScopeWorkspace: "_users/alice/Chats/Code/projects/app-2", OwnerID: "alice"},
		{ID: "other-account", Email: "work@example.com", ScopeWorkspace: code, OwnerID: "alice"},
		{ID: "shared", Email: "alice@example.com"},
	}}}
	var got []string
	for _, c := range g.PrivateDuplicatesOf("new") {
		got = append(got, c.ID)
	}
	if strings.Join(got, ",") != "old" {
		t.Fatalf("duplicates = %v, want [old]", got)
	}
	if dup := g.PrivateDuplicatesOf("shared"); len(dup) != 0 {
		t.Fatalf("a shared connection replaced private ones: %v", dup)
	}
}
