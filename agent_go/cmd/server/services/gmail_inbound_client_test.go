package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGmailInboundGogAdapterScopesAndWireFormat(t *testing.T) {
	t.Setenv("GOG_HOME", t.TempDir())
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	binary := filepath.Join(dir, "gog")
	// No real credential lookup or Google network request is involved.
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$TEST_INBOUND_ARGS"
case "$*" in
 *'"pageToken":"second"'*) printf '%s\n' '{"messages":[{"id":"m2"}]}';;
 *"gmail.users.messages.list"*) printf '%s\n' '{"messages":[{"id":"m1"}],"nextPageToken":"second"}';;
 *"gmail raw"*) printf '%s\n' '{"id":"m1","threadId":"t1","payload":{}}';;
 *) exit 1;;
esac
`
	if e := os.WriteFile(binary, []byte(script), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TEST_INBOUND_ARGS", logPath)
	conn := GmailConnection{AgentInbox: true, Personal: true, OwnerID: "owner", ID: "test", Email: "owner@example.com", ClientName: "private-client", Enabled: true, AllowReadAccess: true, AuthBackend: "gog"}
	svc := &GmailService{config: &GmailConfig{GogPath: binary, Connections: []GmailConnection{conn}}, authCaches: map[string]*gmailAuthCacheEntry{"test": {status: &GmailAuthStatus{Authenticated: true, Scopes: []string{GmailReadonlyScope}}, cachedAt: time.Now()}}}
	old := GetGmailService()
	SetGmailService(svc)
	t.Cleanup(func() { SetGmailService(old) })
	c := GmailInboundClient{ConnectionID: "test", OwnerID: "owner"}
	if conn.UsableFrom(GmailUseScope{}) || conn.UsableFrom(GmailUseScope{Workspace: "Workflow/example", UserID: "other"}) || len(svc.ConnectionsUsableFrom(GmailUseScope{})) != 0 {
		t.Fatal("receiving credentials exposed to an agent")
	}
	if _, err := svc.GoogleCLIAccessForConnectionIn(context.Background(), conn.ID, GmailUseScope{}); err == nil {
		t.Fatal("general gog reached the receiving mailbox")
	}
	if gogHomeForConnection(conn) == gogHomeDir() {
		t.Fatal("receiving token stored in the shared terminal's gog home")
	}
	svc.config.DefaultConnectionID = conn.ID
	if _, ok := svc.DefaultConnection(); ok {
		t.Fatal("receiving account became the default sender")
	}

	ids, e := c.List(context.Background(), 100, []string{"owner+agent-one@example.com"})
	if e != nil || len(ids) != 2 {
		t.Fatalf("alias mail %+v %v", ids, e)
	}
	raw, e := c.Message(context.Background(), "m1")
	if e != nil || raw.ThreadID != "t1" {
		t.Fatalf("raw %+v %v", raw, e)
	}
	data, _ := os.ReadFile(logPath)
	args := string(data)
	for _, want := range []string{"--account\nowner@example.com", "--client\nprivate-client", "--home\n", "--readonly", "--gmail-no-send", `"pageToken":"second"`, `deliveredto:owner+agent-one@example.com`} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in %s", want, args)
		}
	}
	svc.config.Connections[0].AllowReadAccess = false
	if _, e = c.List(context.Background(), 100, []string{"owner+agent-one@example.com"}); e == nil {
		t.Fatal("revoked read intent accepted")
	}
	svc.config.Connections[0].AllowReadAccess = true
	svc.config.Connections[0].OwnerID = "other"
	if _, e = c.List(context.Background(), 100, []string{"owner+agent-one@example.com"}); e == nil {
		t.Fatal("another person's account used")
	}
	svc.config.Connections[0].OwnerID = "owner"
	svc.config.Connections[0].AgentInbox = false
	if _, e = c.List(context.Background(), 100, []string{"owner+agent-one@example.com"}); e == nil {
		t.Fatal("unselected account used")
	}
}
