package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// Managed writes honor the foreign keys the schema declares (PLAT-428): a row
// pointing at a missing parent is refused and nothing of the batch is kept.
func TestMutateWorkflowDBEnforcesForeignKeys(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE contacts(id INTEGER PRIMARY KEY, email TEXT)`,
		`CREATE TABLE sends(id INTEGER PRIMARY KEY, contact_id INTEGER NOT NULL REFERENCES contacts(id))`,
		`INSERT INTO contacts(id, email) VALUES (1, 'a@example.com')`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	ok := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{DBPath: rel, Statements: []models.MutationStatement{
		{SQL: "INSERT INTO sends(contact_id) VALUES (?)", Params: []any{1}},
	}})
	if ok.Code != http.StatusOK {
		t.Fatalf("insert with an existing parent failed: %d %s", ok.Code, ok.Body.String())
	}
	orphan := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{DBPath: rel, Statements: []models.MutationStatement{
		{SQL: "INSERT INTO sends(contact_id) VALUES (?)", Params: []any{1}},
		{SQL: "INSERT INTO sends(contact_id) VALUES (?)", Params: []any{999}},
	}})
	if orphan.Code == http.StatusOK || !strings.Contains(strings.ToLower(orphan.Body.String()), "foreign key") {
		t.Fatalf("an orphan row must be refused with a foreign key error: %d %s", orphan.Code, orphan.Body.String())
	}
	check, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var n int
	if err := check.QueryRow(`SELECT COUNT(*) FROM sends`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sends = %d, want 1: the refused batch must roll back entirely", n)
	}
}
