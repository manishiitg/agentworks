package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// PLAT-746-464: a changing MCP call may carry request_id. Its first successful
// reply is saved, so a retry after a dropped connection returns that reply
// instead of acting twice. Tools with their own request_id rules keep them.
func (api *StreamingAPI) externalRetryReceipt(w http.ResponseWriter, r *http.Request, tool externalTool, args map[string]any) (http.ResponseWriter, map[string]any, func(), bool) {
	key := externalArg(args, "request_id")
	props, _ := tool.InputSchema["properties"].(map[string]any)
	claims := GetUserFromContext(r.Context())
	if !tool.mutates || key == "" || props["request_id"] != nil || claims == nil {
		return w, args, func() {}, false
	}
	rest := make(map[string]any, len(args))
	for k, v := range args {
		if k != "request_id" {
			rest[k] = v
		}
	}
	payload, _ := json.Marshal(map[string]any{"tool": tool.Name, "arguments": rest})
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(payload))
	identity := uuid.NewSHA1(uuid.NameSpaceURL, []byte("external-call\x00"+claims.UserID+"\x00"+tool.Name+"\x00"+key)).String()
	lock := productConversationRegistryMutex("external-call:" + identity)
	lock.Lock()
	store, err := openExternalBuilderStore()
	if err == nil {
		_, err = store.db.ExecContext(r.Context(), `CREATE TABLE IF NOT EXISTS external_call_receipts (identity TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, status INTEGER NOT NULL, body BLOB NOT NULL)`)
	}
	if err != nil {
		if store != nil {
			store.Close()
		}
		lock.Unlock()
		externalFailure(w, err)
		return w, rest, func() {}, true
	}
	var previous string
	var status int
	var body []byte
	err = store.db.QueryRowContext(r.Context(), `SELECT fingerprint,status,body FROM external_call_receipts WHERE identity=?`, identity).Scan(&previous, &status, &body)
	if err == nil {
		store.Close()
		lock.Unlock()
		if previous != fingerprint {
			externalError(w, 409, "request_id_conflict", "This request_id was already used for a different call. Use a new request_id for a new change.")
			return w, rest, func() {}, true
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AgentWorks-Replayed", "true")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return w, rest, func() {}, true
	}
	if err != sql.ErrNoRows {
		store.Close()
		lock.Unlock()
		externalFailure(w, err)
		return w, rest, func() {}, true
	}
	rec := &externalMCPRecorder{header: http.Header{}}
	return rec, rest, func() {
		defer lock.Unlock()
		defer store.Close()
		if rec.status >= 200 && rec.status < 300 {
			// The change already happened: a client that hung up must not lose its receipt, or its retry repeats the change.
			_, _ = store.db.ExecContext(context.WithoutCancel(r.Context()), `INSERT OR IGNORE INTO external_call_receipts(identity,fingerprint,status,body) VALUES (?,?,?,?)`, identity, fingerprint, rec.status, rec.body.Bytes())
		}
		for k, values := range rec.header {
			w.Header()[k] = values
		}
		if rec.status != 0 {
			w.WriteHeader(rec.status)
		}
		_, _ = w.Write(rec.body.Bytes())
	}, false
}
