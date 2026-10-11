package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This is server-owned auth state. A shell in the Crew cannot rewrite its
// history. We retain operation metadata, never tool arguments, output, SQL
// values, messages, browser URLs, or credentials.
func openCrewExecutionAudit() (*mcpOAuthStore, error) {
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS external_crew_execution_audit (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, crew_id TEXT NOT NULL,
 connection_id TEXT NOT NULL, tool_group TEXT NOT NULL, action TEXT NOT NULL,
 operation TEXT NOT NULL, started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL, exit_code INTEGER)`)
	if err != nil {
		s.Close()
		return nil, err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS crew_execution_audit_history ON external_crew_execution_audit (crew_id,started_at DESC,id DESC)`)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func crewAuditOperation(action string, params map[string]any) string {
	if action == "execute_shell_command" {
		return "[shell command redacted]"
	}
	if action == "mutate_workflow_db" {
		if statements, ok := params["statements"].([]any); ok {
			return fmt.Sprintf("database batch (%d statements; SQL and values redacted)", len(statements))
		}
		words := strings.Fields(externalArg(params, "sql"))
		if len(words) > 0 {
			switch verb := strings.ToUpper(words[0]); verb {
			case "INSERT", "UPDATE", "DELETE", "WITH":
				return "database " + verb + " (SQL and values redacted)"
			}
		}
		return "database mutation (SQL and values redacted)"
	}
	return action + " (arguments redacted)"
}

func startCrewExecutionAudit(ctx context.Context, user, crew, connection, group, action string, params map[string]any) (*mcpOAuthStore, string, error) {
	s, err := openCrewExecutionAudit()
	if err != nil {
		return nil, "", err
	}
	id := uuid.NewString()
	// Thirty-day retention plus bounded per-Crew/global inventories. Keep
	// in-flight calls: their completion writes may arrive after disconnection.
	_, err = s.db.ExecContext(ctx, `DELETE FROM external_crew_execution_audit WHERE
 started_at<? OR (status!='started' AND id IN (
 SELECT id FROM external_crew_execution_audit WHERE crew_id=? ORDER BY started_at DESC,id DESC LIMIT -1 OFFSET 999))
 OR (status!='started' AND id IN (
 SELECT id FROM external_crew_execution_audit ORDER BY started_at DESC,id DESC LIMIT -1 OFFSET 99999))`, time.Now().Add(-30*24*time.Hour).UnixMilli(), crew)
	if err != nil {
		s.Close()
		return nil, "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO external_crew_execution_audit
 (id,user_id,crew_id,connection_id,tool_group,action,operation,started_at,status)
 VALUES (?,?,?,?,?,?,?,?, 'started')`, id, user, crew, connection, group, action, crewAuditOperation(action, params), time.Now().UnixMilli())
	if err != nil {
		s.Close()
		return nil, "", err
	}
	return s, id, nil
}

func finishCrewExecutionAudit(s *mcpOAuthStore, id, result string, executionErr error) error {
	// Persist even when the caller disconnects after a side effect.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status := "completed"
	var exit any
	if executionErr != nil {
		status = "failed"
	}
	var receipt map[string]any
	if json.Unmarshal([]byte(result), &receipt) == nil {
		if code, ok := receipt["exit_code"].(float64); ok {
			exit = int(code)
			if code != 0 {
				status = "failed"
			}
		}
		if success, ok := receipt["success"].(bool); ok && !success {
			status = "failed"
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE external_crew_execution_audit SET finished_at=?,status=?,exit_code=? WHERE id=?`, time.Now().UnixMilli(), status, exit, id)
	return err
}

func crewExecutionHistory(ctx context.Context, crew string) ([]map[string]any, error) {
	s, err := openCrewExecutionAudit()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,connection_id,tool_group,action,operation,started_at,finished_at,status,exit_code
 FROM external_crew_execution_audit WHERE crew_id=? AND started_at>=? ORDER BY started_at DESC,id DESC LIMIT 100`, crew, time.Now().Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, user, connection, group, action, operation, status string
		var started, finished int64
		var exit sql.NullInt64
		if err = rows.Scan(&id, &user, &connection, &group, &action, &operation, &started, &finished, &status, &exit); err != nil {
			return nil, err
		}
		row := map[string]any{"id": id, "actor_id": user, "connection_id": connection, "tool_group": group, "action": action, "operation": operation, "started_at": started, "finished_at": finished, "status": status}
		if exit.Valid {
			row["exit_code"] = exit.Int64
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
