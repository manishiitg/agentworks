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

func (c inboxClient) List(context.Context, int64, []string) ([]string, error) {
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
	one, err := store.EnsureInbox(ctx, Inbox{OwnerID: "owner", ConnectionID: "personal", ID: "one", ProfileID: "work", ProjectOwnerID: "owner", ConnectionID: "personal", ID: "one", Address: "company+one@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := store.EnsureInbox(ctx, Inbox{OwnerID: "other", ConnectionID: "other", ID: "two", ProfileID: "work", ProjectOwnerID: "other", ConnectionID: "other", ID: "two", Address: "company+one@example.com"})
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
