package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	if err != nil || o.Provider != "clickhouse" || o.WriteMode != "durable" {
		t.Fatalf("server default: %+v %v", o, err)
	}
	if _, err = OpenAuditBackend(o); err == nil {
		t.Fatal("missing ClickHouse silently accepted")
	}
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
func TestClickHouseAuditQueuePersistsFailedDelivery(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var inserts atomic.Int32
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ClickHouse-Key") != "private-test-key" || r.URL.Query().Get("password") != "" {
			t.Error("credentials not restricted to headers")
		}
		if strings.HasPrefix(r.URL.Query().Get("query"), "INSERT") {
			inserts.Add(1)
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, string(raw))
			mu.Unlock()
			if failing.Load() {
				http.Error(w, "failure with secret details", 500)
				return
			}
			return
		}
		raw, _ := io.ReadAll(r.Body)
		q := string(raw)
		if strings.HasPrefix(q, "SELECT Event") {
			if !strings.Contains(q, " FINAL") || r.URL.Query().Get("param_workspace") != "w' OR 1=1 --" || strings.Contains(q, "w' OR") {
				t.Error("query lost deduplication or parameterization")
			}
			json.NewEncoder(w).Encode(map[string]string{"Event": string(mustAuditJSON(t, auditTestEvent("a", "w")))})
		}
	}))
	defer server.Close()
	opts := AuditOptions{Provider: "clickhouse", StateDir: t.TempDir(), Retention: 30 * 24 * time.Hour, MaxBytes: 16 << 20, ClickHouseURL: server.URL, ClickHousePassword: "private-test-key"}
	a, err := openClickHouseAudit(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e := auditTestEvent("a", "w")
	if err = a.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err = a.flush(ctx); err == nil || strings.Contains(err.Error(), "secret details") {
		t.Fatal("failure or sensitive response mishandled")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = openClickHouseAudit(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	pending, err := a.spool.pending(ctx, 100)
	if err != nil || len(pending) != 1 || pending[0].ID != "a" {
		t.Fatal("delivery failure lost queued event")
	}
	failing.Store(false)
	if _, err = a.flush(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err = a.spool.pending(ctx, 100)
	if err != nil || len(pending) != 0 {
		t.Fatal("acknowledged event stayed queued")
	}
	rows, err := a.Query(ctx, AuditFilter{WorkspaceID: "w' OR 1=1 --", Limit: 1})
	if err != nil || len(rows) != 1 {
		t.Fatalf("read: %+v %v", rows, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if inserts.Load() != 2 || len(bodies) != 2 || !bytes.Equal([]byte(bodies[0]), []byte(bodies[1])) {
		t.Fatal("replay changed event")
	}
}
func mustAuditJSON(t *testing.T, e AuditEvent) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Opt-in test against a disposable ClickHouse instance, never a production DB.
func TestClickHouseAuditLive(t *testing.T) {
	endpoint := os.Getenv("VAULT_TEST_CLICKHOUSE_URL")
	if endpoint == "" {
		t.Skip("no disposable ClickHouse instance")
	}
	a, err := openClickHouseAudit(AuditOptions{Provider: "clickhouse", StateDir: t.TempDir(), Retention: 30 * 24 * time.Hour, MaxBytes: 16 << 20, ClickHouseURL: endpoint, ClickHouseUser: "default", ClickHousePassword: os.Getenv("VAULT_TEST_CLICKHOUSE_PASSWORD")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	workspace := fmt.Sprintf("live-audit-%d", time.Now().UnixNano())
	first := auditTestEvent("allowed", workspace)
	first.UserID = "alice' OR 1=1 --"
	second := auditTestEvent("denied", workspace)
	second.Decision = DecisionDeny
	second.Outcome = OutcomeDenied
	second.DurationMs = 20
	old := auditTestEvent("expired", workspace)
	old.Timestamp = time.Now().Add(-31 * 24 * time.Hour)
	for _, e := range []AuditEvent{first, second, old} {
		if err = a.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.flush(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Query(ctx, AuditFilter{WorkspaceID: workspace, GroupID: "g", UserID: first.UserID, PublicName: "READ", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatalf("live filters: %+v %v", rows, err)
	}
	summary, err := a.Summary(ctx, AuditFilter{WorkspaceID: workspace})
	if err != nil || summary.Total != 2 || summary.Allowed != 1 || summary.Denied != 1 || summary.AvgDurationMs != 15 {
		t.Fatalf("live summary: %+v %v", summary, err)
	}
	// Retry the exact event after ingestion: FINAL must remove duplicate counts.
	if err = a.Append(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err = a.flush(ctx); err != nil {
		t.Fatal(err)
	}
	summary, err = a.Summary(ctx, AuditFilter{WorkspaceID: workspace})
	if err != nil || summary.Total != 2 {
		t.Fatalf("live replay duplicated usage: %+v %v", summary, err)
	}
	if len(summary.ByTool) != 1 || summary.ByTool[0].Count != 2 || len(summary.ByDay) != 1 {
		t.Fatal("live buckets wrong")
	}
}
