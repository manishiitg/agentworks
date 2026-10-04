package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func auditTestEvent(id, workspace string) AuditEvent {
	return AuditEvent{ID: id, CallID: id, WorkspaceID: workspace, Timestamp: time.Now().UTC(), UserID: "alice", GroupIDs: []string{"g"}, ClientID: "claude", ConnectorID: "memory", PublicName: "memory__read", Decision: DecisionAllow, Outcome: OutcomeOK, DurationMs: 10}
}
func TestAuditProviderDefaultsAndOff(t *testing.T) {
	for _, k := range []string{"LOCAL_MODE", "VAULT_AUDIT_PROVIDER", "VAULT_AUDIT_RETENTION", "VAULT_AUDIT_MAX_MB", "VAULT_AUDIT_WRITE_MODE"} {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	o, err := AuditOptionsFromEnv(dir)
	if err != nil || o.Provider != "sqlite" || o.WriteMode != "durable" || o.Retention != 24*time.Hour {
		t.Fatalf("server default: %+v %v", o, err)
	}
	backend, err := OpenAuditBackend(o)
	if err != nil {
		t.Fatal(err)
	}
	backend.Close()
	t.Setenv("VAULT_AUDIT_PROVIDER", "clickhouse")
	if _, err = AuditOptionsFromEnv(dir); err == nil {
		t.Fatal("deferred audit provider accepted")
	}
	if _, err = OpenAuditBackend(AuditOptions{Provider: "clickhouse", StateDir: dir}); err == nil {
		t.Fatal("deferred audit backend accepted")
	}
	t.Setenv("VAULT_AUDIT_PROVIDER", "")
	t.Setenv("LOCAL_MODE", "true")
	o, err = AuditOptionsFromEnv(dir)
	if err != nil || o.Provider != "sqlite" || o.Retention != 24*time.Hour {
		t.Fatalf("local default: %+v %v", o, err)
	}
	t.Setenv("VAULT_AUDIT_RETENTION", "25h")
	if _, err = AuditOptionsFromEnv(dir); err == nil {
		t.Fatal("local retention exceeded 24 hours")
	}
	t.Setenv("VAULT_AUDIT_RETENTION", "")
	t.Setenv("VAULT_AUDIT_PROVIDER", "off")
	dir = t.TempDir() // Off must not create a database in a fresh state directory.
	o, err = AuditOptionsFromEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenAuditBackend(o)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	st := NewMemoryStore()
	st.SetAuditBackend(b)
	if err = st.AppendAudit(auditTestEvent("a", "w")); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ReadAudit(AuditFilter{WorkspaceID: "w"})
	if err != nil || len(rows) != 0 || b.Info().Enabled || st.AuditCount() != 0 {
		t.Fatal("off collected events")
	}
	if _, err = os.Stat(filepath.Join(dir, "audit.sqlite")); !os.IsNotExist(err) {
		t.Fatal("off created audit database")
	}
	t.Setenv("VAULT_AUDIT_PROVIDER", "typo")
	if _, err = AuditOptionsFromEnv(dir); err == nil {
		t.Fatal("invalid provider accepted")
	}
}
func TestSQLiteAuditSurvivesRestartAndFilters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.sqlite")
	a, err := openSQLiteAudit(path, 24*time.Hour, 16<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	events := []AuditEvent{auditTestEvent("one", "w"), auditTestEvent("two", "w"), auditTestEvent("other", "other")}
	events[0].Input, _ = CaptureAuditPayload(map[string]any{"project": "one"})
	events[0].Output, _ = CaptureAuditPayload(map[string]any{"content": []any{map[string]any{"type": "text", "text": "recorded result"}}})
	events[1].Decision = DecisionDeny
	events[1].Outcome = OutcomeDenied
	events[1].UserID = "bob"
	events[1].DurationMs = 20
	for _, e := range events {
		if err = a.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = openSQLiteAudit(path, 24*time.Hour, 16<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rows, err := a.Query(ctx, AuditFilter{WorkspaceID: "w", GroupID: "g", UserID: "alice", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].ID != "one" || rows[0].ClientID != "claude" {
		t.Fatalf("restart/filter: %+v %v", rows, err)
	}
	if string(rows[0].Input) != string(events[0].Input) || string(rows[0].Output) != string(events[0].Output) {
		t.Fatal("payload lost on SQLite restart")
	}
	summary, err := a.Summary(ctx, AuditFilter{WorkspaceID: "w", Limit: 1})
	if err != nil || summary.Total != 2 || summary.Allowed != 1 || summary.Denied != 1 || summary.AvgDurationMs != 15 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	rows, err = a.Query(ctx, AuditFilter{WorkspaceID: "w' OR 1=1 --"})
	if err != nil || len(rows) != 0 {
		t.Fatal("workspace filter escaped")
	}
	mode, err := os.Stat(path)
	if err != nil || mode.Mode().Perm() != 0600 {
		t.Fatal("database permissions")
	}
	var journal, syncMode string
	a.db.QueryRow("PRAGMA journal_mode").Scan(&journal)
	a.db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	if journal != "wal" || syncMode != "2" {
		t.Fatalf("durability: %s %s", journal, syncMode)
	}
}
func TestSQLiteAuditRetentionHidesAndDeletesExpiredRows(t *testing.T) {
	a, err := openSQLiteAudit(filepath.Join(t.TempDir(), "audit.sqlite"), 24*time.Hour, 16<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	now := time.Now()
	a.now = func() time.Time { return now }
	if err = a.Append(ctx, auditTestEvent("expire", "w")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	// Query-time retention applies before the maintenance timer runs.
	rows, err := a.Query(ctx, AuditFilter{WorkspaceID: "w"})
	if err != nil || len(rows) != 0 {
		t.Fatalf("expired query: %+v %v", rows, err)
	}
	summary, err := a.Summary(ctx, AuditFilter{WorkspaceID: "w"})
	if err != nil || summary.Total != 0 {
		t.Fatal("expired usage")
	}
	if err = a.cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = a.db.QueryRow("SELECT count(*) FROM audit_events").Scan(&n); err != nil || n != 0 {
		t.Fatal("expired rows not removed")
	}
}
func TestSQLiteAuditConcurrentDurableWritesAndReplay(t *testing.T) {
	a, err := openSQLiteAudit(filepath.Join(t.TempDir(), "audit.sqlite"), 24*time.Hour, 16<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 128; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := a.Append(ctx, auditTestEvent(fmt.Sprint(i), "w")); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if err = a.Append(ctx, auditTestEvent("0", "w")); err != nil {
		t.Fatal(err)
	}
	s, err := a.Summary(ctx, AuditFilter{WorkspaceID: "w"})
	if err != nil || s.Total != 128 {
		t.Fatalf("concurrency/replay: %+v %v", s, err)
	}
}
