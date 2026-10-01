package services

import (
	"context"
	"encoding/base64"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"io"
	"net/mail"
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
	svc.config.Connections[0].OwnerID = "owner"
	if _, _, e = c.Watch(context.Background()); e == nil {
		t.Fatal("shared scope accessed a private Code's Gmail")
	}
	c.Scope = GmailUseScope{UserID: "owner", CodeWorkspace: svc.config.Connections[0].ScopeWorkspace}
	if _, _, e = c.Watch(context.Background()); e != nil {
		t.Fatalf("private owner scope rejected: %v", e)
	}
}

func TestGmailInboundReplyHeadersAndBody(t *testing.T) {
	d := gmailinbound.Delivery{Route: gmailinbound.Route{Address: "owner+agent-123@example.com"}, Message: gmailinbound.Message{From: "owner@example.com", Subject: "Café\r\nBcc: attacker@example.com", RFCMessageID: "<source@example.com>"}, Response: "Final response\nwith unicode: ✓"}
	m, e := mail.ReadMessage(strings.NewReader(inboundReplyMIME(d, "receiver@example.com")))
	if e != nil {
		t.Fatal(e)
	}
	for header, want := range map[string]string{"To": "owner@example.com", "Reply-To": d.Route.Address, "Auto-Submitted": "auto-replied", "In-Reply-To": d.Message.RFCMessageID} {
		if m.Header.Get(header) != want {
			t.Fatalf("header %s=%q", header, m.Header.Get(header))
		}
	}
	if m.Header.Get("Bcc") != "" {
		t.Fatal("subject injected a header")
	}
	b, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, m.Body))
	if e != nil || string(b) != d.Response {
		t.Fatalf("body %q %v", b, e)
	}
}
