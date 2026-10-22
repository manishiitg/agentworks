package server

import (
	"encoding/json"
	"net/http"
)

// One-off operator entry point. It uses the same administrator-only migration
// as the chat tool and may be removed after every installation has moved.
func (api *StreamingAPI) handleVaultMigration(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Apply bool `json:"apply"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil {
		writeUsersError(w, 400, "invalid migration request")
		return
	}
	rows, err := api.migratePlaceConnections(r.Context(), in.Apply)
	if err != nil {
		writeUsersJSON(w, 500, map[string]any{"rows": rows, "error": err.Error()})
		return
	}
	failed := 0
	for _, row := range rows {
		if row.Result == "failed" {
			failed++
		}
	}
	writeUsersJSON(w, 200, map[string]any{"rows": rows, "failed": failed})
}
