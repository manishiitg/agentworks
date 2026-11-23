package cursorcli

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"path/filepath"

	"testing"

	_ "modernc.org/sqlite"
)

// writeCursorDurableStore builds a minimal store.db with the real schema
// (meta.latestRootBlobId + protobuf root + JSON message blobs) holding
// the given user_query texts in order. It returns the db path and the
// hex ref of each query blob.
func writeCursorDurableStore(t *testing.T, userQueries ...string) (string, []string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatalf("create blobs: %v", err)
	}
	ids := make([][]byte, 0, len(userQueries))
	refs := make([]string, 0, len(userQueries))
	bodies := make([]string, 0, len(userQueries))
	for i, q := range userQueries {
		id := synthBlobID(byte(0x10 + i))
		ids = append(ids, id)
		refs = append(refs, hex.EncodeToString(id))
		body, _ := json.Marshal(map[string]any{
			"role": "user",
			"content": []map[string]string{
				{"type": "text", "text": "<user_query>\n" + q + "\n</user_query>"},
			},
		})
		bodies = append(bodies, string(body))
	}
	rootID := synthBlobID(0xff)
	// Root lists message refs; the root blob itself is stored under rootID.
	fullRoot := buildCursorRootBlob(t, ids)
	metaJSON, _ := json.Marshal(map[string]any{"latestRootBlobId": hex.EncodeToString(rootID)})
	if _, err := db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('0', ?)`, hex.EncodeToString(metaJSON)); err != nil {
		t.Fatalf("insert meta: %v", err)
	}
	insertBlob := func(id []byte, body []byte) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO blobs(id,data) VALUES(?, ?)`, hex.EncodeToString(id), body); err != nil {
			t.Fatalf("insert blob: %v", err)
		}
	}
	insertBlob(rootID, fullRoot)
	for i := range ids {
		insertBlob(ids[i], []byte(bodies[i]))
	}
	return dbPath, refs
}
