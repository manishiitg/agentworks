package step_based_workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"

	_ "modernc.org/sqlite"
)

// A durable receipt of which Pulse reviewers ran and what they concluded.
//
// Pulse Gate chooses which modules to review each cycle. It had no way to tell a
// module that keeps finding real problems from one that never finds anything,
// because nothing recorded the outcome — so the choice drifted to habit in one
// workflow (the same four modules every run) and to everything-at-once in another.
//
// The evidence that this was costing real findings: Server C ran seven reviewers
// in one cycle. All seven wrote substantive artifacts to disk — the knowledgebase
// reviewer caught a half-finished migration where two live consumers were reading
// a deleted path and silently getting nothing. Every one of those seven left
// `last_result` empty in pulse_module_state, because recording the outcome depends
// on the Pulse Fixer calling record_pulse_result, and it did not. A cycle
// that found a live breakage left no trace for the next cycle to learn from.
//
// The review receipt is deliberately not a second findings store. Findings live
// only in run_concerns + pulse_finding_details, and reviewer verification
// judgments live as structured JSON on this receipt. Raw agent output is never
// persisted here.
//
// Kept separate from pulse_module_audit deliberately. That table records the
// FIXER's outcome for a module (done/changed/blocked). This records that a
// REVIEWER ran and what it concluded, which is a different fact: a review can
// find something real and still be followed by no fix at all.

const pulseReviewLogSchema = `CREATE TABLE IF NOT EXISTS pulse_review_log (
	_id INTEGER PRIMARY KEY AUTOINCREMENT,
	module TEXT NOT NULL,
	review_run_id TEXT NOT NULL DEFAULT '',
	pulse_run_id TEXT NOT NULL DEFAULT '',
	verdict TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	finding_count INTEGER NOT NULL DEFAULT 0,
	verification_count INTEGER NOT NULL DEFAULT 0,
	verifications_json TEXT NOT NULL DEFAULT '[]',
	recorded_at TEXT NOT NULL
)`

const pulseReviewLogIndex = `CREATE INDEX IF NOT EXISTS idx_pulse_review_log_module ON pulse_review_log(module, recorded_at)`

// maxVerdictChars keeps one recorded verdict to a scannable length.
const maxVerdictChars = 400

// ModuleReviewHistory summarizes what one reviewer has been finding.
type ModuleReviewHistory struct {
	Module        string   `json:"module"`
	RunCount      int      `json:"run_count"`
	LastRanAt     string   `json:"last_ran_at,omitempty"`
	RecentVerdict []string `json:"recent_verdicts,omitempty"`
}

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

func ensurePulseReviewLogSchema(ctx context.Context, db pulseFindingLifecycleDB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(pulse_review_log)`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(columns) == 0 {
		if _, err := db.ExecContext(ctx, pulseReviewLogSchema); err != nil {
			return err
		}
	} else {
		// SQLite cannot remove columns portably. Rebuild the former
		// artifact/markdown table so raw reviewer prose is genuinely gone rather
		// than merely ignored by current reads.
		legacyNarrative := false
		for _, name := range []string{"artifact_path", "artifact_kind", "artifact_markdown", "content_sha256", "markdown"} {
			legacyNarrative = legacyNarrative || columns[name]
		}
		if legacyNarrative {
			const legacyTable = "pulse_review_log_legacy_narrative"
			if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_pulse_review_log_module`); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS `+legacyTable); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, `ALTER TABLE pulse_review_log RENAME TO `+legacyTable); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, pulseReviewLogSchema); err != nil {
				return err
			}
			value := func(name, fallback string) string {
				if columns[name] {
					return name
				}
				return fallback
			}
			findingCount := "0"
			if columns["artifact_markdown"] {
				findingCount = `CASE WHEN TRIM(COALESCE(artifact_markdown, '')) <> '' THEN 1 ELSE 0 END`
			}
			copySQL := `INSERT INTO pulse_review_log
				(module, review_run_id, pulse_run_id, verdict, status, finding_count, verification_count, verifications_json, recorded_at)
				SELECT ` + strings.Join([]string{
				value("module", "''"), value("review_run_id", "''"), value("pulse_run_id", "''"),
				value("verdict", "''"), value("status", "'completed'"), findingCount, "0", "'[]'", value("recorded_at", "''"),
			}, ", ") + ` FROM ` + legacyTable
			if _, err := db.ExecContext(ctx, copySQL); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, `DROP TABLE `+legacyTable); err != nil {
				return err
			}
			columns = map[string]bool{}
			for _, name := range []string{"status", "finding_count", "verification_count", "verifications_json"} {
				columns[name] = true
			}
		}
		for name, definition := range map[string]string{
			"status":             "TEXT NOT NULL DEFAULT ''",
			"finding_count":      "INTEGER NOT NULL DEFAULT 0",
			"verification_count": "INTEGER NOT NULL DEFAULT 0",
			"verifications_json": "TEXT NOT NULL DEFAULT '[]'",
		} {
			if columns[name] {
				continue
			}
			if _, err := db.ExecContext(ctx, `ALTER TABLE pulse_review_log ADD COLUMN `+name+` `+definition); err != nil {
				return err
			}
		}
	}
	if _, err := db.ExecContext(ctx, pulseReviewLogIndex); err != nil {
		return err
	}
	// Probed first: this runs on every open, including pure reads, and a no-op
	// UPDATE still takes the write lock (see pulseRowsExist).
	if err := pulseExecIfRows(ctx, db,
		`SELECT 1 FROM pulse_review_log WHERE module IN (?, ?)`,
		[]interface{}{pulsemodules.LegacyStrategyAuditorID, pulsemodules.LegacyGoalAdvisorID},
		`UPDATE pulse_review_log SET module=? WHERE module IN (?, ?)`,
		pulsemodules.StrategicReviewID, pulsemodules.LegacyStrategyAuditorID, pulsemodules.LegacyGoalAdvisorID); err != nil {
		return err
	}
	if err := pulseExecIfRows(ctx, db,
		`SELECT 1 FROM pulse_review_log WHERE module IN (?, ?)`,
		[]interface{}{pulsemodules.LegacyWorkflowReviewID, pulsemodules.LegacyLLMOpsReviewID},
		`UPDATE pulse_review_log SET module=? WHERE module IN (?, ?)`,
		pulsemodules.TechnicalReviewID, pulsemodules.LegacyWorkflowReviewID, pulsemodules.LegacyLLMOpsReviewID); err != nil {
		return err
	}
	return nil
}

func truncateVerdict(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxVerdictChars {
		return s
	}
	return s[:maxVerdictChars] + "…"
}

func validatePulseReviewVerificationAllowlistForModules(
	ctx context.Context,
	workspacePath string,
	modules []string,
	verifications []PulseReviewVerificationResult,
) error {
	if len(verifications) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	canonicalModules := make([]string, 0, len(modules))
	for _, module := range modules {
		module = pulsemodules.Normalize(module)
		if module == "" {
			continue
		}
		canonicalModules = append(canonicalModules, module)
		candidates, err := LoadPulseReviewVerificationCandidates(ctx, workspacePath, module)
		if err != nil {
			return fmt.Errorf("load verification allowlist for %s: %w", module, err)
		}
		for _, candidate := range candidates {
			allowed[candidate.FindingID+"\x00"+candidate.Fingerprint+"\x00"+candidate.AttemptID] = true
		}
	}
	for _, verification := range verifications {
		key := verification.FindingID + "\x00" + verification.Fingerprint + "\x00" + verification.AttemptID
		if !allowed[key] {
			return fmt.Errorf(
				"verification marker for finding %q attempt %q is outside the backend allowlist for selected modules %q",
				verification.FindingID, verification.AttemptID, strings.Join(canonicalModules, ","),
			)
		}
	}
	return nil
}

func normalizePulseReviewVerification(result PulseReviewVerificationResult) (PulseReviewVerificationResult, error) {
	result.FindingID = strings.TrimSpace(result.FindingID)
	result.Fingerprint = strings.TrimSpace(result.Fingerprint)
	result.AttemptID = strings.TrimSpace(result.AttemptID)
	result.Verdict = strings.ToLower(strings.TrimSpace(result.Verdict))
	result.Expected = strings.TrimSpace(result.Expected)
	result.Observed = strings.TrimSpace(result.Observed)
	result.NextCheck = strings.TrimSpace(result.NextCheck)
	result.Evidence = normalizedLifecycleStrings(result.Evidence)
	if result.FindingID == "" || result.Fingerprint == "" || result.AttemptID == "" || result.Expected == "" || result.Observed == "" {
		return result, fmt.Errorf("finding_id, fingerprint, attempt_id, expected, and observed are required")
	}
	switch result.Verdict {
	case VerificationPassed, VerificationFailed:
	case VerificationInconclusive:
		if result.NextCheck == "" {
			return result, fmt.Errorf("verdict=inconclusive requires next_check")
		}
	default:
		return result, fmt.Errorf("verdict must be passed, failed, or inconclusive")
	}
	return result, nil
}

// RecordPulseReviewVerification stores one allowlisted verification as soon as
// the reviewer makes the judgment. It does not wait for or parse final output.
func RecordPulseReviewVerification(ctx context.Context, workspacePath, module, reviewRunID, pulseRunID string, input PulseReviewVerificationResult) error {
	module = pulsemodules.Normalize(module)
	if module == "" || strings.TrimSpace(reviewRunID) == "" || strings.TrimSpace(pulseRunID) == "" {
		return fmt.Errorf("module, review_run_id, and pulse_run_id are required")
	}
	verification, err := normalizePulseReviewVerification(input)
	if err != nil {
		return err
	}
	if err := validatePulseReviewVerificationAllowlistForModules(ctx, workspacePath, []string{module}, []PulseReviewVerificationResult{verification}); err != nil {
		return err
	}
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
		return err
	}
	verifications := []PulseReviewVerificationResult{}
	var encoded string
	err = db.QueryRowContext(ctx, `SELECT verifications_json FROM pulse_review_log WHERE module=? AND review_run_id=? ORDER BY _id DESC LIMIT 1`, module, strings.TrimSpace(reviewRunID)).Scan(&encoded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &verifications); err != nil {
			return fmt.Errorf("decode existing reviewer verifications: %w", err)
		}
	}
	key := verification.Fingerprint + "\x00" + verification.AttemptID
	replaced := false
	for index := range verifications {
		if verifications[index].Fingerprint+"\x00"+verifications[index].AttemptID == key {
			verifications[index] = verification
			replaced = true
			break
		}
	}
	if !replaced {
		verifications = append(verifications, verification)
	}
	return recordPulseReviewOnDB(ctx, db, module, reviewRunID, pulseRunID, "", "running", pulseReviewFindingCountFromDB(ctx, db, module, reviewRunID), verifications, time.Now().UTC().Format(time.RFC3339Nano))
}

// CompletePulseReview finalizes compact reviewer receipts. Finding and
// verification counts are computed from SQLite rows written through the typed
// tools; the caller cannot smuggle lifecycle data through verdict text.
func CompletePulseReview(ctx context.Context, workspacePath string, modules []string, reviewRunID, pulseRunID, verdict, status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "completed" && status != "failed" {
		return fmt.Errorf("status must be completed or failed")
	}
	if strings.TrimSpace(reviewRunID) == "" || strings.TrimSpace(pulseRunID) == "" {
		return fmt.Errorf("review_run_id and pulse_run_id are required")
	}
	verdict = truncateVerdict(verdict)
	if verdict == "" {
		return fmt.Errorf("verdict is required")
	}
	canonical := []string{}
	seen := map[string]bool{}
	for _, module := range modules {
		module = pulsemodules.Normalize(module)
		if module == "" || seen[module] {
			continue
		}
		seen[module] = true
		canonical = append(canonical, module)
	}
	if len(canonical) == 0 {
		return fmt.Errorf("at least one valid module is required")
	}
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
		return err
	}
	recordedAt := time.Now().UTC().Format(time.RFC3339Nano)
	for _, module := range canonical {
		verifications := []PulseReviewVerificationResult{}
		var encoded string
		if err := db.QueryRowContext(ctx, `SELECT verifications_json FROM pulse_review_log WHERE module=? AND review_run_id=? ORDER BY _id DESC LIMIT 1`, module, strings.TrimSpace(reviewRunID)).Scan(&encoded); err != nil && err != sql.ErrNoRows {
			return err
		} else if encoded != "" {
			if err := json.Unmarshal([]byte(encoded), &verifications); err != nil {
				return err
			}
		}
		if err := recordPulseReviewOnDB(ctx, db, module, reviewRunID, pulseRunID, verdict, status, pulseReviewFindingCountFromDB(ctx, db, module, reviewRunID), verifications, recordedAt); err != nil {
			return err
		}
	}
	return nil
}

func pulseReviewFindingCountFromDB(ctx context.Context, db pulseFindingLifecycleDB, module, reviewRunID string) int {
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_concerns WHERE phase=? AND step_id=? AND last_seen_run=?`, ConcernPhaseReview, pulsemodules.Normalize(module), strings.TrimSpace(reviewRunID)).Scan(&count)
	return count
}

func recordPulseReviewOnDB(
	ctx context.Context,
	db pulseFindingLifecycleDB,
	module, reviewRunID, pulseRunID, verdict, status string,
	findingCount int,
	verifications []PulseReviewVerificationResult,
	recordedAt string,
) error {
	if strings.TrimSpace(recordedAt) == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	encodedVerifications, err := json.Marshal(verifications)
	if err != nil {
		return fmt.Errorf("encode reviewer verifications: %w", err)
	}
	result, err := db.ExecContext(ctx, `UPDATE pulse_review_log SET
			pulse_run_id=?, verdict=?, status=?, finding_count=?, verification_count=?,
			verifications_json=?, recorded_at=?
		WHERE module=? AND review_run_id=?`,
		strings.TrimSpace(pulseRunID), truncateVerdict(verdict), strings.TrimSpace(status),
		findingCount, len(verifications), string(encodedVerifications), recordedAt,
		module, strings.TrimSpace(reviewRunID))
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed > 0 {
		return nil
	}
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_review_log
		(module, review_run_id, pulse_run_id, verdict, status, finding_count,
		 verification_count, verifications_json, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		module, strings.TrimSpace(reviewRunID), strings.TrimSpace(pulseRunID),
		truncateVerdict(verdict), strings.TrimSpace(status), findingCount,
		len(verifications), string(encodedVerifications), recordedAt)
	return err
}

// LoadPulseReviewReceipts returns compact reviewer receipts.
func LoadPulseReviewReceipts(ctx context.Context, workspacePath, module string, limit int) ([]PulseReviewReceipt, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulseReviewReceipt{}, err
	}
	defer db.Close()
	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
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
	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
		return nil, err
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
	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
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

func LoadPulseReviewVerificationsForPulseRun(ctx context.Context, workspacePath, pulseRunID, module string) ([]PulseReviewVerificationResult, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
		return nil, err
	}
	module = pulsemodules.Normalize(module)
	rows, err := db.QueryContext(ctx, `SELECT verifications_json FROM pulse_review_log
		WHERE pulse_run_id=? AND module=?
			AND status NOT IN ('failed', 'contract_failed')
		ORDER BY recorded_at ASC, _id ASC`, strings.TrimSpace(pulseRunID), module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []PulseReviewVerificationResult{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var parsed []PulseReviewVerificationResult
		if err := json.Unmarshal([]byte(encoded), &parsed); err != nil {
			return nil, err
		}
		results = append(results, parsed...)
	}
	return results, rows.Err()
}

// LoadModuleReviewHistory summarizes recent reviews per module, most recently run
// first, so Gate can weigh "this one keeps finding things" against "this one has
// come back clean five times".
func LoadModuleReviewHistory(ctx context.Context, workspacePath string, perModule int) ([]ModuleReviewHistory, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	if perModule <= 0 {
		perModule = 3
	}

	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
		return nil, err
	}

	// Two bounded queries instead of one unbounded full-table scan + sort.
	// The old version read every row this table has ever accumulated for
	// the workflow's entire lifetime (no WHERE, no LIMIT) just to compute a
	// per-module total and the 3 most recent verdicts in Go -- the only
	// index (module, recorded_at) can't help an ORDER BY recorded_at with
	// no module predicate. Confirmed live on the Dominion deployment
	// 2026-08-31: this was the query behind get_pulse_state's "module" view
	// hanging for 120+ seconds on a workflow with months of accumulated
	// Gate cycles, while the sibling "backlog" view -- which never touches
	// this table -- stayed fast on the identical database. `module` is
	// already canonicalized on write (ensurePulseReviewLogSchema migrates
	// old aliases in place), so grouping on the raw column is equivalent to
	// the previous Normalize-after-read behavior.
	summaryRows, err := db.QueryContext(ctx, `SELECT module, COUNT(*) AS run_count, MAX(recorded_at) AS last_ran_at
		FROM pulse_review_log
		GROUP BY module
		ORDER BY last_ran_at DESC`)
	if err != nil {
		// No table means no reviewer has run yet — that is "nothing to report",
		// not an error the Gate has to handle.
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	type moduleSummary struct {
		module    string
		runCount  int
		lastRanAt string
	}
	var summaries []moduleSummary
	for summaryRows.Next() {
		var s moduleSummary
		if err := summaryRows.Scan(&s.module, &s.runCount, &s.lastRanAt); err != nil {
			summaryRows.Close()
			return nil, err
		}
		summaries = append(summaries, s)
	}
	if err := summaryRows.Err(); err != nil {
		summaryRows.Close()
		return nil, err
	}
	summaryRows.Close()

	out := make([]ModuleReviewHistory, 0, len(summaries))
	for _, s := range summaries {
		history := ModuleReviewHistory{
			Module:        pulsemodules.Normalize(s.module),
			RunCount:      s.runCount,
			LastRanAt:     s.lastRanAt,
			RecentVerdict: []string{},
		}
		recentRows, err := db.QueryContext(ctx, `SELECT recorded_at, verdict
			FROM pulse_review_log
			WHERE module = ?
			ORDER BY recorded_at DESC, _id DESC
			LIMIT ?`, s.module, perModule)
		if err != nil {
			return nil, err
		}
		for recentRows.Next() {
			var recordedAt, verdict string
			if err := recentRows.Scan(&recordedAt, &verdict); err != nil {
				recentRows.Close()
				return nil, err
			}
			if strings.TrimSpace(verdict) == "" {
				verdict = "(ran; no verdict recorded)"
			}
			history.RecentVerdict = append(
				history.RecentVerdict,
				fmt.Sprintf("%s — %s", recordedAt, verdict),
			)
		}
		if err := recentRows.Err(); err != nil {
			recentRows.Close()
			return nil, err
		}
		recentRows.Close()
		out = append(out, history)
	}

	return out, nil
}
