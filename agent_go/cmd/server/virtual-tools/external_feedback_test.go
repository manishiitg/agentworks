package virtualtools

import (
	"errors"
	"testing"
	"time"
)

func TestExternalFeedbackRepliesAreAtomicallySessionScoped(t *testing.T) {
	store := &HumanFeedbackStore{requests: map[string]*HumanFeedbackRequest{}, waiters: map[string]chan string{}}
	if err := store.CreatePendingRequest("input", "Choose", "", "owner", []string{"yes", "no"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.SubmitResponseForSession("other", "input", "yes", time.Now()); !errors.Is(err, ErrFeedbackNotPending) {
		t.Fatalf("foreign session reply: %v", err)
	}
	if err := store.SubmitResponseForSession("owner", "input", "maybe", time.Now()); !errors.Is(err, ErrFeedbackInvalidChoice) {
		t.Fatalf("invalid choice: %v", err)
	}
	if err := store.SubmitResponseForSession("owner", "input", "yes", time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-store.waiters["input"]:
		if got != "yes" {
			t.Fatalf("wrong reply: %q", got)
		}
	default:
		t.Fatal("existing runtime waiter was not signaled")
	}
	if err := store.SubmitResponseForSession("owner", "input", "yes", time.Now()); !errors.Is(err, ErrFeedbackNotPending) {
		t.Fatalf("duplicate reply: %v", err)
	}
	// A completed unique ID can be reused. A stale caller must not answer the
	// new request when it belongs to a different session.
	if err := store.CreatePendingRequest("input", "New question", "", "other", nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.SubmitResponseForSession("owner", "input", "stale", time.Now()); !errors.Is(err, ErrFeedbackNotPending) {
		t.Fatalf("reused-ID reply: %v", err)
	}
}

func TestExternalFeedbackPendingFiltersSessionExpiryAndResponses(t *testing.T) {
	now := time.Now()
	store := &HumanFeedbackStore{requests: map[string]*HumanFeedbackRequest{
		"valid":   {UniqueID: "valid", SessionID: "s", Options: []string{"yes"}, UserResponse: "secret", ExpiresAt: now.Add(time.Minute)},
		"foreign": {UniqueID: "foreign", SessionID: "other"},
		"expired": {UniqueID: "expired", SessionID: "s", ExpiresAt: now},
		"done":    {UniqueID: "done", SessionID: "s", IsCompleted: true},
	}}
	rows := store.PendingForSession("s", now)
	if len(rows) != 1 || rows[0].UniqueID != "valid" || rows[0].UserResponse != "" {
		t.Fatalf("unsafe pending projection: %+v", rows)
	}
	rows[0].Options[0] = "changed"
	if store.requests["valid"].Options[0] != "yes" {
		t.Fatal("pending projection aliases live options")
	}
	if err := store.SubmitResponseForSession("s", "expired", "late", now); !errors.Is(err, ErrFeedbackNotPending) {
		t.Fatalf("expired request accepted: %v", err)
	}
}

func TestFeedbackOperationIsolatesQuestionsAcrossSharedAndChildSessions(t *testing.T) {
	store := &HumanFeedbackStore{requests: map[string]*HumanFeedbackRequest{}, waiters: map[string]chan string{}}
	for _, row := range []struct{ id, session, operation string }{
		{"first", "shared", "fn-a"}, {"child", "child-session", "fn-a"}, {"foreign", "shared", "fn-b"},
	} {
		if err := store.CreatePendingRequest(row.id, row.id, "", row.session, nil, true, time.Minute, row.operation); err != nil {
			t.Fatal(err)
		}
	}
	rows := store.PendingForOperation("fn-a", time.Now())
	if len(rows) != 2 {
		t.Fatalf("call A should see root and child questions only: %+v", rows)
	}
	if err := store.SubmitResponseForOperationID("fn-a", "foreign", "wrong", time.Now()); !errors.Is(err, ErrFeedbackNotPending) {
		t.Fatalf("cross-call reply accepted: %v", err)
	}
	if err := store.SubmitResponseForOperationID("fn-a", "child", "answer", time.Now()); err != nil {
		t.Fatalf("child question reply: %v", err)
	}
	if err := store.SubmitResponseForOperationID("fn-a", "child", "again", time.Now()); !errors.Is(err, ErrFeedbackNotPending) {
		t.Fatalf("duplicate reply accepted: %v", err)
	}
}
