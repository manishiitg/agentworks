package step_based_workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsestore"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"

	_ "modernc.org/sqlite"
)

// run_concerns holds Pulse's canonical findings: rows written by Pulse's own
// review path (RecordPulseReviewFinding), plus historical step-raised rows from
// before 2026-08-29. Steps never write here. A step raises a concern as a plain
// `CONCERNS:` line in its completion summary (scripted steps print it to
// stdout); Pulse reads those lines from retained summaries as selectors and
// decides whether each becomes a finding. There is no Go harvester and no step
// tool, so raw step text never turns into a finding on its own.

const runConcernsSchema = `CREATE TABLE IF NOT EXISTS run_concerns (
	fingerprint TEXT PRIMARY KEY,
	issue_id TEXT NOT NULL DEFAULT '',
	step_id TEXT NOT NULL DEFAULT '',
	phase TEXT NOT NULL DEFAULT '',
	group_name TEXT NOT NULL DEFAULT '',
	text TEXT NOT NULL DEFAULT '',
	first_seen_run TEXT NOT NULL DEFAULT '',
	first_seen_at TEXT NOT NULL DEFAULT '',
	last_seen_run TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT NOT NULL DEFAULT '',
	seen_count INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'open',
	resolved_at TEXT NOT NULL DEFAULT '',
	resolved_by TEXT NOT NULL DEFAULT '',
	resolution_note TEXT NOT NULL DEFAULT '',
	first_seen_platform_version TEXT NOT NULL DEFAULT ''
)`

// Phases a concern can be raised from. The step body and its two closing turns
// are separate sources: knowing a contradiction came from the learnings turn
// rather than the task itself is what tells a reviewer where to look.
const (
	ConcernPhaseExecution       = "execution"
	ConcernPhaseLearnings       = "learnings"
	ConcernPhaseKBReview        = "kb-review"
	ConcernPhaseMessageSequence = "message-sequence"
	// ConcernPhaseReview covers Pulse reviewer artifacts. The "step" for these is
	// the module name (bug_review, db_health, ...) rather than a plan step.
	ConcernPhaseReview = "review"
	// ConcernPhasePreValidation marks validation_schema failures that Go filed
	// (not an agent) before 2026-08-29. SavePreValidationLog no longer writes
	// them; LoadPriorPreValidationFailures still reads the retained rows.
	ConcernPhasePreValidation = "prevalidation"
)

const (
	ConcernStatusOpen                   = "open"
	ConcernStatusAcknowledged           = "acknowledged"
	ConcernStatusExternalActionRequired = "external_action_required"
	ConcernStatusResolved               = "resolved"
	ConcernStatusRejected               = "rejected"
)

// RunConcern is one deduplicated concern with its recurrence history.
type RunConcern struct {
	Fingerprint    string `json:"-"`
	IssueID        string `json:"issue_id"`
	StepID         string `json:"step_id"`
	Phase          string `json:"phase"`
	GroupName      string `json:"group_name,omitempty"`
	Text           string `json:"text"`
	FirstSeenRun   string `json:"first_seen_run,omitempty"`
	FirstSeenAt    string `json:"first_seen_at,omitempty"`
	LastSeenRun    string `json:"last_seen_run,omitempty"`
	LastSeenAt     string `json:"last_seen_at,omitempty"`
	SeenCount      int    `json:"seen_count"`
	Status         string `json:"status"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
	ResolvedBy     string `json:"resolved_by,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
}

// newPulseIssueID creates the durable public address for a newly discovered
// concern. It must never be derived from the concern wording: wording changes
// are reviewed semantically and may reopen or merge an existing issue, while
// the issue ID itself remains stable.
func newPulseIssueID() string {
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return fmt.Sprintf("PUL-%X", bytes)
	}
	// crypto/rand failure is exceptionally rare. The timestamp fallback keeps
	// recording concerns available rather than making a completed step fail.
	return fmt.Sprintf("PUL-%X", time.Now().UTC().UnixNano())
}

// concernFingerprint identifies "the same concern raised again".
//
// Normalization is deliberately conservative: lowercase and collapse whitespace,
// but do NOT strip digits. Stripping them would merge "12 items stale" with
// "3 items stale" — the same underlying issue at a different magnitude, where the
// magnitude is the interesting part. Under-merging shows up as visible
// near-duplicates and is recoverable; over-merging silently hides a changing
// problem behind one row.
func concernFingerprint(stepID, text string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	sum := sha256.Sum256([]byte(strings.TrimSpace(stepID) + "\x00" + normalized))
	return hex.EncodeToString(sum[:])[:16]
}

// preValidationConcernFingerprint identifies the step's output-contract gate,
// not an individual JSON path. The detailed failed checks remain in the concern
// text and the per-run pre_validation.json evidence, while one lifecycle row
// represents the repair the Fixer must make.
func preValidationConcernFingerprint(stepID string) string {
	return concernFingerprint(stepID, "prevalidation:step-output-contract")
}

// existingCanonicalReviewFingerprint preserves the identity of an exact finding
// in the same current review lane.
func existingCanonicalReviewFingerprint(ctx context.Context, db pulseFindingLifecycleDB, module, text string) string {
	wanted := strings.ToLower(strings.Join(strings.Fields(text), " "))
	args := []interface{}{ConcernPhaseReview, module}
	rows, err := db.QueryContext(ctx, `SELECT fingerprint, text FROM run_concerns
		WHERE phase=? AND step_id=?`, args...)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var fingerprint, existingText string
		if rows.Scan(&fingerprint, &existingText) == nil &&
			strings.ToLower(strings.Join(strings.Fields(existingText), " ")) == wanted {
			return fingerprint
		}
	}
	return ""
}

func runConcernsDBPath(workspacePath string) string {
	return filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/")), "db", "db.sqlite")
}

// openRunConcernsDB opens the workflow's shared db.sqlite -- the same file
// report_human_inputs.go, workspace/handlers/query.go, and
// openPlanDriftQueryOnlyDB all open, concurrently, from multiple processes
// (this server plus scheduled background Pulse jobs). Built on the shared
// sqliteopen.DSN helper (journal_mode=WAL and busy_timeout embedded in the
// DSN) rather than a bare path + a one-time runtime PRAGMA call: this was
// the last, and most-used, holdout still opening this file the old way.
// Confirmed live on the Dominion deployment 2026-08-31: a standalone
// reproduction of a query through openPlanDriftQueryOnlyDB's already-WAL-DSN
// connection was fast in isolation, but hung specifically while this
// function's live, non-WAL-DSN connections from the running server were
// concurrently active against the same file -- consistent with
// modernc.org/sqlite's own WAL/locking negotiation being connection-DSN-
// driven, not purely governed by the file's on-disk journal_mode header.
func openRunConcernsDB(ctx context.Context, workspacePath string, create bool) (*sql.DB, error) {
	_ = ctx // busy_timeout is now DSN-embedded (see sqliteopen.DSN), not a runtime PRAGMA on ctx
	dbPath := runConcernsDBPath(workspacePath)
	if create {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(dbPath))
	if err != nil {
		return nil, err
	}
	if _, err := pulsestore.Ensure(ctx, db, dbPath); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate compact Pulse store: %w", err)
	}
	return db, nil
}

func recordRunConcernLinesAtWithFingerprints(
	ctx context.Context,
	db pulseFindingLifecycleDB,
	runFolder, groupName, stepID, phase string,
	lines []string,
	observedAt string,
	fingerprints map[string]string,
) (int, error) {
	if strings.TrimSpace(observedAt) == "" {
		observedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	recorded := 0
	for _, text := range lines {
		issueID := newPulseIssueID()
		normalizedText := strings.ToLower(strings.Join(strings.Fields(text), " "))
		fp := fingerprints[normalizedText]
		if phase == ConcernPhasePreValidation {
			fp = preValidationConcernFingerprint(stepID)
		} else if fp == "" {
			fp = concernFingerprint(stepID, text)
		}
		if phase == ConcernPhaseReview && pulsemodules.IsValid(stepID) {
			if historical := existingCanonicalReviewFingerprint(ctx, db, stepID, text); historical != "" {
				fp = historical
			}
		}
		if canonical := canonicalFingerprintForMergedIssue(ctx, db, fp); canonical != "" {
			fp = canonical
		}
		previousStatus := ""
		previousStepID := ""
		if err := db.QueryRowContext(ctx, `SELECT status, step_id FROM run_concerns WHERE fingerprint=?`, fp).Scan(&previousStatus, &previousStepID); err != nil && err != sql.ErrNoRows {
			return recorded, err
		}
		// A recurring write's step_id may only UPGRADE a placeholder to a real
		// attribution, never move an already-real one. Before per-caller
		// step_id arguments existed, every finding's row got a canonical
		// module name (e.g. "plan_drift_review") as its step_id fallback; that
		// is not a real step identity, and a legacy or module-wide row stuck
		// with one would otherwise be a permanent dead end for any caller that
		// needs exact-step attribution (e.g. plan_drift_review's own
		// verifyStepDriftCheckFindingsExist) — even though reusing the same
		// fingerprint/issue for the same semantic root cause is exactly what
		// callers are told to do. A previousStepID that is NOT a canonical
		// module name is already real and must never silently move to a
		// different step just because a later call passes a different one.
		finalStepID := stepID
		if previousStepID != "" && !pulsemodules.IsValid(previousStepID) {
			finalStepID = previousStepID
		}
		// A concern that recurs after being marked resolved reopens: the fix did
		// not hold, and that is strictly more important than the original report.
		// A concern recurring while awaiting verification is the failed
		// verification signal itself, so it also returns to open.
		// A concern still recurring after the run it was waiting for means that
		// run happened and did not resolve it, so awaiting_run reopens too.
		// "rejected" is deliberately sticky — someone judged it a non-issue, and
		// recurrence is not new evidence against that judgement.
		// first_seen_platform_version is written on INSERT only; the ON CONFLICT
		// branch deliberately leaves it alone. It answers "what was this first
		// observed against", so a recurrence must not overwrite it with a newer
		// revision — that would erase exactly the signal a staleness sweep reads.
		_, err := db.ExecContext(ctx, `INSERT INTO run_concerns
			(fingerprint, issue_id, step_id, phase, group_name, text, first_seen_run, first_seen_at, last_seen_run, last_seen_at, seen_count, status, first_seen_platform_version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT(fingerprint) DO UPDATE SET
				step_id = excluded.step_id,
				text = excluded.text,
				phase = excluded.phase,
				group_name = excluded.group_name,
				last_seen_run = excluded.last_seen_run,
				last_seen_at = excluded.last_seen_at,
				seen_count = run_concerns.seen_count + CASE
					WHEN excluded.phase = 'prevalidation' AND run_concerns.last_seen_run = excluded.last_seen_run THEN 0
					ELSE 1
				END,
				status = CASE WHEN run_concerns.status IN (?, ?, ?) THEN ? ELSE run_concerns.status END`,
			fp, issueID, finalStepID, phase, groupName, text, runFolder, observedAt, runFolder, observedAt, ConcernStatusOpen, PlatformVersion(),
			ConcernStatusResolved, ConcernStatusAwaitingVerification, ConcernStatusAwaitingRun, ConcernStatusOpen)
		if err != nil {
			return recorded, err
		}
		eventType := "observed_again"
		switch previousStatus {
		case "":
			eventType = "filed"
		case ConcernStatusResolved, ConcernStatusAwaitingVerification, ConcernStatusAwaitingRun:
			eventType = "reopened"
		}
		metadata, _ := json.Marshal(map[string]string{
			"previous_status": previousStatus,
			"phase":           phase,
			"step_id":         stepID,
		})
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
			(fingerprint, pulse_run_id, event_type, summary, metadata_json, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO NOTHING`,
			fp, runFolder, eventType, text, string(metadata), observedAt); err != nil {
			return recorded, err
		}
		recorded++
	}
	return recorded, nil
}

// canonicalFingerprintForMergedIssue keeps retired semantic aliases retired.
// Merge history stores the canonical public issue_id; a later workflow
// occurrence of the old symptom must append to and reopen the canonical issue,
// not recreate the duplicate queue item.
func canonicalFingerprintForMergedIssue(ctx context.Context, db pulseFindingLifecycleDB, fingerprint string) string {
	var issueID string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(json_extract(detail_json, '$.merged_into_issue_id'), '')
		FROM pulse_finding_details WHERE fingerprint=?`, fingerprint).Scan(&issueID)
	if err != nil || strings.TrimSpace(issueID) == "" {
		return ""
	}
	var canonical string
	err = db.QueryRowContext(ctx, `SELECT fingerprint FROM run_concerns
		WHERE upper(issue_id)=upper(?)
		ORDER BY first_seen_at ASC LIMIT 1`, strings.TrimSpace(issueID)).Scan(&canonical)
	if err != nil || canonical == fingerprint {
		return ""
	}
	return canonical
}

// LoadExternallyOwnedRunConcerns returns findings Pulse has diagnosed as real
// but cannot act on in this workflow. Reviewers receive these as a suppression
// list; recurrence updates their audit history without putting them back in the
// active Pulse queue.
func LoadExternallyOwnedRunConcerns(ctx context.Context, workspacePath string) ([]RunConcern, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT fingerprint, issue_id, step_id, phase, group_name, text,
			first_seen_run, first_seen_at, last_seen_run, last_seen_at, seen_count, status,
			resolved_at, resolved_by, resolution_note
		FROM run_concerns
		WHERE status=?
		ORDER BY last_seen_at DESC, first_seen_at ASC`, ConcernStatusExternalActionRequired)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []RunConcern
	for rows.Next() {
		var concern RunConcern
		if err := rows.Scan(
			&concern.Fingerprint, &concern.IssueID, &concern.StepID, &concern.Phase, &concern.GroupName,
			&concern.Text, &concern.FirstSeenRun, &concern.FirstSeenAt,
			&concern.LastSeenRun, &concern.LastSeenAt, &concern.SeenCount,
			&concern.Status, &concern.ResolvedAt, &concern.ResolvedBy,
			&concern.ResolutionNote,
		); err != nil {
			return nil, err
		}
		out = append(out, concern)
	}
	return out, rows.Err()
}

// ResolveRunConcern records a terminal judgement on a concern.
//
// Absence never resolves a concern: a report that stops appearing may just mean
// the step did not run, or took a different path. Auto-closing on absence would
// quietly delete real findings, so closing one is always an explicit act.
func ResolveRunConcern(ctx context.Context, workspacePath, fingerprint, status, resolvedBy, note string) error {
	switch status {
	case ConcernStatusAcknowledged, ConcernStatusResolved, ConcernStatusRejected:
	default:
		return fmt.Errorf("invalid concern status %q", status)
	}
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("no workflow database at %s", runConcernsDBPath(workspacePath))
	}
	defer db.Close()

	res, err := db.ExecContext(ctx, `UPDATE run_concerns
		SET status = ?, resolved_at = ?, resolved_by = ?, resolution_note = ?
		WHERE fingerprint = ?`,
		status, time.Now().UTC().Format(time.RFC3339), resolvedBy, note, fingerprint)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no concern with fingerprint %q", fingerprint)
	}
	return nil
}

// LoadPriorPreValidationFailures returns this step's still-open prevalidation
// concerns, newest first, so the next run can be told what its last output got
// wrong.
//
// Why this exists. A scripted step that fails gets its own error handed back:
// controller_execution.go renders ScriptedPriorScript and ScriptedPriorError
// into the prompt, and the LLM repairs the script. An agentic step had no
// equivalent. It receives ValidationSchema — so it knows the shape it must
// produce — but prevalidation runs *after* it, and nothing carried the verdict
// forward. The step therefore read an identical prompt every run and wrote the
// same wrong shape every run.
//
// tectonicusadaytrading's deliver-briefing is the worked example: five concerns
// filed 2026-07-29, still open at seen_count 3 on 2026-07-30, all of the form
// "$.delivery_status must exist but was not found: unknown key". A stable
// contract mismatch that recurred purely because the only party able to fix it
// was never told.
//
// Reads run_concerns rather than the prevalidation log because these rows are
// already durable, already deduplicated by fingerprint, and already carry
// seen_count — recurrence is the strongest signal that the step is not learning,
// and it is free here.
func LoadPriorPreValidationFailures(ctx context.Context, workspacePath, stepID string, limit int) ([]RunConcern, error) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return nil, nil
	}
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.QueryContext(ctx, `SELECT fingerprint, issue_id, step_id, phase, group_name, text,
			first_seen_run, first_seen_at, last_seen_run, last_seen_at, seen_count, status,
			resolved_at, resolved_by, resolution_note
		FROM run_concerns
		WHERE step_id=? AND phase=? AND status NOT IN (?, ?, ?)
		ORDER BY seen_count DESC, last_seen_at DESC
		LIMIT ?`,
		stepID, ConcernPhasePreValidation,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired,
		limit)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []RunConcern
	for rows.Next() {
		var concern RunConcern
		if err := rows.Scan(
			&concern.Fingerprint, &concern.IssueID, &concern.StepID, &concern.Phase, &concern.GroupName,
			&concern.Text, &concern.FirstSeenRun, &concern.FirstSeenAt,
			&concern.LastSeenRun, &concern.LastSeenAt, &concern.SeenCount,
			&concern.Status, &concern.ResolvedAt, &concern.ResolvedBy,
			&concern.ResolutionNote,
		); err != nil {
			return nil, err
		}
		out = append(out, concern)
	}
	return out, rows.Err()
}

// FormatPriorPreValidationFailures renders concerns for a step prompt. Returns
// "" when there is nothing to say, so the caller can leave the template variable
// empty and the block disappears entirely rather than rendering an empty header.
func FormatPriorPreValidationFailures(concerns []RunConcern) string {
	if len(concerns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("A previous run of this step produced output that failed its own validation schema. ")
	b.WriteString("These are unresolved. Produce output that satisfies them this time; if the schema itself is wrong, say so explicitly in your summary as a CONCERNS: line rather than writing output you know will fail.\n")
	for _, concern := range concerns {
		text := strings.TrimSpace(concern.Text)
		if text == "" {
			continue
		}
		// seen_count is the part that matters most: "failed once" is noise, and
		// "failed on three consecutive runs" means the step has been reading its
		// schema and getting it wrong repeatedly.
		if concern.SeenCount > 1 {
			fmt.Fprintf(&b, "- %s (seen on %d runs)\n", text, concern.SeenCount)
			continue
		}
		fmt.Fprintf(&b, "- %s\n", text)
	}
	return strings.TrimRight(b.String(), "\n")
}
