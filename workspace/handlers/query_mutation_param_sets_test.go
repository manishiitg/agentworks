package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

func openFacts(t *testing.T, abs string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS facts(id INTEGER PRIMARY KEY, value TEXT UNIQUE, n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func countFacts(t *testing.T, abs string) int {
	t.Helper()
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM facts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// One statement with many parameter sets: one transaction, one receipt that sums
// the rows affected.
func TestMutateWorkflowDBParamSetsInsertManyRowsInOneTransaction(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	_ = openFacts(t, abs).Close()
	sets := make([][]any, 0, 1000)
	for i := 0; i < 1000; i++ {
		sets = append(sets, []any{"value-" + string(rune('a'+i%26)) + "-" + strconv.Itoa(i), i})
	}
	recorder := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{DBPath: rel, Statements: []models.MutationStatement{
		{SQL: "INSERT INTO facts(value, n) VALUES (?, ?)", ParamSets: sets},
	}})
	if recorder.Code != http.StatusOK {
		t.Fatalf("param_sets insert failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := countFacts(t, abs); got != 1000 {
		t.Fatalf("rows = %d, want 1000", got)
	}
	if !strings.Contains(recorder.Body.String(), `"total_rows_affected":1000`) {
		t.Fatalf("receipt must sum the rows affected: %s", recorder.Body.String())
	}
}

// One bad row rolls the whole call back, including rows before it and other statements.
func TestMutateWorkflowDBParamSetsRollBackOnAnyFailingRow(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	_ = openFacts(t, abs).Close()
	recorder := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{DBPath: rel, Statements: []models.MutationStatement{
		{SQL: "INSERT INTO facts(value, n) VALUES ('first', 0)"},
		{SQL: "INSERT INTO facts(value, n) VALUES (?, ?)", ParamSets: [][]any{{"a", 1}, {"b", 2}, {"a", 3}, {"c", 4}}},
	}})
	if recorder.Code == http.StatusOK {
		t.Fatalf("a duplicate row unexpectedly committed: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "param_sets row 3") {
		t.Errorf("the error must name the failing row: %s", recorder.Body.String())
	}
	if got := countFacts(t, abs); got != 0 {
		t.Fatalf("rows survived a rolled-back call: %d", got)
	}
}

func TestMutateWorkflowDBParamSetsRejectBadRequests(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	_ = openFacts(t, abs).Close()
	post := func(statements ...models.MutationStatement) *http.Response {
		recorder := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{DBPath: rel, Statements: statements})
		return &http.Response{StatusCode: recorder.Code, Body: nil, Status: recorder.Body.String()}
	}
	if r := post(models.MutationStatement{SQL: "INSERT INTO facts(value) VALUES (?)", Params: []any{"x"}, ParamSets: [][]any{{"y"}}}); r.StatusCode == http.StatusOK || !strings.Contains(r.Status, "not both") {
		t.Errorf("params and param_sets together must be refused: %d %s", r.StatusCode, r.Status)
	}
	tooMany := make([][]any, maximumMutationRows+1)
	for i := range tooMany {
		tooMany[i] = []any{"v" + strconv.Itoa(i)}
	}
	if r := post(models.MutationStatement{SQL: "INSERT INTO facts(value) VALUES (?)", ParamSets: tooMany}); r.StatusCode == http.StatusOK || !strings.Contains(r.Status, "split it") {
		t.Errorf("more rows than the cap must be refused: %d", r.StatusCode)
	}
	// Statements are capped by count too, and the old cap of 20 is gone.
	many := make([]models.MutationStatement, 0, 100)
	for i := 0; i < 100; i++ {
		many = append(many, models.MutationStatement{SQL: "INSERT INTO facts(value) VALUES (?)", Params: []any{"s" + strconv.Itoa(i)}})
	}
	if r := post(many...); r.StatusCode != http.StatusOK {
		t.Errorf("100 statements must be accepted now: %d %s", r.StatusCode, r.Status)
	}
	if got := countFacts(t, abs); got != 100 {
		t.Errorf("rows = %d, want 100", got)
	}
	over := make([]models.MutationStatement, maximumStatements+1)
	for i := range over {
		over[i] = models.MutationStatement{SQL: "INSERT INTO facts(value) VALUES ('z')"}
	}
	if r := post(over...); r.StatusCode == http.StatusOK {
		t.Error("more statements than the cap must be refused")
	}
}
