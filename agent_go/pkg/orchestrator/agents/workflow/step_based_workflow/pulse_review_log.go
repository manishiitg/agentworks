package step_based_workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	_ "modernc.org/sqlite"
)

// Historical reviewer receipts remain readable; current Pulse writes goal checks and work.
type PulseReviewReceipt struct {
	ID                int64                           `json:"id"`
	Module            string                          `json:"module"`
	ReviewRunID       string                          `json:"review_run_id"`
	PulseRunID        string                          `json:"pulse_run_id,omitempty"`
	Verdict           string                          `json:"verdict,omitempty"`
	Status            string                          `json:"status,omitempty"`
	FindingCount      int                             `json:"finding_count"`
	VerificationCount int                             `json:"verification_count"`
	RecordedAt        string                          `json:"recorded_at"`
	Verifications     []PulseReviewVerificationResult `json:"verifications"`
	Metrics           *PulseAgentMetricRecord         `json:"metrics,omitempty"`
}

// PulseReviewVerificationResult is the reviewer's structured judgment about a
// prior changed_unverified attempt. The reviewer remains read-only; the single
// Fixer consumes this record and performs the lifecycle mutation.
type PulseReviewVerificationResult struct {
	FindingID   string   `json:"finding_id"`
	Fingerprint string   `json:"fingerprint"`
	AttemptID   string   `json:"attempt_id"`
	Verdict     string   `json:"verdict"`
	Expected    string   `json:"expected"`
	Observed    string   `json:"observed"`
	Evidence    []string `json:"evidence"`
	NextCheck   string   `json:"next_check,omitempty"`
}

// LoadPulseReviewReceipts returns compact reviewer receipts.
func LoadPulseReviewReceipts(ctx context.Context, workspacePath, module string, limit int) ([]PulseReviewReceipt, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulseReviewReceipt{}, err
	}
	defer db.Close()
	exists, err := pulseReviewLogExists(ctx, db)
	if err != nil || !exists {
		return nil, err
	}
	if limit == 0 {
		limit = 200
	}
	module = pulsemodules.Normalize(module)
	query := `SELECT _id, module, review_run_id, pulse_run_id,
			verdict, status, finding_count, verification_count,
			verifications_json, recorded_at
		FROM pulse_review_log WHERE 1=1`
	args := []interface{}{}
	if module != "" {
		aliases := []string{}
		for _, accepted := range pulsemodules.AcceptedForReviewReceipts() {
			if pulsemodules.Normalize(accepted) == module {
				aliases = append(aliases, accepted)
			}
		}
		if len(aliases) == 0 {
			return []PulseReviewReceipt{}, nil
		}
		query += ` AND module IN (` + strings.TrimRight(strings.Repeat("?,", len(aliases)), ",") + `)`
		for _, alias := range aliases {
			args = append(args, alias)
		}
	}
	query += ` ORDER BY recorded_at DESC, _id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PulseReviewReceipt{}
	for rows.Next() {
		var artifact PulseReviewReceipt
		var verificationsJSON string
		if err := rows.Scan(
			&artifact.ID, &artifact.Module, &artifact.ReviewRunID, &artifact.PulseRunID,
			&artifact.Verdict, &artifact.Status, &artifact.FindingCount,
			&artifact.VerificationCount, &verificationsJSON, &artifact.RecordedAt,
		); err != nil {
			return nil, err
		}
		artifact.Module = pulsemodules.Normalize(artifact.Module)
		if err := json.Unmarshal([]byte(verificationsJSON), &artifact.Verifications); err != nil {
			return nil, fmt.Errorf("decode reviewer verifications for %s/%s: %w", artifact.ReviewRunID, artifact.Module, err)
		}
		out = append(out, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	metrics, err := LoadPulseAgentMetrics(ctx, workspacePath, "", "", "reviewer", -1)
	if err != nil {
		return nil, err
	}
	attachPulseReviewMetrics(out, metrics)
	return out, nil
}

func LoadPulseReviewReceiptForRun(ctx context.Context, workspacePath, reviewRunID, module string) (*PulseReviewReceipt, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, sql.ErrNoRows
	}
	defer db.Close()
	exists, err := pulseReviewLogExists(ctx, db)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, sql.ErrNoRows
	}
	module = pulsemodules.Normalize(module)
	var artifact PulseReviewReceipt
	var verificationsJSON string
	err = db.QueryRowContext(ctx, `SELECT _id, module, review_run_id, pulse_run_id,
			verdict, status, finding_count, verification_count,
			verifications_json, recorded_at
		FROM pulse_review_log
		WHERE review_run_id=? AND module=?
		ORDER BY _id DESC LIMIT 1`, strings.TrimSpace(reviewRunID), module).Scan(
		&artifact.ID, &artifact.Module, &artifact.ReviewRunID, &artifact.PulseRunID,
		&artifact.Verdict, &artifact.Status, &artifact.FindingCount,
		&artifact.VerificationCount, &verificationsJSON, &artifact.RecordedAt,
	)
	if err != nil {
		return nil, err
	}
	artifact.Module = pulsemodules.Normalize(artifact.Module)
	if err := json.Unmarshal([]byte(verificationsJSON), &artifact.Verifications); err != nil {
		return nil, err
	}
	metrics, err := LoadPulseAgentMetrics(ctx, workspacePath, artifact.PulseRunID, artifact.Module, "reviewer", -1)
	if err != nil {
		return nil, err
	}
	withMetrics := []PulseReviewReceipt{artifact}
	attachPulseReviewMetrics(withMetrics, metrics)
	artifact = withMetrics[0]
	return &artifact, nil
}

// RecentPulseReviewRunIDsForModule is a diagnostic-only read for PLAT-196:
// when a background task agent's expected receipt is missing, this lists
// what review_run_ids actually did write a receipt for the module recently,
// so a recurrence can show directly whether the write landed under a
// different session id than the one the read side expected.
func RecentPulseReviewRunIDsForModule(ctx context.Context, workspacePath, module string, limit int) ([]string, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, nil
	}
	defer db.Close()
	exists, err := pulseReviewLogExists(ctx, db)
	if err != nil || !exists {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.QueryContext(ctx, `SELECT review_run_id FROM pulse_review_log
		WHERE module=? ORDER BY _id DESC LIMIT ?`, pulsemodules.Normalize(module), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, err
		}
		out = append(out, runID)
	}
	return out, rows.Err()
}

func attachPulseReviewMetrics(reviews []PulseReviewReceipt, metrics []PulseAgentMetricRecord) {
	byReview := make(map[string]*PulseAgentMetricRecord, len(metrics))
	for i := range metrics {
		metric := &metrics[i]
		key := strings.TrimSpace(metric.ReviewRunID) + "\x00" + pulsemodules.Normalize(metric.Module)
		if _, exists := byReview[key]; !exists {
			byReview[key] = metric
		}
	}
	for i := range reviews {
		key := strings.TrimSpace(reviews[i].ReviewRunID) + "\x00" + pulsemodules.Normalize(reviews[i].Module)
		reviews[i].Metrics = byReview[key]
	}
}

func pulseReviewLogExists(ctx context.Context, db *sql.DB) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='pulse_review_log'`).Scan(&exists)
	return exists > 0, err
}
