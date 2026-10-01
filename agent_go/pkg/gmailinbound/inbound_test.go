package gmailinbound

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "email.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type boundedClient struct {
	fakeClient
	ctxEntered chan<- bool
	active     *atomic.Int32
	peak       *atomic.Int32
}

func (f *boundedClient) Watch(ctx context.Context) (string, int64, error) {
	n := f.active.Add(1)
	for {
		old := f.peak.Load()
		if n <= old || f.peak.CompareAndSwap(old, n) {
			break
		}
	}
	defer f.active.Add(-1)
	f.ctxEntered <- true
	<-ctx.Done()
	return "", 0, ctx.Err()
}
func TestOneHundredMailboxesUseBoundedWorkerPools(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 100; i++ {
		r := testRoute()
		r.ID = fmt.Sprintf("route%d", i)
		r.ConnectionID = fmt.Sprintf("gmail%d", i)
		r.WorkspacePath = fmt.Sprintf("Workflow/%d", i)
		r.Address = fmt.Sprintf("owner+agent-%d@example.com", i)
		if e := s.SaveRoute(ctx, r, fmt.Sprintf("owner%d@example.com", i)); e != nil {
			t.Fatal(e)
		}
		m, _ := testRaw(fmt.Sprintf("msg%d", i)).Parse("owner@example.com")
		m.ThreadID = fmt.Sprintf("thread%d", i)
		if e := s.Enqueue(ctx, r, m); e != nil {
			t.Fatal(e)
		}
		box, _ := s.MailboxStatus(ctx, r.ConnectionID)
		if e := s.Synced(ctx, box, ""); e != nil {
			t.Fatal(e)
		}
		_ = s.Wake(ctx, box.Email)
	}
	syncEntered := make(chan bool, 100)
	deliveryEntered := make(chan bool, 100)
	var syncActive, syncPeak, deliveryActive, deliveryPeak atomic.Int32
	svc := Service{Store: s, Client: func(context.Context, Mailbox) (Client, error) {
		return &boundedClient{ctxEntered: syncEntered, active: &syncActive, peak: &syncPeak}, nil
	}, Authorize: func(context.Context, Route, Message) error { return nil }, Dispatch: func(ctx context.Context, d *Delivery) error {
		n := deliveryActive.Add(1)
		for {
			old := deliveryPeak.Load()
			if n <= old || deliveryPeak.CompareAndSwap(old, n) {
				break
			}
		}
		defer deliveryActive.Add(-1)
		deliveryEntered <- true
		<-ctx.Done()
		return ctx.Err()
	}}
	svc.Start(ctx)
	defer func() { cancel(); svc.Wait() }()
	deadline := time.After(4 * time.Second)
	for i := 0; i < 4; i++ {
		select {
		case <-syncEntered:
		case <-deadline:
			t.Fatal("sync workers did not start")
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-deliveryEntered:
		case <-deadline:
			t.Fatal("delivery workers did not start")
		}
	}
	if syncPeak.Load() != 4 || deliveryPeak.Load() != 2 {
		t.Fatalf("unbounded pools: sync=%d delivery=%d", syncPeak.Load(), deliveryPeak.Load())
	}
	cancel()
}
func testRoute() Route {
	return Route{ID: "route", OwnerID: "owner", ConnectionID: "gmail", Address: "owner+agent-route@example.com", WorkspacePath: "Workflow/test", Enabled: true, Reply: true}
}
func testRaw(id string) RawMessage {
	var r RawMessage
	_ = json.Unmarshal([]byte(`{"id":"`+id+`","threadId":"conversation","payload":{"mimeType":"text/plain","headers":[{"name":"From","value":"Owner <owner@example.com>"},{"name":"To","value":"owner+agent-route@example.com"},{"name":"Subject","value":"Review"},{"name":"Message-ID","value":"<msg@example.com>"},{"name":"Authentication-Results","value":"mx.google.com; dmarc=pass (p=NONE) header.from=example.com"}],"body":{"data":"aGVsbG8"}}}`), &r)
	return r
}
func TestParseSenderAndAutomaticMail(t *testing.T) {
	r := testRaw("m1")
	m, e := r.Parse("owner@example.com")
	if e != nil || !m.Authenticated || m.Body != "hello" || m.From != "owner@example.com" {
		t.Fatalf("parse: %+v %v", m, e)
	}
	r.Payload.Headers[4].Value = "mx.google.com; dmarc=fail header.from=example.com"
	forged := r.Payload.Headers[4]
	forged.Value = "mx.google.com; dmarc=pass header.from=example.com"
	r.Payload.Headers = append(r.Payload.Headers, forged)
	m, _ = r.Parse("owner@example.com")
	if m.Authenticated {
		t.Fatal("forged later result overrode Gmail's failure")
	}
	r.LabelIDs = []string{"SENT"}
	m, _ = r.Parse("owner@example.com")
	if !m.Authenticated {
		t.Fatal("own sent mail must be usable")
	}
	r.LabelIDs = []string{"SPAM"}
	m, _ = r.Parse("owner@example.com")
	if !m.Automatic {
		t.Fatal("spam accepted")
	}
	r = testRaw("auto")
	h := r.Payload.Headers[0]
	h.Name = "Auto-Submitted"
	h.Value = "auto-replied"
	r.Payload.Headers = append(r.Payload.Headers, h)
	m, _ = r.Parse("owner@example.com")
	if !m.Automatic {
		t.Fatal("automatic reply accepted")
	}
}
func TestStoreDeduplicatesAndSerializesConversation(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	if e := s.SaveRoute(ctx, r, "owner@example.com"); e != nil {
		t.Fatal(e)
	}
	m, _ := testRaw("m1").Parse("owner@example.com")
	for i := 0; i < 2; i++ {
		if e := s.Enqueue(ctx, r, m); e != nil {
			t.Fatal(e)
		}
	}
	m.ID = "m2"
	_ = s.Enqueue(ctx, r, m)
	publishBatch(t, s)
	first, ok, e := s.Claim(ctx)
	if e != nil || !ok || first.Message.ID != "m1" {
		t.Fatalf("claim: %+v %v %v", first, ok, e)
	}
	if _, ok, e := s.Claim(ctx); e != nil || ok {
		t.Fatalf("same conversation overlapped: %v %v", ok, e)
	}
	first.Response = "done"
	first.SessionID = "session"
	_ = s.Finish(ctx, first, "reply", nil)
	reply, ok, e := s.Claim(ctx)
	if e != nil || !ok || reply.Status != "reply" || reply.Response != "done" {
		t.Fatalf("reply: %+v %v", reply, e)
	}
	_ = s.Finish(ctx, reply, "complete", nil)
	next, ok, e := s.Claim(ctx)
	if e != nil || !ok || next.Message.ID != "m2" {
		t.Fatalf("next: %+v %v", next, e)
	}
	history, _ := s.History(ctx, r.ID)
	if len(history) != 2 {
		t.Fatalf("duplicate delivery: %d", len(history))
	}
}
func TestCrashRecoveryDoesNotRepeatSideEffects(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "email.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	r := testRoute()
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	m, _ := testRaw("m1").Parse("owner@example.com")
	_ = s.Enqueue(ctx, r, m)
	publishBatch(t, s)
	d, _, _ := s.Claim(ctx)
	d.SessionID = "saved-session"
	_ = s.Finish(ctx, d, "running", nil)
	_ = s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, ok, _ := s.Claim(ctx); ok {
		t.Fatal("crashed run was re-executed")
	}
	h, _ := s.History(ctx, r.ID)
	if len(h) != 1 || h[0].Status != "uncertain" || h[0].SessionID != "saved-session" {
		t.Fatalf("lost recovery info: %+v", h)
	}
}
func TestWatchRenewalAndWakeRacePreserveCursor(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	_ = s.Watch(ctx, "gmail", "100", time.Now().Add(time.Hour).Unix())
	m, _ := s.MailboxStatus(ctx, "gmail")
	_ = s.Watch(ctx, "gmail", "999", time.Now().Add(time.Hour).Unix())
	renewed, _ := s.MailboxStatus(ctx, "gmail")
	if renewed.Cursor != "100" {
		t.Fatal("renewal skipped history")
	}
	_ = s.Wake(ctx, "owner@example.com")
	_ = s.Synced(ctx, m, "101")
	due, _ := s.Mailboxes(ctx)
	if len(due) != 1 || due[0].Cursor != "101" {
		t.Fatal("concurrent wakeup lost")
	}
}

type fakeClient struct {
	ids         []string
	historyErr  error
	resyncErr   error
	resynced    bool
	failMessage bool
}

func (f *fakeClient) Watch(context.Context) (string, int64, error) {
	return "100", time.Now().Add(7 * 24 * time.Hour).Unix(), nil
}
func (f *fakeClient) History(context.Context, string) ([]string, string, error) {
	return f.ids, "102", f.historyErr
}
func (f *fakeClient) Resync(context.Context, int64) ([]string, string, error) {
	f.resynced = true
	return f.ids, "103", f.resyncErr
}
func (f *fakeClient) Message(_ context.Context, id string) (RawMessage, error) {
	if f.failMessage {
		return RawMessage{}, errors.New("temporary read failure")
	}
	if id == "gone" {
		return RawMessage{}, ErrMessageGone
	}
	return testRaw(id), nil
}

type datedClient struct{ fakeClient }

func (f *datedClient) Message(_ context.Context, id string) (RawMessage, error) {
	r := testRaw(id)
	r.InternalDate = "1000"
	if id == "recent" {
		r.InternalDate = "3000"
	}
	return r, nil
}
func TestMailReceivedWhileDisabledIsNotExecutedOnReactivation(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.EnabledAt = 2000
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	_ = s.Watch(ctx, "gmail", "100", time.Now().Add(time.Hour).Unix())
	m, _ := s.MailboxStatus(ctx, "gmail")
	client := &datedClient{fakeClient: fakeClient{ids: []string{"before", "recent"}}}
	svc := Service{Store: s, Client: func(context.Context, Mailbox) (Client, error) { return client, nil }, Authorize: func(context.Context, Route, Message) error { return nil }}
	if e := svc.syncMailbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	h, _ := s.History(ctx, r.ID)
	if len(h) != 1 || h[0].ID != "route:recent" {
		t.Fatalf("disabled-period email replayed: %+v", h)
	}
}
func TestSyncRetriesWithoutDroppingOrRepeatingMessages(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	_ = s.Watch(ctx, "gmail", "100", time.Now().Add(time.Hour).Unix())
	m, _ := s.MailboxStatus(ctx, "gmail")
	f := &fakeClient{ids: []string{"m1", "m1", "gone"}}
	svc := Service{Store: s, Client: func(context.Context, Mailbox) (Client, error) { return f, nil }, Authorize: func(context.Context, Route, Message) error { return nil }}
	f.failMessage = true
	if e := svc.syncMailbox(ctx, m); e == nil {
		t.Fatal("read failure lost")
	}
	current, _ := s.MailboxStatus(ctx, "gmail")
	if current.Cursor != "100" {
		t.Fatal("advanced after failed read")
	}
	f.failMessage = false
	f.historyErr = ErrHistoryExpired
	if e := svc.syncMailbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	if !f.resynced {
		t.Fatal("expired cursor not recovered")
	}
	if e := svc.syncMailbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	h, _ := s.History(ctx, r.ID)
	if len(h) != 1 {
		t.Fatalf("duplicate or deleted mail enqueued: %+v", h)
	}
	svc.Authorize = func(context.Context, Route, Message) error { return errors.New("access revoked") }
	svc.Dispatch = func(context.Context, *Delivery) error { t.Fatal("revoked access dispatched"); return nil }
	svc.deliverNext(ctx)
	h, _ = s.History(ctx, r.ID)
	if h[0].Status != "rejected" {
		t.Fatal("revoked access not rejected")
	}
}
func TestReceiveAuthenticatesAndPersistsWakeup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.SaveRoute(ctx, testRoute(), "owner@example.com")
	svc := Service{Store: s, Verify: func(_ context.Context, token string) error {
		if token != "valid" {
			return errors.New("bad token")
		}
		return nil
	}}
	data := base64.StdEncoding.EncodeToString([]byte(`{"emailAddress":"owner@example.com","historyId":"123"}`))
	body := `{"message":{"data":"` + data + `"}}`
	for _, test := range []struct {
		token string
		want  int
	}{{"invalid", 401}, {"valid", 204}} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+test.token)
		rec := httptest.NewRecorder()
		svc.Receive(rec, req)
		if rec.Code != test.want {
			t.Fatalf("status %d want %d", rec.Code, test.want)
		}
	}
	m, _ := s.MailboxStatus(ctx, "gmail")
	if m.Generation != 2 {
		t.Fatalf("unauthenticated wake accepted or authorized wake lost: %+v", m)
	}
}
func TestPushVerifierIdentityAndCertificateCache(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	fetches := 0
	v := PushVerifier{Audience: "https://example.com/api/hooks/gmail/events", Email: "push@project.iam.gserviceaccount.com", Fetch: func(context.Context) (map[string]*rsa.PublicKey, error) {
		fetches++
		return map[string]*rsa.PublicKey{"google": &key.PublicKey}, nil
	}}
	sign := func(email, aud, kid string, expiry time.Time) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://accounts.google.com", "aud": aud, "email": email, "email_verified": true, "exp": expiry.Unix()})
		token.Header["kid"] = kid
		s, e := token.SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	good := sign(v.Email, v.Audience, "google", time.Now().Add(time.Hour))
	if e = v.Verify(context.Background(), good); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{sign("other@project", v.Audience, "google", time.Now().Add(time.Hour)), sign(v.Email, "wrong", "google", time.Now().Add(time.Hour)), sign(v.Email, v.Audience, "google", time.Now().Add(-time.Hour)), sign(v.Email, v.Audience, "unknown", time.Now().Add(time.Hour))} {
		if v.Verify(context.Background(), bad) == nil {
			t.Fatal("invalid OIDC accepted")
		}
	}
	if fetches != 1 {
		t.Fatalf("certificate fetch flood: %d", fetches)
	}
}

func publishBatch(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	m, e := s.MailboxStatus(ctx, "gmail")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Synced(ctx, m, "100"); e != nil {
		t.Fatal(e)
	}
}
func TestSyncBatchReceiptOrderAndRetention(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	newer, _ := testRaw("reply").Parse("owner@example.com")
	newer.ReceivedAt = 2000
	_ = s.Enqueue(ctx, r, newer)
	if _, ok, _ := s.Claim(ctx); ok {
		t.Fatal("partial sync batch dispatched before original message was read")
	}
	older := newer
	older.ID = "original"
	older.ReceivedAt = 1000
	older.Body = ""
	older.Attachments = []Attachment{{Name: "small.txt", Data: "c2VjcmV0", Size: 6}}
	_ = s.Enqueue(ctx, r, older)
	publishBatch(t, s)
	d, ok, e := s.Claim(ctx)
	if e != nil || !ok || d.Message.ID != "original" {
		t.Fatalf("newest-first recovery reordered conversation: %+v %v", d, e)
	}
	_ = s.Finish(ctx, d, "complete", nil)
	_, _ = s.db.ExecContext(ctx, `UPDATE deliveries SET created_at=? WHERE id=?`, time.Now().Add(-31*24*time.Hour).Unix(), d.ID)
	if e = s.PruneContent(ctx); e != nil {
		t.Fatal(e)
	}
	var message string
	_ = s.db.QueryRowContext(ctx, `SELECT message FROM deliveries WHERE id=?`, d.ID).Scan(&message)
	if strings.Contains(message, "c2VjcmV0") {
		t.Fatal("completed attachment data retained beyond 30 days")
	}
	_ = s.Enqueue(ctx, r, older)
	h, _ := s.History(ctx, r.ID)
	if len(h) != 2 {
		t.Fatal("retention removed deduplication key")
	}
	next, ok, e := s.Claim(ctx)
	if e != nil || !ok || next.Message.ID != "reply" || next.Message.Body != "hello" {
		t.Fatalf("pruning changed pending content: %+v %v", next, e)
	}
}

func TestInitialRecoveryWindowSurvivesFirstScanFailure(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	_ = s.SaveRoute(ctx, r, "owner@example.com")
	f := &fakeClient{ids: []string{"gap-message"}, resyncErr: errors.New("temporary initial scan failure")}
	svc := Service{Store: s, Client: func(context.Context, Mailbox) (Client, error) { return f, nil }, Authorize: func(context.Context, Route, Message) error { return nil }}
	m, _ := s.MailboxStatus(ctx, "gmail")
	if e := svc.syncMailbox(ctx, m); e == nil {
		t.Fatal("initial failure was lost")
	}
	m, _ = s.MailboxStatus(ctx, "gmail")
	if m.Cursor == "" || m.Processed != 0 {
		t.Fatal("initial baseline was lost after watch registration")
	}
	f.resyncErr = nil
	f.resynced = false
	if e := svc.syncMailbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	if !f.resynced {
		t.Fatal("retry skipped the pre-watch recovery window")
	}
	h, _ := s.History(ctx, r.ID)
	if len(h) != 1 {
		t.Fatalf("registration-gap email lost: %+v", h)
	}
}
