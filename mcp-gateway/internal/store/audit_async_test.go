package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func waitAudit(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("audit worker did not reach expected state")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAsyncAuditReturnsWithoutDiskAndDrainsOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.sqlite")
	a, err := openSQLiteAuditMode(path, 24*time.Hour, 16<<20, true, "async")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Occupy the only DB connection: no commit can complete until release.
	conn, err := a.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first := auditTestEvent("0", "w")
	if err = a.Append(ctx, first); err != nil {
		t.Fatal("async admission waited for disk:", err)
	}
	first.GroupIDs[0] = "mutated"
	for i := 1; i < 100; i++ {
		if err = a.Append(ctx, auditTestEvent(fmt.Sprint(i), "w")); err != nil {
			t.Fatal(err)
		}
	}
	if info := a.Info(); info.PendingWrites != 100 || info.WriteMode != "async" {
		t.Fatalf("queue stats: %+v", info)
	}
	conn.Close()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = openSQLiteAudit(path, 24*time.Hour, 16<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rows, err := a.Query(context.Background(), AuditFilter{WorkspaceID: "w"})
	if err != nil || len(rows) != 100 {
		t.Fatalf("shutdown drain/restart: %d %v", len(rows), err)
	}
	for _, row := range rows {
		if row.GroupIDs[0] != "g" {
			t.Fatal("queued event retained mutable caller memory")
		}
	}
}

func TestAsyncAuditQueueIsBoundedAndDoesNotSilentlyDrop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.sqlite")
	a, err := openSQLiteAuditMode(path, 24*time.Hour, 16<<20, true, "async")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	conn, err := a.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	accepted := 0
	for ; accepted < 2000; accepted++ {
		err = a.Append(context.Background(), auditTestEvent(fmt.Sprint(accepted), "w"))
		if err != nil {
			break
		}
	}
	if !errors.Is(err, ErrAuditQueueFull) || accepted > 1280 {
		t.Fatalf("unbounded/lost queue: accepted %d, err %v", accepted, err)
	}
	conn.Close()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if a.Info().PendingWrites != 0 {
		t.Fatal("accepted writes not drained")
	}
	reopened, err := openSQLiteAudit(path, 24*time.Hour, 16<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	summary, err := reopened.Summary(context.Background(), AuditFilter{WorkspaceID: "w"})
	if err != nil || summary.Total != accepted {
		t.Fatalf("accepted events lost on overload: %+v %v", summary, err)
	}
}

func TestAsyncAuditRetainsFailedBatchAndRecovers(t *testing.T) {
	a, err := openSQLiteAuditMode(filepath.Join(t.TempDir(), "audit.sqlite"), 24*time.Hour, 16<<20, true, "async")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL, 'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Append(context.Background(), auditTestEvent("retry", "w")); err != nil {
		t.Fatal(err)
	}
	waitAudit(t, func() bool { return !a.Info().WriteHealthy })
	if a.Info().PendingWrites != 1 || a.Info().WriteFailures == 0 {
		t.Fatal("failed event disappeared")
	}
	if err = a.Append(context.Background(), auditTestEvent("rejected", "w")); err == nil {
		t.Fatal("writer failure hidden from new admissions")
	}
	if _, err = a.db.Exec("DROP TRIGGER fail_audit"); err != nil {
		t.Fatal(err)
	}
	waitAudit(t, func() bool { return a.Info().WriteHealthy && a.Info().PendingWrites == 0 })
	rows, err := a.Query(context.Background(), AuditFilter{WorkspaceID: "w"})
	if err != nil || len(rows) != 1 || rows[0].ID != "retry" {
		t.Fatalf("retry lost/duplicated event: %+v %v", rows, err)
	}
}

func TestAsyncAuditShutdownReportsUnflushedEvents(t *testing.T) {
	a, err := openSQLiteAuditMode(filepath.Join(t.TempDir(), "audit.sqlite"), 24*time.Hour, 16<<20, true, "async")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = a.Append(context.Background(), auditTestEvent("failure", "w")); err != nil {
		t.Fatal(err)
	}
	waitAudit(t, func() bool { return !a.Info().WriteHealthy })
	if err = a.Close(); err == nil {
		t.Fatal("shutdown falsely claimed a successful drain")
	}
}

func TestAuditWriteModeConfiguration(t *testing.T) {
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("VAULT_AUDIT_PROVIDER", "sqlite")
	t.Setenv("VAULT_AUDIT_RETENTION", "")
	t.Setenv("VAULT_AUDIT_MAX_MB", "")
	t.Setenv("VAULT_AUDIT_WRITE_MODE", "async")
	o, err := AuditOptionsFromEnv(t.TempDir())
	if err != nil || o.WriteMode != "async" {
		t.Fatalf("async option: %+v %v", o, err)
	}
	b, err := OpenAuditBackend(o)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Info().WriteMode != "async" {
		t.Fatal("provider ignored async option")
	}
	t.Setenv("VAULT_AUDIT_WRITE_MODE", "typo")
	if _, err = AuditOptionsFromEnv(t.TempDir()); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

// Measures caller-side admission latency with a real SQLite background writer.
// Logs are test-only; this does not execute upstream MCP actions.
func BenchmarkAuditAdmission(b *testing.B) {
	for _, mode := range []string{"async", "durable"} {
		b.Run(mode, func(b *testing.B) {
			a, err := openSQLiteAuditMode(filepath.Join(b.TempDir(), "audit.sqlite"), 24*time.Hour, 16<<20, true, mode)
			if err != nil {
				b.Fatal(err)
			}
			defer a.Close()
			samples := make([]time.Duration, b.N)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				err = a.Append(context.Background(), auditTestEvent(fmt.Sprint(i), "w"))
				samples[i] = time.Since(start)
				if err != nil {
					b.Fatal(err)
				}
				// Pace batch admission to measure normal operation, not overload.
				if mode == "async" && a.Info().PendingWrites > 800 {
					b.StopTimer()
					for a.Info().PendingWrites > 256 {
						time.Sleep(time.Millisecond)
					}
					b.StartTimer()
				}
			}
			b.StopTimer()
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			if len(samples) > 0 {
				b.ReportMetric(float64(samples[(len(samples)-1)*95/100].Nanoseconds())/1000, "p95-us")
				b.ReportMetric(float64(samples[(len(samples)-1)*99/100].Nanoseconds())/1000, "p99-us")
			}
		})
	}
}
