package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

func setupWorkflowDBTest(t *testing.T) (string, string, *gin.Engine) {
	t.Helper()
	docs := t.TempDir()
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	rel := filepath.ToSlash(filepath.Join("Workflow", "wal-test", "db", "db.sqlite"))
	abs := filepath.Join(docs, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/query", QueryWorkflowDB)
	router.POST("/api/mutate", MutateWorkflowDB)
	router.POST("/api/db/initialize", InitializeWorkflowDB)
	router.POST("/api/db/backup-snapshot", CreateWorkflowDatabaseBackupSnapshot)
	return rel, abs, router
}

func TestInitializeWorkflowDBCreatesManagedSchemaIdempotently(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	request := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"CREATE TABLE IF NOT EXISTS ui_presentations (id TEXT PRIMARY KEY, kind TEXT NOT NULL)",
		"CREATE INDEX IF NOT EXISTS idx_ui_presentations_kind ON ui_presentations(kind)",
	}}
	for attempt := 0; attempt < 2; attempt++ {
		recorder := postWorkflowDBTest(t, router, "/api/db/initialize", request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("initialize attempt %d failed: status=%d body=%s", attempt+1, recorder.Code, recorder.Body.String())
		}
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var table string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='ui_presentations'").Scan(&table); err != nil {
		t.Fatal(err)
	}
}

func TestInitializeWorkflowDBCreatesEmptyManagedDatabase(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	recorder := postWorkflowDBTest(t, router, "/api/db/initialize", models.InitializeDatabaseRequest{
		DBPath: rel,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("empty initialize failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() {
		t.Fatalf("valid SQLite file was not created: info=%v err=%v", info, err)
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("created database is invalid: integrity=%q err=%v", integrity, err)
	}
	var tables int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("empty initializer invented %d placeholder tables", tables)
	}
}

func TestWorkDatabaseAcceptsCurrentRuntimePathAndRejectsAnotherUser(t *testing.T) {
	docs := t.TempDir()
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/db/initialize", InitializeWorkflowDB)
	router.POST("/api/query", QueryWorkflowDB)

	ownPath := "_users/alice/Chats/Work/projects/demo/db/db.sqlite"
	request := models.InitializeDatabaseRequest{DBPath: ownPath, Migrations: []string{"CREATE TABLE IF NOT EXISTS notes (id TEXT PRIMARY KEY)"}}
	reqBody, _ := json.Marshal(request)
	req := httptest.NewRequest(http.MethodPost, "/api/db/initialize", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "alice")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("current user's runtime path rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(docs, "_users", "alice", "Chats", "Work", "projects", "demo", "db", "db.sqlite")); err != nil {
		t.Fatal(err)
	}

	foreign := models.QueryRequest{DBPath: "_users/bob/Chats/Work/projects/demo/db/db.sqlite", SQL: "SELECT 1"}
	foreignBody, _ := json.Marshal(foreign)
	foreignReq := httptest.NewRequest(http.MethodPost, "/api/query", bytes.NewReader(foreignBody))
	foreignReq.Header.Set("Content-Type", "application/json")
	foreignReq.Header.Set("X-User-ID", "alice")
	foreignRecorder := httptest.NewRecorder()
	router.ServeHTTP(foreignRecorder, foreignReq)
	if foreignRecorder.Code == http.StatusOK {
		t.Fatalf("foreign runtime path accepted: %s", foreignRecorder.Body.String())
	}
}

func TestInitializeWorkflowDBRejectsNonIdempotentOrStackedSQL(t *testing.T) {
	rel, _, router := setupWorkflowDBTest(t)
	for _, migration := range []string{
		"CREATE TABLE presentations (id TEXT)",
		"DROP TABLE ui_presentations",
		"CREATE TABLE IF NOT EXISTS safe (id TEXT); DELETE FROM safe",
	} {
		recorder := postWorkflowDBTest(t, router, "/api/db/initialize", models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{migration}})
		if recorder.Code == http.StatusOK {
			t.Fatalf("unsafe migration accepted: %q", migration)
		}
	}
}

func postWorkflowDBTest(t *testing.T, router *gin.Engine, route string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestQueryWorkflowDBCheckpointedWALWithoutSidecars(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE facts(id INTEGER PRIMARY KEY, value TEXT); INSERT INTO facts(value) VALUES ('checkpointed');`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(abs + "-wal")
	_ = os.Remove(abs + "-shm")

	recorder := postWorkflowDBTest(t, router, "/api/query", models.QueryRequest{DBPath: rel, SQL: "SELECT value FROM facts"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("query failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("checkpointed")) {
		t.Fatalf("checkpointed row missing: %s", recorder.Body.String())
	}
}

func TestQueryWorkflowDBSeesCommittedRowsInActiveWAL(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE facts(id INTEGER PRIMARY KEY, value TEXT); INSERT INTO facts(value) VALUES ('main');`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO facts(value) VALUES ('wal-only')`); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(abs + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("fixture requires a non-empty WAL: info=%v err=%v", info, err)
	}

	recorder := postWorkflowDBTest(t, router, "/api/query", models.QueryRequest{DBPath: rel, SQL: "SELECT value FROM facts ORDER BY id"})
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("main")) || !bytes.Contains(recorder.Body.Bytes(), []byte("wal-only")) {
		t.Fatalf("active WAL rows not visible: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkflowDBQueryRejectsWritesAndStackedSQL(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE facts(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for _, statement := range []string{
		"DELETE FROM facts",
		"SELECT * FROM facts; DELETE FROM facts",
		"PRAGMA query_only=OFF",
		"ATTACH DATABASE '/tmp/other.sqlite' AS other",
	} {
		recorder := postWorkflowDBTest(t, router, "/api/query", models.QueryRequest{DBPath: rel, SQL: statement})
		if recorder.Code == http.StatusOK {
			t.Fatalf("unsafe query accepted: %q body=%s", statement, recorder.Body.String())
		}
	}
}

func TestMutateWorkflowDBRollsBackAllStatements(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE facts(id INTEGER PRIMARY KEY, value TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	recorder := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{DBPath: rel, Statements: []models.MutationStatement{
		{SQL: "CREATE TABLE IF NOT EXISTS staged (id INTEGER)"},
		{SQL: "INSERT INTO facts(value) VALUES (?)", Params: []any{"same"}},
		{SQL: "INSERT INTO facts(value) VALUES (?)", Params: []any{"same"}},
	}})
	if recorder.Code == http.StatusOK {
		t.Fatalf("failing transaction unexpectedly committed: %s", recorder.Body.String())
	}

	check, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var staged int
	if err := check.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='staged'").Scan(&staged); err != nil || staged != 0 {
		t.Fatalf("schema survived rollback: %d %v", staged, err)
	}
	var count int
	if err := check.QueryRow(`SELECT COUNT(*) FROM facts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("transaction was not rolled back: count=%d", count)
	}
}

func TestMutateWorkflowDBDoesNotCreateMissingDatabase(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	recorder := postWorkflowDBTest(t, router, "/api/mutate", models.MutationRequest{DBPath: rel, Statements: []models.MutationStatement{{SQL: "INSERT INTO facts(value) VALUES (?)", Params: []any{"x"}}}})
	if recorder.Code == http.StatusOK {
		t.Fatalf("missing database unexpectedly created: %s", recorder.Body.String())
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("missing database path was created: err=%v", err)
	}
}
