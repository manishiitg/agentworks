package gmailinbound

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func chatRules() []Rule {
	return []Rule{
		{ID: "invoice", Name: "Invoices", Filters: &Filters{SubjectContains: []string{"invoice"}}, Instruction: "Extract the invoice amount."},
		{ID: "notion", Name: "Notion updates", Filters: &Filters{SubjectContains: []string{"Notion"}}, Instruction: "Summarize the Notion update."},
	}
}

func TestEmailRulesChooseOneActionAndNeverReplayOnReorder(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Rules = chatRules()
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	m, _ := testRaw("both").Parse("owner@example.com")
	m.Subject = "Notion invoice"
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	r.Rules[0], r.Rules[1] = r.Rules[1], r.Rules[0]
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	d, ok, err := s.Claim(ctx)
	if err != nil || !ok || d.RuleID != "invoice" {
		t.Fatalf("wrong admission: %+v %v", d, err)
	}
	instruction, err := d.Route.ChatInstruction()
	if err != nil || instruction != "Extract the invoice amount." {
		t.Fatalf("wrong action: %q %v", instruction, err)
	}
	if err := s.Finish(ctx, d, "complete", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Claim(ctx); ok {
		t.Fatal("same email ran more than once")
	}
	history, _ := s.History(ctx, r.ID)
	if len(history) != 1 || history[0].RuleName != "Invoices" {
		t.Fatalf("missing matched rule: %+v", history)
	}
	m.ID = "new"
	m.ThreadID = "new-thread"
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	d, ok, err = s.Claim(ctx)
	if err != nil || !ok || d.RuleID != "notion" {
		t.Fatalf("new order ignored: %+v %v", d, err)
	}
}

func TestEmailRulesRecheckSelectedRuleWithoutRedirectingQueuedMail(t *testing.T) {
	for _, mode := range []string{"removed", "paused", "changed condition"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := testStore(t)
			r := testRoute()
			r.Rules = chatRules()
			if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
				t.Fatal(err)
			}
			m, _ := testRaw("queued").Parse("owner@example.com")
			m.Subject = "invoice Notion"
			if err := s.Enqueue(ctx, r, m); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "removed":
				r.Rules = r.Rules[1:]
			case "paused":
				r.Rules[0].Enabled = boolFilter(false)
			case "changed condition":
				r.Rules[0].Filters = &Filters{SubjectContains: []string{"different"}}
			}
			if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
				t.Fatal(err)
			}
			publishBatch(t, s)
			d, ok, err := s.Claim(ctx)
			if err != nil || !ok || d.RuleID != "invoice" {
				t.Fatalf("queued mail redirected: %+v %v", d, err)
			}
			if reason, err := s.FilterReason(ctx, d); err != nil || reason == "" {
				t.Fatalf("changed rule executed: %q %v", reason, err)
			}
		})
	}
}

func TestEmailRulesSkipNoMatchAndDisabledRules(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Rules = chatRules()
	r.Rules[0].Enabled = boolFilter(false)
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	m, _ := testRaw("skip").Parse("owner@example.com")
	m.Subject = "invoice"
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	if _, ok, _ := s.Claim(ctx); ok {
		t.Fatal("disabled or unmatched rule ran")
	}
	r.Rules[0].Enabled = nil
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	if _, ok, _ := s.Claim(ctx); ok {
		t.Fatal("new rule replayed skipped email")
	}
	history, _ := s.History(ctx, r.ID)
	if len(history) != 1 || history[0].Status != "filtered" {
		t.Fatalf("missing filtered activity: %+v", history)
	}
}

func TestEmailRulesKeepCommonConditionsAndRequireAuthorization(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Rules = chatRules()
	r.Filters = &Filters{HasAttachments: boolFilter(true)}
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	m, _ := testRaw("common").Parse("owner@example.com")
	m.Subject = "invoice Notion"
	auth := func(_ context.Context, r Route, m Message) error {
		if r.SelectedRuleID == "invoice" {
			return errors.New("not authorized")
		}
		return nil
	}
	if err := s.EnqueueAuthorized(ctx, r, m, auth); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	if _, ok, _ := s.Claim(ctx); ok {
		t.Fatal("rule bypassed common attachment filter")
	}
	m.ID = "allowed"
	m.Attachments = []Attachment{{Name: "file"}}
	if err := s.EnqueueAuthorized(ctx, r, m, auth); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	d, ok, err := s.Claim(ctx)
	if err != nil || !ok || d.RuleID != "notion" {
		t.Fatalf("unauthorized rule selected: %+v %v", d, err)
	}
}

func TestEmailRuleNewThreadsRemainIndependent(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := testRoute()
	r.Rules = chatRules()
	r.Rules[0].Filters.NewThreadsOnly = true
	r.Rules[1].Filters.NewThreadsOnly = true
	if err := s.SaveRoute(ctx, r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	m, _ := testRaw("first").Parse("owner@example.com")
	m.Subject = "invoice"
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	d, _, _ := s.Claim(ctx)
	_ = s.Finish(ctx, d, "complete", nil)
	m.ID = "second"
	m.Subject = "Notion" // Same Gmail thread, different rule.
	if err := s.Enqueue(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	publishBatch(t, s)
	d, ok, err := s.Claim(ctx)
	if err != nil || !ok || d.RuleID != "notion" {
		t.Fatalf("other rule consumed thread: %+v %v", d, err)
	}
	if reason, err := s.FilterReason(ctx, d); err != nil || reason != "" {
		t.Fatalf("per-rule admission: %q %v", reason, err)
	}
}

func TestEmailRulesValidateActionKindsAndPreserveFullWorkflowMap(t *testing.T) {
	for _, rules := range [][]Rule{
		{{ID: "bad id", Name: "Bad", Instruction: "X"}},
		{{ID: "same", Name: "A", Instruction: "X"}, {ID: "same", Name: "B", Instruction: "Y"}},
		{{ID: "none", Name: "Missing instruction"}},
		{{ID: "cross", Name: "Cross", Instruction: "X", GroupNames: []string{"prod"}}},
		{{ID: "huge", Name: "Huge", Instruction: strings.Repeat("x", 16385)}},
		make([]Rule, 21),
	} {
		if _, err := NormalizeRules(rules, false); err == nil {
			t.Fatalf("invalid rule accepted: %+v", rules)
		}
	}
	s := testStore(t)
	r := testRoute()
	r.WorkflowTrigger = true
	r.Rules = []Rule{{ID: "full", Name: "Full", GroupNames: []string{"prod"}, RouteSelections: map[string]string{}}}
	if err := s.SaveRoute(context.Background(), r, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	routes, _ := s.Routes(context.Background())
	if _, err := NormalizeRules(routes[0].Rules, true); err != nil {
		t.Fatalf("explicit full workflow lost on disk: %v", err)
	}
	r.Rules[0].Instruction = "X"
	if err := s.SaveRoute(context.Background(), r, "owner@example.com"); err == nil {
		t.Fatal("workflow accepted chat action")
	}
}

func TestEmailRuleDatabaseMigrationPreservesLegacyDeduplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE deliveries(id TEXT PRIMARY KEY,route TEXT NOT NULL,message TEXT NOT NULL,status TEXT NOT NULL,session TEXT NOT NULL DEFAULT '',response TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,received_at INTEGER NOT NULL); INSERT INTO deliveries(id,route,message,status,created_at,received_at) VALUES('route:old','route','{"thread_id":"t"}','complete',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	history, err := s.History(context.Background(), "route")
	if err != nil || len(history) != 1 || history[0].RuleID != "" {
		t.Fatalf("legacy record lost: %+v %v", history, err)
	}
	var id string
	if err := s.db.QueryRow("SELECT id FROM deliveries").Scan(&id); err != nil || id != "route:old" {
		t.Fatalf("dedup key changed: %s %v", id, err)
	}
}
