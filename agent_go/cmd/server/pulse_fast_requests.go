package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// pulseFastRequestSchema is deliberately workflow-local. The finalizer agent
// decides whether its completed run merits earlier review; the scheduler only
// delivers that request on the existing dedicated Pulse lane.
const pulseFastRequestSchema = `CREATE TABLE IF NOT EXISTS pulse_fast_request (
	workspace_path TEXT PRIMARY KEY,
	requested_run_id TEXT NOT NULL,
	reason TEXT NOT NULL,
	evidence_json TEXT NOT NULL DEFAULT '[]',
	requested_at TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	delivered_pulse_run_id TEXT NOT NULL DEFAULT '',
	delivered_at TEXT NOT NULL DEFAULT ''
)`

type PulseFastRequest struct {
	WorkspacePath       string   `json:"workspace_path"`
	RequestedRunID      string   `json:"requested_run_id"`
	Reason              string   `json:"reason"`
	Evidence            []string `json:"evidence"`
	RequestedAt         string   `json:"requested_at"`
	Status              string   `json:"status"`
	DeliveredPulseRunID string   `json:"delivered_pulse_run_id,omitempty"`
	DeliveredAt         string   `json:"delivered_at,omitempty"`
}

func ensurePulseFastRequestSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, pulseFastRequestSchema); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_pulse_fast_request_status ON pulse_fast_request(status, requested_at)`)
	return err
}

func requestFastPulse(ctx context.Context, workspacePath, runID, reason string, evidence []string) (*PulseFastRequest, error) {
	runID, reason = strings.TrimSpace(runID), strings.TrimSpace(reason)
	if runID == "" || reason == "" {
		return nil, fmt.Errorf("record_pulse_fast_request requires run_id and a concrete reason")
	}
	normalized, db, err := openPulseStoreDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensurePulseFastRequestSchema(ctx, db); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	evidenceJSON, _ := json.Marshal(evidence)
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_fast_request
		(workspace_path,requested_run_id,reason,evidence_json,requested_at,status,delivered_pulse_run_id,delivered_at)
		VALUES (?,?,?,?,?,'pending','','')
		ON CONFLICT(workspace_path) DO UPDATE SET
		requested_run_id=excluded.requested_run_id, reason=excluded.reason,
		evidence_json=excluded.evidence_json, requested_at=excluded.requested_at,
		status='pending', delivered_pulse_run_id='', delivered_at=''`,
		normalized, runID, reason, string(evidenceJSON), now); err != nil {
		return nil, err
	}
	return &PulseFastRequest{WorkspacePath: normalized, RequestedRunID: runID, Reason: reason, Evidence: evidence, RequestedAt: now, Status: "pending"}, nil
}

func pendingFastPulseRequest(ctx context.Context, workspacePath string) (*PulseFastRequest, error) {
	normalized, db, err := openPulseStoreDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	if err := ensurePulseFastRequestSchema(ctx, db); err != nil {
		return nil, err
	}
	var request PulseFastRequest
	var evidenceJSON string
	err = db.QueryRowContext(ctx, `SELECT workspace_path,requested_run_id,reason,evidence_json,requested_at,status,delivered_pulse_run_id,delivered_at
		FROM pulse_fast_request WHERE workspace_path=? AND status='pending'`, normalized).Scan(
		&request.WorkspacePath, &request.RequestedRunID, &request.Reason, &evidenceJSON, &request.RequestedAt,
		&request.Status, &request.DeliveredPulseRunID, &request.DeliveredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(evidenceJSON), &request.Evidence)
	return &request, nil
}

func markFastPulseRequestDelivered(ctx context.Context, workspacePath, pulseRunID string) error {
	normalized, db, err := openPulseStoreDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseFastRequestSchema(ctx, db); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE pulse_fast_request SET status='delivered', delivered_pulse_run_id=?, delivered_at=?
		WHERE workspace_path=? AND status='pending'`, strings.TrimSpace(pulseRunID), time.Now().UTC().Format(time.RFC3339Nano), normalized)
	return err
}
