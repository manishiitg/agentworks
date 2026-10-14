package guidancesessions

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Pins the receipt's security/lifecycle decisions against real persisted SQLite.
func TestGuidanceReceiptLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	versions := map[string]string{"dashboard-authoring": "v1", "another-product": "v1"}
	first, err := store.Check(ctx, "connection-a", "", true, versions, map[string]bool{"dashboard-authoring": true}, false, now)
	if err != nil || first.SessionID == "" {
		t.Fatalf("prepare: %v %v", first, err)
	}
	handle := first.SessionID
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	check := func(caller, id string, version map[string]string, when time.Time) Receipt {
		t.Helper()
		got, err := store.Check(ctx, caller, id, false, version, nil, false, when)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	status := func(got Receipt, name string) string {
		for _, topic := range got.Topics {
			if topic.Name == name {
				return topic.Status
			}
		}
		return ""
	}
	resumed := check("connection-a", handle, versions, now.Add(24*time.Hour))
	if status(resumed, "dashboard-authoring") != "delivered_to_client" || status(resumed, "another-product") != "missing" {
		t.Fatalf("restart lost receipt: %+v", resumed)
	}
	changed := check("connection-a", handle, map[string]string{"dashboard-authoring": "v2", "another-product": "v1"}, now.Add(25*time.Hour))
	if status(changed, "dashboard-authoring") != "stale" || status(changed, "another-product") != "missing" {
		t.Fatalf("topic refresh: %+v", changed)
	}
	for _, id := range []string{handle, "awg_forged"} {
		foreign := check("connection-b", id, versions, now.Add(26*time.Hour))
		if foreign.SessionID != "" || status(foreign, "dashboard-authoring") != "missing" {
			t.Fatalf("foreign leaked: %+v", foreign)
		}
	}
	expired := check("connection-a", handle, versions, now.Add(25*time.Hour+IdleExpiry))
	if expired.SessionID != "" || status(expired, "dashboard-authoring") != "missing" {
		t.Fatalf("expiry failed: %+v", expired)
	}
	// An expired context prepares a new handle, and per-caller storage stays bounded.
	for i := 0; i < maxCallerSessions+2; i++ {
		if _, err := store.Check(ctx, "connection-a", "", true, versions, nil, false, now.Add(40*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM guidance_sessions`).Scan(&count); err != nil || count != maxCallerSessions {
		t.Fatalf("unbounded storage: %d %v", count, err)
	}
	stats, err := store.Stats(ctx)
	if err != nil || stats.Deliveries != 1 || stats.StaleHits == 0 || stats.MissingHits == 0 {
		t.Fatalf("counters: %+v %v", stats, err)
	}
}
