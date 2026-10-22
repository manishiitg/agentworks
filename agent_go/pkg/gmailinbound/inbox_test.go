package gmailinbound

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

type inboxClient struct{ mail RawMessage }

func (c inboxClient) List(context.Context, int64, []string, bool) ([]string, error) {
	return []string{c.mail.ID}, nil
}
func (c inboxClient) Message(context.Context, string) (RawMessage, error) { return c.mail, nil }
func TestPersonalAliasMailIsSourceBoundWithoutExecution(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	one, err := store.EnsureInbox(ctx, Inbox{OwnerID: "owner", ConnectionID: "personal", ID: "one", ProfileID: "work", ProjectID: "one", Address: "owner+agent-one@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := store.EnsureInbox(ctx, Inbox{OwnerID: "other", ConnectionID: "other", ID: "two", ProfileID: "work", ProjectID: "two", Address: "owner+agent-one@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	raw := RawMessage{ID: "m1", ThreadID: "thread", InternalDate: fmt.Sprint(time.Now().Add(time.Second).UnixMilli())}
	raw.Payload.MimeType = "text/plain"
	raw.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("invoice"))
	raw.Payload.Headers = append(raw.Payload.Headers, struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{"From", "Sender <sender@example.com>"}, struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{"To", one.Address})
	if err = store.SetMailbox(ctx, "personal", "owner", "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := Service{Store: store, Client: func(context.Context, Mailbox) (Client, error) { return inboxClient{raw}, nil }, Inboxes: store.Inboxes, Stored: func(context.Context, Inbox) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("temporary wake failure")
		}
		return nil
	}}
	mailbox, _ := store.MailboxStatus(ctx, "personal")
	if err = service.syncMailbox(ctx, mailbox); err == nil {
		t.Fatal("lost a failed Watcher wake")
	}
	mailbox, _ = store.MailboxStatus(ctx, "personal")
	if err = service.syncMailbox(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	page, err := store.Messages(ctx, one.ID, 0, "", 50)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Body != "invoice" {
		t.Fatalf("mail %+v, %v", page, err)
	}
	if page.Messages[0].Authenticated {
		t.Fatal("invented sender authentication; filtering belongs to the detector")
	}
	other, _ := store.Messages(ctx, two.ID, 0, "", 50)
	if len(other.Messages) != 0 {
		t.Fatal("mail leaked across agent inboxes")
	}
	secondReader, _ := store.Messages(ctx, one.ID, 0, "thread", 50)
	if len(secondReader.Messages) != 1 {
		t.Fatal("one reader consumed another reader's mail")
	}
	after, _ := store.Messages(ctx, one.ID, page.NextCursor, "", 50)
	if len(after.Messages) != 0 {
		t.Fatal("cursor replayed mail")
	}
	if _, err = store.Message(ctx, two.ID, "m1"); err == nil {
		t.Fatal("attachment lookup crossed inboxes")
	}
}

// Private sources share the poller without retaining content or widening Crew
// inboxes. An older mailbox cursor does not backfill newly enabled private mail.
func TestPrivateGmailSourceSkipsOldMailAndKeepsOnlyIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	private, err := store.EnsureInbox(ctx, Inbox{ID: "private", ProfileID: PersonalGmailProfile, ProjectID: "owner", OwnerID: "owner", ConnectionID: "account"})
	if err != nil {
		t.Fatal(err)
	}
	crew, err := store.EnsureInbox(ctx, Inbox{ID: "crew", ProfileID: "work", ProjectID: "crew", OwnerID: "owner", ConnectionID: "account", Address: "owner+agent-crew@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetMailbox(ctx, "account", "owner", "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	raw := RawMessage{ID: "old", ThreadID: "t", InternalDate: fmt.Sprint((private.CreatedAt - 10) * 1000)}
	raw.Payload.MimeType = "text/plain"
	raw.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("PRIVATE_BODY"))
	raw.Payload.Headers = append(raw.Payload.Headers, struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{"From", "sender@example.com"}, struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{"To", "owner@example.com"})
	service := Service{Store: store, Inboxes: store.Inboxes, Client: func(context.Context, Mailbox) (Client, error) { return inboxClient{raw}, nil }}
	box, _ := store.MailboxStatus(ctx, "account")
	if err = service.syncMailbox(ctx, box); err != nil {
		t.Fatal(err)
	}
	page, _ := store.Messages(ctx, private.ID, 0, "", 20)
	if len(page.Messages) != 0 {
		t.Fatal("backfilled personal mail")
	}
	raw.ID = "new"
	raw.InternalDate = fmt.Sprint(time.Now().Add(time.Second).UnixMilli())
	if err = service.syncMailbox(ctx, box); err != nil {
		t.Fatal(err)
	}
	page, _ = store.Messages(ctx, private.ID, 0, "", 20)
	if len(page.Messages) != 1 || page.Messages[0].Body != "" || page.Messages[0].From != "" {
		t.Fatal("private source retained content", page)
	}
	shared, _ := store.Messages(ctx, crew.ID, 0, "", 20)
	if len(shared.Messages) != 0 {
		t.Fatal("personal mail leaked into Crew Inbox")
	}
	// A simultaneous personal Watcher cannot break self-sent Crew mail.
	raw.ID = "sent-to-crew"
	raw.LabelIDs = []string{"SENT"}
	raw.Payload.Headers[1].Value = crew.Address
	if err = service.syncMailbox(ctx, box); err != nil {
		t.Fatal(err)
	}
	privatePage, _ := store.Messages(ctx, private.ID, 0, "", 20)
	shared, _ = store.Messages(ctx, crew.ID, 0, "", 20)
	if len(privatePage.Messages) != 1 || len(shared.Messages) != 1 {
		t.Fatal("combined source changed sent-mail or alias behavior", privatePage, shared)
	}
	if err = store.Reconcile(ctx, map[string]bool{crew.ID: true}); err != nil {
		t.Fatal(err)
	}
	active, _ := store.Inboxes(ctx)
	if len(active) != 1 || active[0].PersonalGmail() {
		t.Fatal("private receiver survived last Watcher removal", active)
	}
	// Mailboxes returns only currently due polls; Synced deferred the next
	// check for a minute. Inspect the stored receiver, not the due queue.
	if _, err := store.MailboxStatus(ctx, "account"); err != nil {
		t.Fatal("stopping private receiving stopped dedicated Crew mail", err)
	}
}
