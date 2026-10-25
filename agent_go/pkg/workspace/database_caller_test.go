package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

// PLAT-856: the same parameter must stamp and filter by the person, even
// when the shared Crew database is opened with its owner's authority.
func TestDatabaseCallerFiltersProjectsWithoutChangingSharedAccess(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE projects (id TEXT PRIMARY KEY, created_by_user_id TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "crew-owner" {
			t.Error("data binding changed execution identity")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/mutate" {
			var body MutateWorkflowDBParams
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			for _, statement := range body.Statements {
				sets := statement.ParamSets
				if len(sets) == 0 {
					sets = [][]interface{}{statement.Params}
				}
				for _, params := range sets {
					if _, err := db.Exec(statement.SQL, params...); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": MutateWorkflowDBResult{}})
			return
		}
		var body QueryWorkflowDBParams
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		rows, err := db.Query(body.SQL, body.Params...)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer rows.Close()
		data := QueryWorkflowDBResult{Columns: []string{"id"}, Rows: []map[string]interface{}{}}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Error(err)
				return
			}
			data.Rows = append(data.Rows, map[string]interface{}{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer server.Close()
	client := NewClient(server.URL, WithUserID("crew-owner"))
	marker := map[string]interface{}{"$current_user_id": true}
	for _, user := range []string{"alice", "bob'quoted"} {
		ctx := WithDatabaseCaller(context.Background(), user)
		params := []interface{}{user + "-one", marker}
		_, err := client.MutateAuthorizedWorkflowDB(ctx, MutateWorkflowDBParams{DBPath: "Crew/demo/db/db.sqlite", Statements: []WorkflowDBMutationStatement{
			{SQL: "INSERT INTO projects VALUES (?,?)", Params: params},
			{SQL: "INSERT INTO projects VALUES (?,?)", ParamSets: [][]interface{}{{user + "-two", marker}}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := params[1].(map[string]interface{}); !ok {
			t.Fatal("binding mutated caller parameters")
		}
		filtered, err := client.QueryAuthorizedWorkflowDB(ctx, QueryWorkflowDBParams{DBPath: "Crew/demo/db/db.sqlite", SQL: "SELECT id FROM projects WHERE created_by_user_id = ? ORDER BY id", Params: []interface{}{marker}})
		if err != nil || len(filtered.Rows) != 2 || filtered.Rows[0]["id"] != user+"-one" {
			t.Fatalf("caller=%q result=%+v err=%v", user, filtered, err)
		}
	}
	shared, err := client.QueryAuthorizedWorkflowDB(context.Background(), QueryWorkflowDBParams{DBPath: "Crew/demo/db/db.sqlite", SQL: "SELECT id FROM projects"})
	if err != nil || len(shared.Rows) != 4 {
		t.Fatalf("shared query was restricted: %+v, %v", shared, err)
	}
	// Co-owner website/MCP reads keep their identity even with owner-root resolution.
	client.DatabaseCallerID = "alice"
	rows, err := client.QueryAuthorizedWorkflowDB(context.Background(), QueryWorkflowDBParams{DBPath: "Crew/demo/db/db.sqlite", SQL: "SELECT id FROM projects WHERE created_by_user_id = ?", Params: []interface{}{marker}})
	if err != nil || len(rows.Rows) != 2 {
		t.Fatalf("co-owner result=%+v err=%v", rows, err)
	}
	if _, err := NewClient(server.URL, WithUserID("crew-owner")).QueryAuthorizedWorkflowDB(context.Background(), QueryWorkflowDBParams{Params: []interface{}{marker}}); err == nil {
		t.Fatal("workspace owner was used in place of a missing caller")
	}
}
