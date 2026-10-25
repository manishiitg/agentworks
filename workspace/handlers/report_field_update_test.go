package handlers

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

func setupReportFieldUpdateTest(t *testing.T) (string, string, *gin.Engine) {
	t.Helper()
	rel, abs, router := setupWorkflowDBTest(t)
	router.POST("/api/report-field", UpdateReportField)
	return rel, abs, router
}

func TestUpdateReportFieldAppliesSingleFieldAndRecordsAudit(t *testing.T) {
	rel, abs, router := setupReportFieldUpdateTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, subject TEXT, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO emails(id, subject, status) VALUES (1, 'Hello', 'pending')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	recorder := postWorkflowDBTest(t, router, "/api/report-field", models.ReportFieldUpdateRequest{
		DBPath: rel, Table: "emails", RowID: float64(1), Fields: map[string]interface{}{"status": "approved"},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	check, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var status string
	if err := check.QueryRow(`SELECT status FROM emails WHERE id=1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "approved" {
		t.Fatalf("status not updated: got %q", status)
	}
	var auditCount int
	if err := check.QueryRow(`SELECT COUNT(*) FROM report_field_update_log WHERE table_name='emails' AND column_name='status' AND old_value='pending' AND new_value='approved'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("expected exactly one matching audit row, got %d", auditCount)
	}
}

func TestUpdateReportFieldAppliesMultipleFieldsAtomically(t *testing.T) {
	rel, abs, router := setupReportFieldUpdateTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, subject TEXT, status TEXT, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO emails(id, subject, status, note) VALUES (1, 'Hello', 'pending', '')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	recorder := postWorkflowDBTest(t, router, "/api/report-field", models.ReportFieldUpdateRequest{
		DBPath: rel, Table: "emails", RowID: float64(1),
		Fields: map[string]interface{}{"status": "approved", "note": "looks good"},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	check, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var status, note string
	if err := check.QueryRow(`SELECT status, note FROM emails WHERE id=1`).Scan(&status, &note); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || note != "looks good" {
		t.Fatalf("fields not both updated: status=%q note=%q", status, note)
	}
}

func TestUpdateReportFieldRejectsGuardedColumnsAndLeavesRowUnchanged(t *testing.T) {
	rel, abs, router := setupReportFieldUpdateTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, workflow_id TEXT, status TEXT, created_at TEXT, updated_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO emails(id, workflow_id, status, created_at, updated_at) VALUES (1, 'wf-1', 'pending', 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	for _, guarded := range []string{"id", "workflow_id", "created_at", "updated_at"} {
		recorder := postWorkflowDBTest(t, router, "/api/report-field", models.ReportFieldUpdateRequest{
			DBPath: rel, Table: "emails", RowID: float64(1), Fields: map[string]interface{}{guarded: "hacked"},
		})
		if recorder.Code == http.StatusOK {
			t.Fatalf("guarded column %q was accepted: %s", guarded, recorder.Body.String())
		}
	}
	// A multi-field call mixing one legitimate column with one guarded column
	// must reject and apply NEITHER — never a partial write.
	recorder := postWorkflowDBTest(t, router, "/api/report-field", models.ReportFieldUpdateRequest{
		DBPath: rel, Table: "emails", RowID: float64(1),
		Fields: map[string]interface{}{"status": "approved", "workflow_id": "hacked"},
	})
	if recorder.Code == http.StatusOK {
		t.Fatalf("mixed valid+guarded field call was accepted: %s", recorder.Body.String())
	}

	check, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var workflowID, status string
	if err := check.QueryRow(`SELECT workflow_id, status FROM emails WHERE id=1`).Scan(&workflowID, &status); err != nil {
		t.Fatal(err)
	}
	if workflowID != "wf-1" {
		t.Fatalf("guarded column workflow_id was mutated: got %q", workflowID)
	}
	if status != "pending" {
		t.Fatalf("status changed despite rejected request (partial write): got %q", status)
	}
}

func TestUpdateReportFieldRejectsReservedTable(t *testing.T) {
	rel, _, router := setupReportFieldUpdateTest(t)
	for _, table := range []string{
		"report_human_inputs", "report_human_input_events", "schema_migration_log",
		"run_concerns", "eval_results", "pulse_module_state", "pulse_module_audit",
		"report_field_update_log",
	} {
		recorder := postWorkflowDBTest(t, router, "/api/report-field", models.ReportFieldUpdateRequest{
			DBPath: rel, Table: table, RowID: "some-id", Fields: map[string]interface{}{"status": "consumed"},
		})
		if recorder.Code == http.StatusOK {
			t.Fatalf("write to reserved table %q was accepted: %s", table, recorder.Body.String())
		}
	}
}

func TestUpdateReportFieldRejectsNonPrimitiveValue(t *testing.T) {
	rel, abs, router := setupReportFieldUpdateTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO emails(id, status) VALUES (1, 'pending')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	recorder := postWorkflowDBTest(t, router, "/api/report-field", models.ReportFieldUpdateRequest{
		DBPath: rel, Table: "emails", RowID: float64(1),
		Fields: map[string]interface{}{"status": map[string]interface{}{"nested": true}},
	})
	if recorder.Code == http.StatusOK {
		t.Fatalf("object value was accepted: %s", recorder.Body.String())
	}
}

func TestUpdateReportFieldRejectsUnknownTableColumnAndRow(t *testing.T) {
	rel, abs, router := setupReportFieldUpdateTest(t)
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO emails(id, status) VALUES (1, 'pending')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	cases := []models.ReportFieldUpdateRequest{
		{DBPath: rel, Table: "not_a_table", RowID: float64(1), Fields: map[string]interface{}{"status": "approved"}},
		{DBPath: rel, Table: "emails", RowID: float64(1), Fields: map[string]interface{}{"not_a_column": "approved"}},
		{DBPath: rel, Table: "emails", RowID: float64(999), Fields: map[string]interface{}{"status": "approved"}},
	}
	for i, req := range cases {
		recorder := postWorkflowDBTest(t, router, "/api/report-field", req)
		if recorder.Code == http.StatusOK {
			t.Fatalf("case %d unexpectedly accepted: %s", i, recorder.Body.String())
		}
	}
}

func TestReportCannotEditManagedGoalData(t *testing.T) {
	for _, table := range []string{"workflow_goal_metrics", "goal_observations"} {
		t.Run(table, func(t *testing.T) {
			rel, abs, router := setupReportFieldUpdateTest(t)
			db, err := sql.Open("sqlite", abs)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE ` + table + ` (id INTEGER PRIMARY KEY, value REAL); INSERT INTO ` + table + ` VALUES (1, 42)`); err != nil {
				t.Fatal(err)
			}
			recorder := postWorkflowDBTest(t, router, "/api/report-field", models.ReportFieldUpdateRequest{
				DBPath: rel, Table: table, RowID: float64(1), Fields: map[string]interface{}{"value": float64(99)},
			})
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected rejection, got %d: %s", recorder.Code, recorder.Body.String())
			}
			var value float64
			if err := db.QueryRow(`SELECT value FROM ` + table + ` WHERE id=1`).Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != 42 {
				t.Fatalf("managed value changed to %v", value)
			}
		})
	}
}
