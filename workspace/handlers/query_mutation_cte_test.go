package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

func TestValidateMutationSQLAllowsOnlyMutatingWithStatements(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"WITH incoming(value) AS (VALUES (?)) INSERT INTO facts(value) SELECT value FROM incoming",
		"WITH first(value) AS (VALUES (?)), second(value) AS (SELECT value FROM first) UPDATE facts SET value = (SELECT value FROM second)",
		"WITH RECURSIVE values_to_delete(id) AS (VALUES (1) UNION ALL SELECT id + 1 FROM values_to_delete WHERE id < 3) DELETE FROM facts WHERE id IN (SELECT id FROM values_to_delete)",
		"/* leading comment */ WITH incoming(value) AS NOT MATERIALIZED (VALUES (?)) INSERT INTO facts(value) SELECT value FROM incoming;",
	}
	for _, statement := range accepted {
		if err := validateMutationSQL(statement); err != nil {
			t.Errorf("valid mutation rejected: %q: %v", statement, err)
		}
	}

	rejected := []string{
		"WITH incoming(value) AS (VALUES ('INSERT INTO facts')) SELECT value FROM incoming",
		"WITH existing AS (SELECT 1) CREATE TABLE forbidden(id INTEGER)",
		"WITH existing AS (SELECT 1) PRAGMA user_version",
		"WITH incoming(value) AS (VALUES (?)) INSERT INTO facts(value) SELECT value FROM incoming; DELETE FROM facts",
		"WITH INSERT INTO facts(value) VALUES ('invalid')",
	}
	for _, statement := range rejected {
		if err := validateMutationSQL(statement); err == nil {
			t.Errorf("non-mutating or unsafe WITH statement accepted: %q", statement)
		}
	}
}

func TestMutateWorkflowDBAcceptsCTEPrefixedInsert(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE facts(id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	recorder := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{
		DBPath: rel,
		Statements: []models.MutationStatement{
			{SQL: "CREATE TABLE IF NOT EXISTS facts(id INTEGER PRIMARY KEY, value TEXT NOT NULL)"},
			{SQL: "WITH incoming(value) AS (VALUES (?)) INSERT INTO facts(value) SELECT value FROM incoming", Params: []any{"written-through-cte"}},
			{SQL: "CREATE UNIQUE INDEX IF NOT EXISTS facts_value ON facts(value)"},
			{SQL: "CREATE TABLE IF NOT EXISTS copied AS SELECT value FROM facts"},
		},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("CTE-prefixed mutation failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if !strings.Contains(recorder.Body.String(), `"total_rows_affected":1`) {
		t.Fatalf("schema counted as row writes: %s", recorder.Body.String())
	}
	check, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var copied int
	if err := check.QueryRow(`SELECT COUNT(*) FROM copied`).Scan(&copied); err != nil || copied != 1 {
		t.Fatalf("CREATE AS SELECT: rows=%d err=%v", copied, err)
	}
	var count int
	if err := check.QueryRow(`SELECT COUNT(*) FROM facts WHERE value = 'written-through-cte'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("CTE-prefixed mutation inserted %d rows, want 1", count)
	}
}
