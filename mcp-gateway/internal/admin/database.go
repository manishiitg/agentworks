package admin

import (
	"encoding/json"
	"errors"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"io"
	"net/http"
)

func (a *Admin) databaseRoutes(mux *http.ServeMux) {
	for _, operation := range []string{"query", "mutate"} {
		mux.HandleFunc("/api/admin/database/"+operation, a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(405)
				return
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
			decoder.DisallowUnknownFields()
			var result any
			var err error
			if operation == "query" {
				var req store.SQLQuery
				if err = decoder.Decode(&req); err == nil {
					if decoder.Decode(&struct{}{}) != io.EOF {
						err = errors.New("expected one JSON object")
					} else {
						result, err = a.Store.QuerySQL(r.Context(), a.WorkspaceID, req)
					}
				}
			} else {
				var req store.SQLMutation
				if err = decoder.Decode(&req); err == nil {
					if decoder.Decode(&struct{}{}) != io.EOF {
						err = errors.New("expected one JSON object")
					} else {
						result, err = a.Store.MutateSQL(r.Context(), a.WorkspaceID, adminActor(r), req)
					}
				}
			}
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
		}))
	}
}
