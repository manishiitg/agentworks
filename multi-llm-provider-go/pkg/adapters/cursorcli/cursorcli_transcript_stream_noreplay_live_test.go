package cursorcli

import (
	"context"
	"database/sql"

	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Include WAL sidecars: an uncheckpointed commit moves bytes there first.

// cursorStoreBlobsContain reports whether any blob in the store carries the
// marker bytes. A direct SQL scan: no stream state, no side effects.
func cursorStoreBlobsContain(t *testing.T, storeDB, marker string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+storeDB+"?mode=ro")
	if err != nil {
		t.Fatalf("sql.Open %s: %v", storeDB, err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT data FROM blobs`)
	if err != nil {
		t.Fatalf("query blobs in %s: %v", storeDB, err)
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			t.Fatalf("scan blob in %s: %v", storeDB, err)
		}
		if strings.Contains(string(data), marker) {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows in %s: %v", storeDB, err)
	}
	return false
}
