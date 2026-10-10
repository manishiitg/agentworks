package gmailinbound

import (
	"context"
	"testing"
)

func TestSenderAllowlistAndAlternativePhrases(t *testing.T) {
	f, err := NormalizeFilters(&Filters{SenderAllowlist: []string{" @example.com ", "updates@vendor.example", "@example.com"}, SubjectContainsAny: []string{"Training", "Notion"}, BodyContainsAny: []string{"approved", "ready"}, HasAttachments: boolFilter(false)})
	if err != nil || len(f.SenderAllowlist) != 2 {
		t.Fatalf("normalize: %+v %v", f, err)
	}
	for _, sender := range []string{"MANISH@example.com", "updates@vendor.example"} {
		if reason := f.Mismatch(Message{From: sender, Subject: "NOTION update", Body: "Ready to review"}); reason != "" {
			t.Fatal(reason)
		}
	}
	for _, sender := range []string{"user@evilexample.com", "user@example.com.evil.example", "user@sub.example.com", "other@vendor.example", "@example.com", "Training <attacker@evil.example>"} {
		if f.SenderAllowed(sender) {
			t.Fatalf("unexpected sender allowed: %s", sender)
		}
	}
	for _, m := range []Message{{From: "manish@example.com", Subject: "Different", Body: "ready"}, {From: "manish@example.com", Subject: "Training", Body: "pending"}, {From: "manish@example.com", Subject: "Notion", Body: "ready", Attachments: []Attachment{{Name: "file"}}}} {
		if f.Mismatch(m) == "" {
			t.Fatal("an OR group bypassed the other required conditions")
		}
	}
	for _, f := range []*Filters{{SenderAllowlist: []string{"*"}}, {SenderAllowlist: []string{"@*.example.com"}}, {SenderAllowlist: []string{"@example.com.evil@example.com"}}, {SenderAllowlist: []string{"Name <user@example.com>"}}, {SenderAllowlist: []string{"@-example.com"}}, {AllowAutomatic: true}} {
		if _, err := NormalizeFilters(f); err == nil {
			t.Fatalf("invalid policy accepted: %+v", f)
		}
	}
}

type notificationClient struct {
	fakeClient
	header, value string
	spam          bool
}

func (c *notificationClient) Message(_ context.Context, id string) (RawMessage, error) {
	r := testRaw(id)
	r.Payload.Headers[0].Value = "updates@vendor.example"
	r.Payload.Headers[4].Value = "mx.google.com; dmarc=pass header.from=vendor.example"
	h := r.Payload.Headers[0]
	h.Name, h.Value = c.header, c.value
	r.Payload.Headers = append(r.Payload.Headers, h)
	if c.spam {
		r.LabelIDs = []string{"SPAM"}
	}
	return r, nil
}
func TestNotificationsRequireOptInAndNeverAllowReplyLoops(t *testing.T) {
	for _, tc := range []struct {
		name, header, value   string
		optIn, spam, accepted bool
	}{
		{"notification allowed", "Auto-Submitted", "auto-generated", true, false, true},
		{"notification without opt-in", "Auto-Submitted", "auto-generated", false, false, false},
		{"mailing list opted in", "List-ID", "news.vendor.example", true, false, true},
		{"automatic reply", "Auto-Submitted", "auto-replied; owner-email=x@example.com", true, false, false},
		{"bounce", "Return-Path", "<>", true, false, false},
		{"spam", "Auto-Submitted", "auto-generated", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := testStore(t)
			r := testRoute()
			r.Filters = &Filters{SenderAllowlist: []string{"updates@vendor.example"}, AllowAutomatic: tc.optIn}
			if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
				t.Fatal(err)
			}
			c := &notificationClient{fakeClient: fakeClient{ids: []string{"n1"}}, header: tc.header, value: tc.value, spam: tc.spam}
			svc := Service{Store: s, Client: func(context.Context, Mailbox) (Client, error) { return c, nil }, Authorize: func(context.Context, Route, Message) error { return nil }}
			box, _ := s.MailboxStatus(ctx, "gmail")
			if err := svc.syncMailbox(ctx, box); err != nil {
				t.Fatal(err)
			}
			_, accepted, err := s.Claim(ctx)
			if err != nil || accepted != tc.accepted {
				t.Fatalf("accepted=%v, want %v: %v", accepted, tc.accepted, err)
			}
		})
	}
}
