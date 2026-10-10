package gmailinbound

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func boolFilter(value bool) *bool { return &value }

func TestFiltersCombineKeywordsAndAttachmentConditions(t *testing.T) {
	f, err := NormalizeFilters(&Filters{SubjectContains: []string{" Invoice ", "invoice", "project-a"}, BodyContains: []string{"approved"}, HasAttachments: boolFilter(true)})
	if err != nil || len(f.SubjectContains) != 2 {
		t.Fatalf("normalize: %+v %v", f, err)
	}
	m := Message{Subject: "project-a INVOICE", Body: "Approved for payment", Attachments: []Attachment{{Name: "invoice.pdf"}}}
	if reason := f.Mismatch(m); reason != "" {
		t.Fatal(reason)
	}
	for _, field := range []string{"subject", "body", "attachment"} {
		bad := m
		switch field {
		case "subject":
			bad.Subject = "Invoice"
		case "body":
			bad.Body = "Pending"
		case "attachment":
			bad.Attachments = nil
		}
		if f.Mismatch(bad) == "" {
			t.Fatalf("%s condition ignored", field)
		}
	}
	noAttachments := &Filters{HasAttachments: boolFilter(false)}
	if noAttachments.Mismatch(Message{}) != "" || noAttachments.Mismatch(m) == "" {
		t.Fatal("explicit false was treated as omitted")
	}
	for _, invalid := range []*Filters{{SubjectContains: []string{" "}}, {BodyContains: []string{strings.Repeat("x", 257)}}, {SubjectContains: make([]string, 11)}} {
		if _, err := NormalizeFilters(invalid); err == nil {
			t.Fatal("invalid keywords accepted")
		}
	}
	if cleared, err := NormalizeFilters(&Filters{}); err != nil || cleared != nil {
		t.Fatal("empty filter object did not clear conditions")
	}
}

func TestNewThreadFilterRecognizesReplyHeaders(t *testing.T) {
	for _, name := range []string{"In-Reply-To", "References"} {
		raw := testRaw("reply")
		header := raw.Payload.Headers[0]
		header.Name, header.Value = name, "<parent@example.com>"
		raw.Payload.Headers = append(raw.Payload.Headers, header)
		m, err := raw.Parse("owner@example.com")
		if err != nil || !m.IsReply || (&Filters{NewThreadsOnly: true}).Mismatch(m) == "" {
			t.Fatalf("%s reply accepted: %+v %v", name, m, err)
		}
	}
}

func TestFilteredMessagesStayVisibleAndNeverReplayAfterClearing(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Filters = &Filters{SubjectContains: []string{"invoice"}}
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	m, _ := testRaw("not-an-invoice").Parse("owner@example.com")
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	history, _ := s.History(ctx, r.ID)
	if len(history) != 1 || history[0].Status != "filtered" || history[0].Error == "" {
		t.Fatalf("missing reason: %+v", history)
	}
	r.Filters = nil
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	if _, ok, err := s.Claim(ctx); err != nil || ok {
		t.Fatal("clearing filters replayed old mail")
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE deliveries SET created_at=?`, time.Now().Add(-31*24*time.Hour).Unix())
	if err := s.PruneContent(ctx); err != nil {
		t.Fatal(err)
	}
	var body string
	if err := s.db.QueryRowContext(ctx, `SELECT json_extract(message,'$.body') FROM deliveries`).Scan(&body); err != nil || body != "" {
		t.Fatalf("filtered body retained: %q %v", body, err)
	}
}

func TestNewThreadAdmissionIsAtomicAndDurable(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Filters = &Filters{NewThreadsOnly: true}
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, _ := testRaw(fmt.Sprintf("m%d", i)).Parse("owner@example.com")
			if err := s.Enqueue(ctx, r, m); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	publishBatch(t, s)
	var pending, filtered int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deliveries WHERE status='pending'`).Scan(&pending)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deliveries WHERE status='filtered'`).Scan(&filtered)
	if pending != 1 || filtered != 19 {
		t.Fatalf("thread started more than once: pending=%d filtered=%d", pending, filtered)
	}
	d, ok, err := s.Claim(ctx)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if reason, err := s.FilterReason(ctx, d); err != nil || reason != "" {
		t.Fatalf("first accepted message filtered itself: %s %v", reason, err)
	}
	if err := s.Finish(ctx, d, "complete", nil); err != nil {
		t.Fatal(err)
	}
	m := d.Message
	m.ID = "later"
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	if _, ok, _ := s.Claim(ctx); ok {
		t.Fatal("completed thread started again")
	}
}

func TestChangedFiltersStopQueuedWorkButAllowFinalResponse(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	m, _ := testRaw("queued").Parse("owner@example.com")
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	r.Filters = &Filters{HasAttachments: boolFilter(true)}
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: s, Authorize: func(context.Context, Route, Message) error { return nil }, Dispatch: func(context.Context, *Delivery) error { t.Fatal("queued message bypassed changed filters"); return nil }}
	svc.deliverNext(ctx)
	h, _ := s.History(ctx, r.ID)
	if h[0].Status != "filtered" {
		t.Fatalf("queued message was not filtered: %+v", h)
	}
	r.Filters = nil
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	m.ID = "already-executed"
	_ = s.Enqueue(ctx, r, m)
	publishBatch(t, s)
	d, _, _ := s.Claim(ctx)
	d.Response = "Done"
	_ = s.Finish(ctx, d, "reply", nil)
	r.Filters = &Filters{HasAttachments: boolFilter(true)}
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	replied := false
	svc.Reply = func(context.Context, Delivery) error { replied = true; return nil }
	svc.deliverNext(ctx)
	if !replied {
		t.Fatal("changed filters suppressed a final response for executed work")
	}
}

func TestFilteredMailDoesNotUsePendingQueueCapacity(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Filters = &Filters{SubjectContains: []string{"invoice"}}
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	_, err := s.db.ExecContext(ctx, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<10000) INSERT INTO deliveries(id,route,message,created_at,received_at) SELECT 'full:'||x,'full','{}',1,1 FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := testRaw("skipped").Parse("owner@example.com")
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatalf("filter could not record skip: %v", err)
	}
	m.ID, m.Subject = "matching", "Invoice"
	if err := s.Enqueue(ctx, r, m); err == nil {
		t.Fatal("matching mail bypassed capacity")
	}
}

func TestFiltersCannotWidenSenderAuthorization(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Filters = &Filters{SubjectContains: []string{"Review"}}
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	f := &fakeClient{ids: []string{"m1"}}
	svc := Service{Store: s, Client: func(context.Context, Mailbox) (Client, error) { return f, nil }, Authorize: func(context.Context, Route, Message) error { return errors.New("sender is not owner") }}
	box, _ := s.MailboxStatus(ctx, "gmail")
	if err := svc.syncMailbox(ctx, box); err != nil {
		t.Fatal(err)
	}
	h, _ := s.History(ctx, r.ID)
	if len(h) != 0 {
		t.Fatal("matching filter bypassed authorization")
	}
}

type busyInboxClient struct{ fakeClient }

func (f *busyInboxClient) Message(_ context.Context, id string) (RawMessage, error) {
	raw := testRaw(id)
	if id != "trigger" {
		raw.Payload.Headers[1].Value = "owner@example.com"
	}
	return raw, nil
}

func TestBusyInboxDoesNotHideAnEligibleTriggerBehindTwentyNewerMessages(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Filters = &Filters{SubjectContains: []string{"Review"}}
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 30; i++ {
		ids = append(ids, fmt.Sprintf("unrelated%d", i))
	}
	ids = append(ids, "trigger")
	client := &busyInboxClient{fakeClient{ids: ids}}
	svc := Service{Store: s, Client: func(context.Context, Mailbox) (Client, error) { return client, nil }, Authorize: func(context.Context, Route, Message) error { return nil }}
	box, _ := s.MailboxStatus(ctx, "gmail")
	if err := svc.syncMailbox(ctx, box); err != nil {
		t.Fatal(err)
	}
	d, ok, err := s.Claim(ctx)
	if err != nil || !ok || d.Message.ID != "trigger" {
		t.Fatalf("trigger lost behind unrelated mail: %+v %v", d, err)
	}
}
