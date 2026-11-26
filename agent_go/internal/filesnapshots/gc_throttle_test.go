package filesnapshots

import (
	"os"
	"testing"
	"time"
)

// #4080: reclaiming expired replies' files rewrites the whole private store
// under the gate, so it runs at most once per gcEvery, and only when an
// expired reply left files behind.
func TestStoreReclaimsExpiredFilesAtMostOncePerInterval(t *testing.T) {
	ctx := t.Context()
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	if err := s.init(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if s.collectGarbage(ctx, now) {
		t.Fatal("reclaimed with nothing expired")
	}
	if err := os.WriteFile(s.gcPendingPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.collectGarbage(ctx, now) {
		t.Fatal("first reclaim did not run")
	}
	if _, err := os.Stat(s.gcPendingPath()); !os.IsNotExist(err) {
		t.Fatalf("pending mark kept after reclaim: %v", err)
	}
	if err := os.WriteFile(s.gcPendingPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s.collectGarbage(ctx, now.Add(gcEvery/2)) {
		t.Fatal("reclaimed again inside the interval")
	}
	if !s.collectGarbage(ctx, now.Add(gcEvery+time.Minute)) {
		t.Fatal("pending reclaim did not run after the interval")
	}
}
