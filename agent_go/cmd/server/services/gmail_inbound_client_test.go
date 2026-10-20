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
 *"gmail watch start"*) printf '%s\n' '{"watch":{"historyId":"100","expirationMs":1999999999000}}';;
 *"gmail history"*"--page second"*) printf '%s\n' '{"historyId":"104","messages":["m2"]}';;
 *"gmail history"*) printf '%s\n' '{"historyId":"104","messages":["m1"],"nextPageToken":"second"}';;
 *"gmail raw"*) printf '%s\n' '{"id":"m1","threadId":"t1","payload":{}}';;
 *) exit 1;;
esac
`
	if e := os.WriteFile(binary, []byte(script), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TEST_INBOUND_ARGS", logPath)
	conn := GmailConnection{ID: "test", Email: "owner@example.com", ClientName: "private-client", Enabled: true, AllowReadAccess: true, AuthBackend: "gog"}
	svc := &GmailService{config: &GmailConfig{GogPath: binary, Connections: []GmailConnection{conn}}, authCaches: map[string]*gmailAuthCacheEntry{"test": {status: &GmailAuthStatus{Authenticated: true, Scopes: []string{GmailReadonlyScope}}, cachedAt: time.Now()}}}
	old := GetGmailService()
	SetGmailService(svc)
	t.Cleanup(func() { SetGmailService(old) })
	c := GmailInboundClient{ConnectionID: "test", Topic: "projects/test/topics/email"}
	cursor, expiry, e := c.Watch(context.Background())
	if e != nil || cursor != "100" || expiry != 1999999999 {
		t.Fatalf("watch %s %d %v", cursor, expiry, e)
	}
	ids, cursor, e := c.History(context.Background(), "100")
	if e != nil || len(ids) != 2 || cursor != "104" {
		t.Fatalf("history %+v %s %v", ids, cursor, e)
	}
	raw, e := c.Message(context.Background(), "m1")
	if e != nil || raw.ThreadID != "t1" {
		t.Fatalf("raw %+v %v", raw, e)
	}
	data, _ := os.ReadFile(logPath)
	args := string(data)
	for _, want := range []string{"--account\nowner@example.com", "--client\nprivate-client", "--home\n", "--readonly", "--gmail-no-send", "--page\nsecond"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in %s", want, args)
		}
	}
	svc.config.Connections[0].AllowReadAccess = false
	if _, _, e = c.Watch(context.Background()); e == nil {
		t.Fatal("revoked read intent accepted")
	}
	svc.config.Connections[0].AllowReadAccess = true
	svc.config.Connections[0].ScopeWorkspace = "_users/owner/Chats/Code/projects/private"
	if _, _, e = c.Watch(context.Background()); e == nil {
		t.Fatal("company receiving used a private Code account")
	}
	svc.config.Connections[0].ScopeWorkspace = ""
	svc.config.Connections[0].Personal = true
	if _, _, e = c.Watch(context.Background()); e == nil {
		t.Fatal("company receiving used a personal Google account")
	}
}
