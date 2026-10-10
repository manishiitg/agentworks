package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsestore"
)

const pulseFixAttemptsSchema = `CREATE TABLE IF NOT EXISTS pulse_fix_attempts (
	attempt_id TEXT PRIMARY KEY,
	module TEXT NOT NULL,
	pulse_run_id TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'fixing',
	intended_files_json TEXT NOT NULL DEFAULT '[]',
	changed_files_json TEXT NOT NULL DEFAULT '[]',
	before_refs_json TEXT NOT NULL DEFAULT '[]',
	after_refs_json TEXT NOT NULL DEFAULT '[]',
	started_at TEXT NOT NULL,
	completed_at TEXT NOT NULL DEFAULT ''
)`

const pulseFixAttemptFindingsSchema = `CREATE TABLE IF NOT EXISTS pulse_fix_attempt_findings (
	attempt_id TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	finding_id TEXT NOT NULL DEFAULT '',
	disposition TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (attempt_id, fingerprint)
)`

const pulseFixVerificationsSchema = `CREATE TABLE IF NOT EXISTS pulse_fix_verifications (
	_id INTEGER PRIMARY KEY AUTOINCREMENT,
	attempt_id TEXT NOT NULL DEFAULT '',
	fingerprint TEXT NOT NULL,
	check_text TEXT NOT NULL,
	verdict TEXT NOT NULL,
	expected TEXT NOT NULL DEFAULT '',
	observed TEXT NOT NULL DEFAULT '',
	evidence_json TEXT NOT NULL DEFAULT '[]',
	verified_at TEXT NOT NULL,
	UNIQUE(attempt_id, fingerprint, check_text)
)`

const pulseFindingEventsSchema = `CREATE TABLE IF NOT EXISTS pulse_finding_events (
	_id INTEGER PRIMARY KEY AUTOINCREMENT,
	fingerprint TEXT NOT NULL,
	finding_id TEXT NOT NULL DEFAULT '',
	pulse_run_id TEXT NOT NULL DEFAULT '',
	attempt_id TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	recorded_at TEXT NOT NULL,
	UNIQUE(fingerprint, pulse_run_id, attempt_id, event_type)
)`

const (
	ConcernStatusFixing               = "fixing"
	ConcernStatusAwaitingVerification = "awaiting_verification"
	ConcernStatusAwaitingRun          = "awaiting_run"
	// ConcernStatusQueuedForEngineering is actionable workflow work which was
	// deliberately not attempted in this Pulse pass. It remains visible to Gate
	// and must not be conflated with a true blocker.
	ConcernStatusQueuedForEngineering = "queued_for_engineering"
)

const (
	FindingDispositionFixedVerified        = "fixed_verified"
	FindingDispositionVerifiedNoChange     = "verified_no_change"
	FindingDispositionChangedUnverified    = "changed_unverified"
	FindingDispositionProposalOnly         = "proposal_only"
	FindingDispositionAwaitingUser         = "awaiting_user"
	FindingDispositionQueuedForEngineering = "queued_for_engineering"
	FindingDispositionBlocked              = "blocked"
	FindingDispositionExternalAction       = "external_action_required"
	// FindingDispositionAwaitingRun is retained only to read older reviewer
	// payloads. It now maps back to an active issue: a future run can rediscover
	// or clarify the problem, but it is never a closure gate.
	FindingDispositionAwaitingRun = "awaiting_run"
	FindingDispositionFailed      = "failed"
	FindingDispositionRejected    = "rejected"
)

const (
	VerificationPassed       = "passed"
	VerificationFailed       = "failed"
	VerificationInconclusive = "inconclusive"
)

// The closed sets below are the single source of truth for both the accept
// check and the rejection message. A rejection that does not name its members
// cannot be converged on: the Fixer wrote external_owner "shared workflow
// runtime" and then "server A dev voice..." across two live runs, both meaning
// platform, because nothing in the error said the set was closed or what was
// in it.
var (
	// Pulse finds an issue and closes it in the same pass: fixed, not a
	// problem, the user's decision, or platform-owned. A failed fix stays open
	// and is retried. There is no disposition that parks an issue for later.
	pulseFindingDispositionValues = []string{
		FindingDispositionFixedVerified,
		FindingDispositionVerifiedNoChange,
		FindingDispositionChangedUnverified,
		FindingDispositionAwaitingUser,
		FindingDispositionExternalAction,
		FindingDispositionFailed,
		FindingDispositionRejected,
	}
	// pulseRetiredFindingDispositions parked an issue instead of closing it.
	// They are still read from old records but refused on new writes.
	pulseRetiredFindingDispositions = map[string]string{
		FindingDispositionProposalOnly:         "proposal_only",
		FindingDispositionQueuedForEngineering: "queued_for_engineering",
		FindingDispositionAwaitingRun:          "awaiting_run",
		FindingDispositionBlocked:              "blocked",
	}
	pulseVerificationVerdictValues = []string{
		VerificationPassed,
		VerificationFailed,
		VerificationInconclusive,
	}
	pulseExternalOwnerValues = []string{"platform", "user", "vendor", "workflow_owner"}
)

func pulseAllowed(values []string) string {
	return strings.Join(values, ", ")
}

func pulseValueAllowed(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// pulseFieldArrival records what actually reached the validator for one field.
// Naming only the first missing field forces the caller to rediscover the
// contract one rejected write at a time, which is exactly how a completed fix
// ends up unrecorded.
type pulseFieldArrival struct {
	Name  string
	State string
}

func pulseStringArrival(name, value string) pulseFieldArrival {
	if strings.TrimSpace(value) == "" {
		return pulseFieldArrival{Name: name, State: "missing"}
	}
	return pulseFieldArrival{Name: name, State: "set"}
}

func pulseListArrival(name string, values []string) pulseFieldArrival {
	if len(values) == 0 {
		return pulseFieldArrival{Name: name, State: "missing"}
	}
	return pulseFieldArrival{Name: name, State: fmt.Sprintf("%d items", len(values))}
}

func pulseArrivalReport(arrivals ...pulseFieldArrival) string {
	parts := make([]string, 0, len(arrivals))
	for _, arrival := range arrivals {
		parts = append(parts, arrival.Name+"="+arrival.State)
	}
	return strings.Join(parts, ", ")
}

type PulseFixFindingRef struct {
	Fingerprint string `json:"-"`
	FindingID   string `json:"finding_id"`
	Disposition string `json:"disposition,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type PulseFixAttempt struct {
	AttemptID     string               `json:"attempt_id"`
	Module        string               `json:"module"`
	PulseRunID    string               `json:"pulse_run_id"`
	Summary       string               `json:"summary"`
	Status        string               `json:"status"`
	IntendedFiles []string             `json:"intended_files"`
	ChangedFiles  []string             `json:"changed_files"`
	BeforeRefs    []string             `json:"before_refs"`
	AfterRefs     []string             `json:"after_refs"`
	StartedAt     string               `json:"started_at"`
	CompletedAt   string               `json:"completed_at,omitempty"`
	Findings      []PulseFixFindingRef `json:"findings,omitempty"`
}

type PulseFindingVerification struct {
	Check    string   `json:"check"`
	Verdict  string   `json:"verdict"`
	Expected string   `json:"expected,omitempty"`
	Observed string   `json:"observed,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	At       string   `json:"verified_at,omitempty"`
}

type PulseFindingDisposition struct {
	Fingerprint     string   `json:"-"`
	FindingID       string   `json:"finding_id"`
	AttemptID       string   `json:"attempt_id,omitempty"`
	Disposition     string   `json:"disposition"`
	Summary         string   `json:"summary"`
	ChangedFiles    []string `json:"changed_files,omitempty"`
	BeforeRefs      []string `json:"before_refs,omitempty"`
	AfterRefs       []string `json:"after_refs,omitempty"`
	NextCheck       string   `json:"next_check,omitempty"`
	ExternalOwner   string   `json:"external_owner,omitempty"`
	ReasonCode      string   `json:"reason_code,omitempty"`
	ReopenCondition string   `json:"reopen_condition,omitempty"`
	// HumanInputID links an awaiting_user finding to the question actually put
	// to the operator. Without it "waiting on the user" is recordable while the
	// user is never asked, which is exactly what happened: latency-report held five
	// findings marked awaiting_user and zero pending questions, so the operator
	// had nothing to answer and no way to discover that.
	HumanInputID string                     `json:"human_input_id,omitempty"`
	Verification []PulseFindingVerification `json:"verification,omitempty"`
}

// ResolvePulseFindingDispositionIssueIDs turns the one public issue identity
// supplied by an agent into the internal fingerprint required by the durable
// lifecycle tables. Fingerprints remain essential for semantic deduplication,
// but they are backend plumbing: making agents copy both identifiers caused
// routine lifecycle writes to fail on an avoidable transcription mistake.
func ResolvePulseFindingDispositionIssueIDs(
	ctx context.Context,
	workspacePath string,
	dispositions []PulseFindingDisposition,
) ([]PulseFindingDisposition, error) {
	if len(dispositions) == 0 {
		return nil, nil
	}
	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil {
		return nil, err
	}

	resolved := make([]PulseFindingDisposition, len(dispositions))
	for index, disposition := range dispositions {
		disposition = NormalizePulseFindingDisposition(disposition)
		if disposition.FindingID == "" {
			return nil, fmt.Errorf("finding_dispositions[%d] requires issue_id: the visible PUL-… id from get_pulse_state(view=\"backlog\")", index)
		}
		fingerprint := ""
		for _, finding := range findings {
			if strings.EqualFold(finding.Issue.ID, disposition.FindingID) {
				fingerprint = finding.Fingerprint
				break
			}
		}
		if fingerprint == "" {
			return nil, fmt.Errorf("no Pulse issue with issue_id %q; refresh get_pulse_state(view=\"backlog\") and use its issue.id", disposition.FindingID)
		}
		disposition.Fingerprint = fingerprint
		resolved[index] = disposition
	}
	return resolved, nil
}

type PulseFindingEvent struct {
	FindingID  string                 `json:"finding_id,omitempty"`
	EventType  string                 `json:"event_type"`
	Summary    string                 `json:"summary"`
	PulseRunID string                 `json:"pulse_run_id,omitempty"`
	AttemptID  string                 `json:"attempt_id,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	RecordedAt string                 `json:"recorded_at"`
}

type PulseFindingLifecycle struct {
	Issue PulseIssue `json:"issue"`
	// Kind is the durable projection boundary between evidence emitted by a
	// workflow step and an issue Pulse has actually accepted for lifecycle
	// work. Both species intentionally share the same ledger so promotion keeps
	// the original evidence history; callers must not flatten them into one
	// backlog count.
	Kind            string                     `json:"kind"`
	Fingerprint     string                     `json:"-"`
	IssueID         string                     `json:"issue_id"`
	FindingID       string                     `json:"finding_id,omitempty"`
	Module          string                     `json:"module,omitempty"`
	StepID          string                     `json:"step_id"`
	Phase           string                     `json:"phase"`
	GroupName       string                     `json:"group_name,omitempty"`
	Text            string                     `json:"text"`
	Status          string                     `json:"status"`
	FirstSeenRun    string                     `json:"first_seen_run,omitempty"`
	FirstSeenAt     string                     `json:"first_seen_at,omitempty"`
	LastSeenRun     string                     `json:"last_seen_run,omitempty"`
	LastSeenAt      string                     `json:"last_seen_at,omitempty"`
	SeenCount       int                        `json:"seen_count"`
	ResolutionNote  string                     `json:"resolution_note,omitempty"`
	ExternalOwner   string                     `json:"external_owner,omitempty"`
	ReasonCode      string                     `json:"reason_code,omitempty"`
	ReopenCondition string                     `json:"reopen_condition,omitempty"`
	Details         *PulseFindingDetails       `json:"details,omitempty"`
	Attempts        []PulseFixAttempt          `json:"fix_attempts"`
	Verification    []PulseFindingVerification `json:"verifications"`
	Events          []PulseFindingEvent        `json:"events"`
}

const (
	PulseFindingKindIssue       = "issue"
	PulseFindingKindObservation = "observation"
)

// PulseFindingKindForLifecycle classifies the projection, not the underlying
// row. A workflow observation becomes an issue only when a reviewer explicitly
// promotes it, Pulse starts lifecycle work on it, or it originated from a
// reviewer. This preserves a single auditable history without making every
// CONCERNS line a repair ticket.
func PulseFindingKindForLifecycle(finding PulseFindingLifecycle) string {
	if finding.Phase == ConcernPhaseReview || len(finding.Attempts) > 0 {
		return PulseFindingKindIssue
	}
	for _, event := range finding.Events {
		switch strings.TrimSpace(event.EventType) {
		case "promoted_to_issue", "duplicates_merged", "fix_started", "updated",
			"closed", "verification_failed", "verification_inconclusive",
			"proposal_recorded", "awaiting_user", "queued_for_engineering",
			"blocked", "awaiting_run", "external_action_required", "reopened":
			return PulseFindingKindIssue
		}
	}
	return PulseFindingKindObservation
}

func IsPulseIssue(finding PulseFindingLifecycle) bool {
	return PulseFindingKindForLifecycle(finding) == PulseFindingKindIssue
}

func SplitPulseFindingLifecycles(findings []PulseFindingLifecycle) (issues, observations []PulseFindingLifecycle) {
	issues = make([]PulseFindingLifecycle, 0, len(findings))
	observations = make([]PulseFindingLifecycle, 0, len(findings))
	for _, finding := range findings {
		if IsPulseIssue(finding) {
			issues = append(issues, finding)
		} else {
			observations = append(observations, finding)
		}
	}
	return issues, observations
}

// ResolvePulseFindingIssueID translates the one public Pulse identity into the
// internal lifecycle row. Fingerprints remain database implementation details:
// callers and agents must never copy them out of a backlog response.
func ResolvePulseFindingIssueID(ctx context.Context, workspacePath, issueID string) (PulseFindingLifecycle, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return PulseFindingLifecycle{}, fmt.Errorf("issue_id is required: use the visible issue.id from get_pulse_state(view=\"backlog\")")
	}
	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil {
		return PulseFindingLifecycle{}, err
	}
	matches := make([]PulseFindingLifecycle, 0, 1)
	for _, finding := range findings {
		if strings.EqualFold(NewPulseIssue(finding).ID, issueID) {
			matches = append(matches, finding)
		}
	}
	if len(matches) == 0 {
		db, err := openRunConcernsDB(ctx, workspacePath, false)
		if err != nil {
			return PulseFindingLifecycle{}, err
		}
		if db != nil {
			defer db.Close()
			var fingerprint string
			err := db.QueryRowContext(ctx, `SELECT fingerprint FROM pulse_finding_issue_aliases WHERE issue_id=?`, issueID).Scan(&fingerprint)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return PulseFindingLifecycle{}, err
			}
			if err == nil {
				for _, finding := range findings {
					if finding.Fingerprint == fingerprint {
						matches = append(matches, finding)
					}
				}
			}
		}
	}
	switch len(matches) {
	case 1:
		if matches[0].Details != nil && strings.TrimSpace(matches[0].Details.MergedIntoIssueID) != "" {
			return PulseFindingLifecycle{}, fmt.Errorf("Pulse issue %q was merged into %s; update the canonical issue instead", issueID, matches[0].Details.MergedIntoIssueID)
		}
		return matches[0], nil
	case 0:
		return PulseFindingLifecycle{}, fmt.Errorf("no Pulse issue with issue_id %q; refresh get_pulse_state(view=\"backlog\") and use issue.id exactly", issueID)
	default:
		return PulseFindingLifecycle{}, fmt.Errorf("issue_id %q resolves to %d lifecycle rows; run the Pulse backlog consolidation before updating it", issueID, len(matches))
	}
}

// MergePulseFindingIssues retires symptom-level duplicates without deleting
// their evidence. The semantic decision is agent-owned; this function only
// validates PUL identities, preserves the old history, and links each retired
// record to its canonical root cause.
func MergePulseFindingIssues(ctx context.Context, workspacePath, canonicalIssueID string, duplicateIssueIDs []string, reason string) (int, error) {
	canonical, err := ResolvePulseFindingIssueID(ctx, workspacePath, canonicalIssueID)
	if err != nil {
		return 0, err
	}
	canonicalIssueID = NewPulseIssue(canonical).ID
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, fmt.Errorf("merge reason is required: state the shared root cause")
	}
	if len(duplicateIssueIDs) == 0 {
		return 0, fmt.Errorf("duplicate_issue_ids must contain at least one PUL issue to merge")
	}
	seen := map[string]bool{}
	duplicates := make([]PulseFindingLifecycle, 0, len(duplicateIssueIDs))
	for _, duplicateIssueID := range duplicateIssueIDs {
		duplicateIssueID = strings.TrimSpace(duplicateIssueID)
		if duplicateIssueID == "" || seen[strings.ToUpper(duplicateIssueID)] {
			continue
		}
		seen[strings.ToUpper(duplicateIssueID)] = true
		if strings.EqualFold(duplicateIssueID, canonicalIssueID) {
			return 0, fmt.Errorf("canonical issue %q cannot also be a duplicate", canonicalIssueID)
		}
		duplicate, lookupErr := ResolvePulseFindingIssueID(ctx, workspacePath, duplicateIssueID)
		if lookupErr != nil {
			return 0, lookupErr
		}
		duplicates = append(duplicates, duplicate)
	}
	if len(duplicates) == 0 {
		return 0, fmt.Errorf("no distinct duplicate issue ids were supplied")
	}

	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return 0, err
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, duplicate := range duplicates {
		details := PulseFindingDetails{}
		if duplicate.Details != nil {
			details = *duplicate.Details
		}
		details.MergedIntoIssueID = canonicalIssueID
		detailJSON, marshalErr := json.Marshal(normalizePulseFindingDetails(details))
		if marshalErr != nil {
			return 0, marshalErr
		}
		note := fmt.Sprintf("Merged into %s: %s", canonicalIssueID, reason)
		if _, err := tx.ExecContext(ctx, `UPDATE run_concerns
			SET status=?, resolved_at=?, resolved_by=?, resolution_note=? WHERE fingerprint=?`,
			ConcernStatusResolved, now, "pulse_backlog_consolidation", note, duplicate.Fingerprint); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pulse_finding_details
			(fingerprint, finding_id, issue_kind, target_key, detail_json, source_run_id, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(fingerprint) DO UPDATE SET detail_json=excluded.detail_json, updated_at=excluded.updated_at`,
			duplicate.Fingerprint, details.FindingID, details.IssueKind, details.TargetKey, string(detailJSON), duplicate.LastSeenRun, now); err != nil {
			return 0, err
		}
		metadata, _ := json.Marshal(map[string]string{"canonical_issue_id": canonicalIssueID, "reason": reason})
		if _, err := tx.ExecContext(ctx, `INSERT INTO pulse_finding_events
			(fingerprint, finding_id, pulse_run_id, event_type, summary, metadata_json, recorded_at)
			VALUES (?, ?, '', 'merged_duplicate', ?, ?, ?)
			ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO NOTHING`,
			duplicate.Fingerprint, NewPulseIssue(duplicate).ID, note, string(metadata), now); err != nil {
			return 0, err
		}
	}
	metadata, _ := json.Marshal(map[string]interface{}{"merged_count": len(duplicates), "reason": reason})
	_, err = tx.ExecContext(ctx, `INSERT INTO pulse_finding_events
		(fingerprint, finding_id, pulse_run_id, event_type, summary, metadata_json, recorded_at)
		VALUES (?, ?, '', 'duplicates_merged', ?, ?, ?)
		ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO NOTHING`,
		canonical.Fingerprint, canonicalIssueID, fmt.Sprintf("Merged %d duplicate issue(s)", len(duplicates)), string(metadata), now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(duplicates), nil
}

// PulseReviewVerificationCandidate is the backend-owned allowlist entry for a
// reviewer checking a prior fix. A review may call record_pulse_verification only
// for one of these exact tuples; ordinary open, blocked, rejected, and already
// resolved findings have no attempt to verify.
type PulseReviewVerificationCandidate struct {
	FindingID   string `json:"finding_id"`
	Fingerprint string `json:"-"`
	AttemptID   string `json:"attempt_id"`
	NextCheck   string `json:"next_check"`
}

// ResolvePulseReviewVerificationIssueID resolves the one public issue identity
// a reviewer supplies into the exact changed_unverified attempt it is allowed
// to verify. Attempt and fingerprint identifiers are lifecycle internals; a
// reviewer should never have to copy them from a backlog payload.
func ResolvePulseReviewVerificationIssueID(
	ctx context.Context,
	workspacePath, module, issueID string,
) (PulseReviewVerificationCandidate, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return PulseReviewVerificationCandidate{}, fmt.Errorf("issue_id is required: use the visible issue.id from get_pulse_state(view=\"backlog\")")
	}
	candidates, err := LoadPulseReviewVerificationCandidates(ctx, workspacePath, module)
	if err != nil {
		return PulseReviewVerificationCandidate{}, err
	}
	matched := []PulseReviewVerificationCandidate{}
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.FindingID, issueID) {
			matched = append(matched, candidate)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return PulseReviewVerificationCandidate{}, fmt.Errorf("issue_id %q has no pending verification in module %q; refresh get_pulse_state(view=\"backlog\") and record a normal finding result instead if it is not awaiting proof", issueID, pulsemodules.Normalize(module))
	default:
		return PulseReviewVerificationCandidate{}, fmt.Errorf("issue_id %q has %d pending verification attempts in module %q; this is a backend lifecycle inconsistency", issueID, len(matched), pulsemodules.Normalize(module))
	}
}

type pulseFindingLifecycleDB interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

// migrateRunConcernPlatformVersionColumn adds first_seen_platform_version to
// databases created before PLAT-072.
//
// Existing rows keep an empty value, which is correct and must stay that way:
// their platform revision is genuinely unknown and back-filling the current one
// would assert they were first seen against today's build — the exact false
// claim the column exists to prevent. Those rows continue to be triaged by
// reading; only findings recorded from now on can be judged mechanically.
func migrateRunConcernPlatformVersionColumn(ctx context.Context, db pulseFindingLifecycleDB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(run_concerns)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	present := false
	for rows.Next() {
		var (
			cid                 int
			name, colType       string
			notNull, primaryKey int
			defaultValue        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "first_seen_platform_version" {
			present = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if present {
		return nil
	}
	_, err = db.ExecContext(ctx,
		`ALTER TABLE run_concerns ADD COLUMN first_seen_platform_version TEXT NOT NULL DEFAULT ''`)
	return err
}

func ensurePulseFindingLifecycleSchema(ctx context.Context, db pulseFindingLifecycleDB) error {
	for _, ddl := range []string{
		runConcernsSchema,
		pulseFixAttemptsSchema,
		pulseFixAttemptFindingsSchema,
		pulseFixVerificationsSchema,
		pulseFindingEventsSchema,
		pulseFindingDetailsSchema,
		pulseFindingIssueAliasesSchema,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	if err := migrateRunConcernPlatformVersionColumn(ctx, db); err != nil {
		return err
	}
	if err := migrateRunConcernIssueIDs(ctx, db); err != nil {
		return err
	}
	// Each migration is probed with a SELECT first: this runs on every open,
	// including pure reads, and a no-op UPDATE still takes the write lock (see
	// pulseRowsExist).
	if err := pulseExecIfRows(ctx, db,
		`SELECT 1 FROM run_concerns WHERE phase=? AND step_id IN (?, ?)`,
		[]interface{}{ConcernPhaseReview, pulsemodules.LegacyStrategyAuditorID, pulsemodules.LegacyGoalAdvisorID},
		`UPDATE run_concerns SET step_id=?
		WHERE phase=? AND step_id IN (?, ?)`, pulsemodules.StrategicReviewID,
		ConcernPhaseReview, pulsemodules.LegacyStrategyAuditorID, pulsemodules.LegacyGoalAdvisorID); err != nil {
		return err
	}
	if err := pulseExecIfRows(ctx, db,
		`SELECT 1 FROM run_concerns WHERE phase=? AND step_id IN (?, ?)`,
		[]interface{}{ConcernPhaseReview, pulsemodules.LegacyWorkflowReviewID, pulsemodules.LegacyLLMOpsReviewID},
		`UPDATE run_concerns SET step_id=?
		WHERE phase=? AND step_id IN (?, ?)`, pulsemodules.TechnicalReviewID,
		ConcernPhaseReview, pulsemodules.LegacyWorkflowReviewID, pulsemodules.LegacyLLMOpsReviewID); err != nil {
		return err
	}
	if err := pulseExecIfRows(ctx, db,
		`SELECT 1 FROM pulse_fix_attempts WHERE module IN (?, ?)`,
		[]interface{}{pulsemodules.LegacyWorkflowReviewID, pulsemodules.LegacyLLMOpsReviewID},
		`UPDATE pulse_fix_attempts SET module=? WHERE module IN (?, ?)`,
		pulsemodules.TechnicalReviewID, pulsemodules.LegacyWorkflowReviewID, pulsemodules.LegacyLLMOpsReviewID); err != nil {
		return err
	}
	if err := migratePreValidationConcernGranularity(ctx, db); err != nil {
		return err
	}
	if err := migrateDuplicatePulseFindingIdentities(ctx, db); err != nil {
		return err
	}
	if err := migrateOrphanedPulseFindingEvents(ctx, db); err != nil {
		return err
	}
	if err := migrateUnlinkedAwaitingUserFindings(ctx, db); err != nil {
		return err
	}
	if err := migrateAppliedPulseFixesClosed(ctx, db); err != nil {
		return err
	}
	if err := migrateMergedPulseAliasesClosed(ctx, db); err != nil {
		return err
	}
	for _, ddl := range []string{
		`CREATE INDEX IF NOT EXISTS idx_pulse_fix_attempts_module_run ON pulse_fix_attempts(module, pulse_run_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_pulse_fix_findings_fingerprint ON pulse_fix_attempt_findings(fingerprint, attempt_id)`,
		`CREATE INDEX IF NOT EXISTS idx_pulse_fix_verifications_fingerprint ON pulse_fix_verifications(fingerprint, verified_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_pulse_finding_events_fingerprint ON pulse_finding_events(fingerprint, recorded_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_pulse_finding_details_finding_id ON pulse_finding_details(lower(finding_id)) WHERE finding_id<>''`,
		`CREATE INDEX IF NOT EXISTS idx_pulse_finding_details_target_key ON pulse_finding_details(target_key) WHERE target_key<>''`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	if concrete, ok := db.(*sql.DB); ok {
		if err := pulsestore.RefreshCompatibility(ctx, concrete); err != nil {
			return fmt.Errorf("install compact Pulse compatibility: %w", err)
		}
	}
	return nil
}

// PulseLifecycleReconciliation is the resulting state of the idempotent
// close-on-applied compatibility pass for one workflow.
type PulseLifecycleReconciliation struct {
	TotalIssues           int `json:"total_issues"`
	ActiveIssues          int `json:"active_issues"`
	ClosedIssues          int `json:"closed_issues"`
	AppliedClosures       int `json:"applied_closures"`
	ReopenedWaitingIssues int `json:"reopened_waiting_issues"`
	RetiredAliases        int `json:"retired_aliases"`
}

// PulseActionableBacklogReconciliation is the product-level projection used
// when upgrading the old Pulse register.  Earlier versions let untyped step
// observations and shared-platform defects remain in the same active count as
// workflow repairs.  That made a workflow look permanently broken even when
// Pulse had no workflow artifact it could safely change.
//
// The migration never deletes evidence.  It retires only legacy observations
// that were never promoted into a typed canonical finding, and hands typed
// harness findings to their platform owner.  Decision and evidence routes are
// retained as visible non-actionable work for the person or future review that
// owns them.
type PulseActionableBacklogReconciliation struct {
	PulseLifecycleReconciliation
	RetiredLegacyObservations int `json:"retired_legacy_observations"`
	PlatformHandoffs          int `json:"platform_handoffs"`
	ActionableWorkflowIssues  int `json:"actionable_workflow_issues"`
	HumanDecisions            int `json:"human_decisions"`
	EvidenceWaits             int `json:"evidence_waits"`
	// Retired evidence waits: closed when the review found no problem,
	// otherwise handed to the fixer for this pass.
	ClosedEvidenceWaits   int `json:"closed_evidence_waits"`
	ReroutedEvidenceWaits int `json:"rerouted_evidence_waits"`
	// Issues parked by a retired disposition (queued, blocked, proposal,
	// awaiting a run) returned to the active register to be fixed now.
	ReopenedParkedIssues int `json:"reopened_parked_issues"`
}

// ReconcilePulseFindingLifecycle runs the compatibility migrations explicitly
// for one workflow. The workflow-contract upgrade invokes this once; ordinary
// lifecycle reads retain the same ensure call as a recovery path for restored
// or previously missed databases.
func ReconcilePulseFindingLifecycle(ctx context.Context, workspacePath string) (PulseLifecycleReconciliation, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return PulseLifecycleReconciliation{}, err
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return PulseLifecycleReconciliation{}, err
	}

	result := PulseLifecycleReconciliation{}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN status NOT IN (?, ?, ?) THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status IN (?, ?, ?) THEN 1 ELSE 0 END), 0)
		FROM run_concerns`, ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired,
	).Scan(&result.TotalIssues, &result.ActiveIssues, &result.ClosedIssues); err != nil {
		return PulseLifecycleReconciliation{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_finding_events
		WHERE pulse_run_id='migration:close-applied-fixes' AND event_type='fix_applied'`).Scan(&result.AppliedClosures); err != nil {
		return PulseLifecycleReconciliation{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_finding_events
		WHERE pulse_run_id='migration:reopen-unfixed-waits' AND event_type='reopened_for_review'`).Scan(&result.ReopenedWaitingIssues); err != nil {
		return PulseLifecycleReconciliation{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_concerns
		WHERE resolved_by='pulse_backlog_consolidation'
		AND resolution_note LIKE 'Retired semantic alias%'`).Scan(&result.RetiredAliases); err != nil {
		return PulseLifecycleReconciliation{}, err
	}
	return result, nil
}

// ReconcilePulseActionableBacklog upgrades the durable register to the
// actionable-work model.  A workflow-owned repair is the only thing that
// counts toward Pulse's "to fix" target.  Platform work and human decisions
// remain durable, but are deliberately outside that target.
func ReconcilePulseActionableBacklog(ctx context.Context, workspacePath string) (PulseActionableBacklogReconciliation, error) {
	base, err := ReconcilePulseFindingLifecycle(ctx, workspacePath)
	if err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	defer db.Close()

	result := PulseActionableBacklogReconciliation{PulseLifecycleReconciliation: base}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Normal workflow execution used to write free-text CONCERNS rows directly
	// into run_concerns. Those rows are audit evidence, not accepted issues: a
	// reviewer never assigned ownership, a route, or a safe repair boundary.
	// Keep any row that already has an attempt, because it did enter lifecycle
	// work; otherwise retire it instead of presenting it as a new bug forever.
	legacy, err := db.ExecContext(ctx, `UPDATE run_concerns SET
		status=?, resolved_at=?, resolved_by='pulse_actionable_backlog_migration',
		resolution_note='Retired legacy observation: it was never promoted to a typed canonical Pulse finding.'
		WHERE status NOT IN (?, ?, ?)
			AND NOT EXISTS (SELECT 1 FROM pulse_finding_details d WHERE d.fingerprint=run_concerns.fingerprint)
			AND NOT EXISTS (SELECT 1 FROM pulse_fix_attempt_findings af WHERE af.fingerprint=run_concerns.fingerprint)`,
		ConcernStatusRejected, now,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired)
	if err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if affected, affectedErr := legacy.RowsAffected(); affectedErr == nil {
		result.RetiredLegacyObservations = int(affected)
	}

	// Steps could also file typed rows through the retired record_run_concern
	// tool. They carry a details record but were never routed by a reviewer
	// either, so they are the same unowned observations. Retire them unless a
	// reviewer routed them or a repair was attempted. Prevalidation history
	// stays: LoadPriorPreValidationFailures reads it.
	stepTyped, err := db.ExecContext(ctx, `UPDATE run_concerns SET
		status=?, resolved_at=?, resolved_by='pulse_actionable_backlog_migration',
		resolution_note='Retired step-raised observation: filed by a step, never routed by a Pulse reviewer.'
		WHERE status NOT IN (?, ?, ?)
			AND phase IN (?, ?, ?, ?)
			AND EXISTS (SELECT 1 FROM pulse_finding_details d WHERE d.fingerprint=run_concerns.fingerprint
				AND COALESCE(json_extract(d.detail_json, '$.recommended_route'), '')='')
			AND NOT EXISTS (SELECT 1 FROM pulse_fix_attempt_findings af WHERE af.fingerprint=run_concerns.fingerprint)`,
		ConcernStatusRejected, now,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired,
		ConcernPhaseExecution, ConcernPhaseMessageSequence, ConcernPhaseLearnings, ConcernPhaseKBReview)
	if err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if affected, affectedErr := stepTyped.RowsAffected(); affectedErr == nil {
		result.RetiredLegacyObservations += int(affected)
	}

	// A typed harness issue is explicit evidence that the shared platform owns
	// the failed boundary. It must not remain eligible for a per-workflow fixer.
	platform, err := db.ExecContext(ctx, `UPDATE run_concerns SET
		status=?, resolved_at=?, resolved_by='pulse_platform',
		resolution_note='Platform-owned finding: retained for the platform register and excluded from this workflow repair backlog.'
		WHERE status NOT IN (?, ?, ?)
			AND EXISTS (
				SELECT 1 FROM pulse_finding_details d
				WHERE d.fingerprint=run_concerns.fingerprint AND d.issue_kind=?
			)`, ConcernStatusExternalActionRequired, now,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired, IssueKindHarness)
	if err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if affected, affectedErr := platform.RowsAffected(); affectedErr == nil {
		result.PlatformHandoffs = int(affected)
	}

	// Pulse no longer parks issues waiting for evidence. A wait whose review
	// found no problem is closed; every other wait goes to the fixer so this
	// pass repairs it or asks the user.
	closedWaits, err := db.ExecContext(ctx, `UPDATE run_concerns SET
		status=?, resolved_at=?, resolved_by='pulse_evidence_wait_retirement',
		resolution_note='Closed: the review found no problem to fix. Pulse no longer keeps issues open waiting for evidence.'
		WHERE status NOT IN (?, ?, ?)
			AND EXISTS (SELECT 1 FROM pulse_finding_details d WHERE d.fingerprint=run_concerns.fingerprint
				AND json_extract(d.detail_json, '$.recommended_route')=?
				AND COALESCE(json_extract(d.detail_json, '$.classification'), '') IN ('no_material_problem', 'no_issue', 'no_problem'))`,
		ConcernStatusResolved, now,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired, pulseFindingRouteEvidenceWait)
	if err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if affected, affectedErr := closedWaits.RowsAffected(); affectedErr == nil {
		result.ClosedEvidenceWaits = int(affected)
	}
	// A parked wait was "acknowledged"; handing it to the fixer reopens it.
	if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=?
		WHERE status=? AND EXISTS (SELECT 1 FROM pulse_finding_details d WHERE d.fingerprint=run_concerns.fingerprint
			AND json_extract(d.detail_json, '$.recommended_route')=?)`,
		ConcernStatusOpen, ConcernStatusAcknowledged, pulseFindingRouteEvidenceWait); err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	rerouted, err := db.ExecContext(ctx, `UPDATE pulse_finding_details SET
		detail_json=json_set(detail_json, '$.recommended_route', ?), updated_at=?
		WHERE json_extract(detail_json, '$.recommended_route')=?
			AND EXISTS (SELECT 1 FROM run_concerns c WHERE c.fingerprint=pulse_finding_details.fingerprint
				AND c.status NOT IN (?, ?, ?))`,
		pulseFindingRouteFixerHandoff, now, pulseFindingRouteEvidenceWait,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired)
	if err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if affected, affectedErr := rerouted.RowsAffected(); affectedErr == nil {
		result.ReroutedEvidenceWaits = int(affected)
	}

	// Retired parking states return to the active register. An acknowledged
	// issue stays only when it waits on a real user decision.
	parked, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=?
		WHERE status IN (?, ?, ?)
			OR (status=?
				AND NOT EXISTS (SELECT 1 FROM pulse_finding_details d WHERE d.fingerprint=run_concerns.fingerprint
					AND json_extract(d.detail_json, '$.recommended_route')=?)
				AND NOT EXISTS (SELECT 1 FROM pulse_finding_events e WHERE e.fingerprint=run_concerns.fingerprint
					AND e.event_type='awaiting_user'))`,
		ConcernStatusOpen,
		ConcernStatusQueuedForEngineering, ConcernStatusAwaitingRun, ConcernStatusAwaitingVerification,
		ConcernStatusAcknowledged, pulseFindingRouteDecisionRequired)
	if err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if affected, affectedErr := parked.RowsAffected(); affectedErr == nil {
		result.ReopenedParkedIssues = int(affected)
	}

	const active = `c.status NOT IN ('resolved', 'rejected', 'external_action_required')`
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_concerns c
		JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint
		WHERE `+active+` AND d.issue_kind=?
			AND COALESCE(json_extract(d.detail_json, '$.recommended_route'), '') NOT IN (?, ?)`,
		IssueKindWorkflow, pulseFindingRouteDecisionRequired, pulseFindingRouteEvidenceWait,
	).Scan(&result.ActionableWorkflowIssues); err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_concerns c
		JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint
		WHERE `+active+` AND COALESCE(json_extract(d.detail_json, '$.recommended_route'), '')=?`,
		pulseFindingRouteDecisionRequired,
	).Scan(&result.HumanDecisions); err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_concerns c
		JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint
		WHERE `+active+` AND COALESCE(json_extract(d.detail_json, '$.recommended_route'), '')=?`,
		pulseFindingRouteEvidenceWait,
	).Scan(&result.EvidenceWaits); err != nil {
		return PulseActionableBacklogReconciliation{}, err
	}
	return result, nil
}

// CountPulseActionableWorkflowIssues is the scheduler's narrow completion
// check. It intentionally excludes platform-owned findings, human decisions,
// and evidence waits: those remain durable work, but cannot be completed by a
// workflow repair agent in this run.
// CountPulseActionableWorkflowIssuesForPass counts the workflow issues a
// Technical pass must close: every open workflow issue except one waiting on
// the user's decision. Pulse finds and closes issues in the same pass, so an
// open issue left behind means the pass did not finish.
func CountPulseActionableWorkflowIssuesForPass(ctx context.Context, workspacePath string, since time.Time) (int, error) {
	_ = since // every open workflow issue belongs to the pass; see the comment above
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return 0, err
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return 0, err
	}
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_concerns c
		JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint
		WHERE c.status NOT IN (?, ?, ?, ?)
			AND d.issue_kind=?
			AND COALESCE(json_extract(d.detail_json, '$.recommended_route'), '')<>?`,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired, ConcernStatusAcknowledged,
		IssueKindWorkflow, pulseFindingRouteDecisionRequired,
	).Scan(&count)
	return count, err
}

// PulseActionableIssue is one open workflow issue the Technical drain owns,
// with the last time anything happened to it (seen again or any recorded
// event).
type PulseActionableIssue struct {
	ID           string
	LastActivity time.Time
}

// ListPulseActionableWorkflowIssues returns the issues
// CountPulseActionableWorkflowIssuesForPass counts, so a failed drain can name
// them and a fix run can tell whether any changed since its last attempt.
func ListPulseActionableWorkflowIssues(ctx context.Context, workspacePath string) ([]PulseActionableIssue, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT c.fingerprint, c.issue_id, c.last_seen_at,
			COALESCE((SELECT MAX(e.recorded_at) FROM pulse_finding_events e WHERE e.fingerprint=c.fingerprint), '')
		FROM run_concerns c
		JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint
		WHERE c.status NOT IN (?, ?, ?, ?)
			AND d.issue_kind=?
			AND COALESCE(json_extract(d.detail_json, '$.recommended_route'), '')<>?
		ORDER BY c.issue_id`,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired, ConcernStatusAcknowledged,
		IssueKindWorkflow, pulseFindingRouteDecisionRequired,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var issues []PulseActionableIssue
	for rows.Next() {
		var fingerprint, issueID, lastSeen, lastEvent string
		if err := rows.Scan(&fingerprint, &issueID, &lastSeen, &lastEvent); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(issueID)
		if id == "" {
			id = fingerprint
		}
		issue := PulseActionableIssue{ID: id}
		for _, raw := range []string{lastSeen, lastEvent} {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z"} {
				if at, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
					if at.After(issue.LastActivity) {
						issue.LastActivity = at
					}
					break
				}
			}
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func CountPulseActionableWorkflowIssues(ctx context.Context, workspacePath string) (int, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return 0, err
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return 0, err
	}
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_concerns c
		JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint
		WHERE c.status NOT IN (?, ?, ?)
			AND d.issue_kind=?
			AND COALESCE(json_extract(d.detail_json, '$.recommended_route'), '') NOT IN (?, ?)`,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired,
		IssueKindWorkflow, pulseFindingRouteDecisionRequired, pulseFindingRouteEvidenceWait,
	).Scan(&count)
	return count, err
}

// migrateRunConcernIssueIDs introduces a stored public ID without changing a
// user's existing PUL links. Fingerprint remains an internal compatibility
// join key for old companion tables; new lifecycle and agent code address the
// concern by issue_id.
func migrateRunConcernIssueIDs(ctx context.Context, db pulseFindingLifecycleDB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(run_concerns)`)
	if err != nil {
		return err
	}
	hasIssueID := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		hasIssueID = hasIssueID || name == "issue_id"
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasIssueID {
		if _, err := db.ExecContext(ctx, `ALTER TABLE run_concerns ADD COLUMN issue_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return pulseExecIfRows(ctx, db,
		`SELECT 1 FROM run_concerns WHERE trim(issue_id)=''`, nil,
		`UPDATE run_concerns SET issue_id='PUL-' || upper(substr(fingerprint, 1, 8))
		WHERE trim(issue_id)=''`)
}

// migrateMergedPulseAliasesClosed repairs aliases that old recurrence handling
// reopened after backlog consolidation. Their evidence remains durable, but
// only the canonical issue may return to the active register.
func migrateMergedPulseAliasesClosed(ctx context.Context, db pulseFindingLifecycleDB) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return pulseExecIfRows(ctx, db,
		`SELECT 1 FROM run_concerns
		WHERE fingerprint IN (
			SELECT fingerprint FROM pulse_finding_details
			WHERE COALESCE(json_extract(detail_json, '$.merged_into_issue_id'), '')<>''
		) AND status NOT IN (?, ?, ?)`,
		[]interface{}{ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired},
		`UPDATE run_concerns SET
		status=?, resolved_at=?, resolved_by='pulse_backlog_consolidation',
		resolution_note='Retired semantic alias; later evidence is attached to its canonical issue_id.'
		WHERE fingerprint IN (
			SELECT fingerprint FROM pulse_finding_details
			WHERE COALESCE(json_extract(detail_json, '$.merged_into_issue_id'), '')<>''
		) AND status NOT IN (?, ?, ?)`, ConcernStatusResolved, now,
		ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired)
}

// migrateAppliedPulseFixesClosed adopts the issue-register lifecycle for
// repairs recorded under the former verification-gated policy. Once a Fixer
// successfully wrote changed files, the issue is closed; a later occurrence
// reopens the same issue through the normal concern recorder. Keeping these
// rows awaiting_verification created a second backlog whose only purpose was to
// prove work Pulse had already completed.
func migrateAppliedPulseFixesClosed(ctx context.Context, db pulseFindingLifecycleDB) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Every statement here is idempotent and normally matches nothing; probe
	// first so an already-migrated database never takes the write lock on a
	// read path (see pulseRowsExist).
	appliedFixesPending, err := pulseRowsExist(ctx, db, `SELECT 1
		FROM run_concerns c
		JOIN pulse_fix_attempt_findings af ON af.fingerprint=c.fingerprint
		JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
		WHERE c.status NOT IN (?, ?, ?) AND af.disposition=?
			AND a.changed_files_json NOT IN ('', '[]', 'null')
			AND NOT EXISTS (
				SELECT 1 FROM pulse_finding_events e
				WHERE e.fingerprint=c.fingerprint AND e.event_type='reopened'
					AND e.recorded_at>a.completed_at
			)`, ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired, FindingDispositionChangedUnverified)
	if err != nil {
		return err
	}
	if appliedFixesPending {
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
		(fingerprint, finding_id, pulse_run_id, attempt_id, event_type, summary, metadata_json, recorded_at)
		SELECT c.fingerprint, af.finding_id, 'migration:close-applied-fixes', af.attempt_id,
			'fix_applied', 'Applied repair closed under the issue-register lifecycle.',
			'{"policy":"close_on_applied_fix"}', ?
		FROM run_concerns c
		JOIN pulse_fix_attempt_findings af ON af.fingerprint=c.fingerprint
		JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
		WHERE c.status NOT IN (?, ?, ?) AND af.disposition=?
			AND a.changed_files_json NOT IN ('', '[]', 'null')
			AND NOT EXISTS (
				SELECT 1 FROM pulse_finding_events e
				WHERE e.fingerprint=c.fingerprint AND e.event_type='reopened'
					AND e.recorded_at>a.completed_at
			)
		ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO NOTHING`,
			now, ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired, FindingDispositionChangedUnverified); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET
		status=?, resolved_at=?, resolved_by='workflow_builder',
		resolution_note='Applied repair closed; normal concern recurrence will reopen this issue.'
		WHERE status NOT IN (?, ?, ?) AND EXISTS (
			SELECT 1 FROM pulse_fix_attempt_findings af
			JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
			WHERE af.fingerprint=run_concerns.fingerprint
				AND af.disposition=?
				AND a.changed_files_json NOT IN ('', '[]', 'null')
				AND NOT EXISTS (
					SELECT 1 FROM pulse_finding_events e
					WHERE e.fingerprint=run_concerns.fingerprint AND e.event_type='reopened'
						AND e.recorded_at>a.completed_at
				)
		)`, ConcernStatusResolved, now, ConcernStatusResolved, ConcernStatusRejected, ConcernStatusExternalActionRequired, FindingDispositionChangedUnverified); err != nil {
			return err
		}
	}
	if err := pulseExecIfRows(ctx, db,
		`SELECT 1 FROM pulse_fix_attempts
		WHERE status=? AND EXISTS (
			SELECT 1 FROM pulse_fix_attempt_findings af
			JOIN run_concerns c ON c.fingerprint=af.fingerprint
			WHERE af.attempt_id=pulse_fix_attempts.attempt_id
				AND af.disposition=? AND c.status=?
		)`, []interface{}{ConcernStatusAwaitingVerification, FindingDispositionChangedUnverified, ConcernStatusResolved},
		`UPDATE pulse_fix_attempts SET status='applied'
		WHERE status=? AND EXISTS (
			SELECT 1 FROM pulse_fix_attempt_findings af
			JOIN run_concerns c ON c.fingerprint=af.fingerprint
			WHERE af.attempt_id=pulse_fix_attempts.attempt_id
				AND af.disposition=? AND c.status=?
		)`, ConcernStatusAwaitingVerification, FindingDispositionChangedUnverified, ConcernStatusResolved); err != nil {
		return err
	}
	// A legacy wait with no applied repair is still an active problem. Keeping
	// it out of the ordinary queue made old findings wait forever even after
	// their workflow ran many times. The current lifecycle closes repairs at
	// application time; it does not use a future run as a second closure gate.
	unfixedWaitsPending, err := pulseRowsExist(ctx, db, `SELECT 1
		FROM run_concerns c
		WHERE c.status=? AND NOT EXISTS (
			SELECT 1 FROM pulse_fix_attempt_findings af
			JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
			WHERE af.fingerprint=c.fingerprint
				AND af.disposition=?
				AND a.changed_files_json NOT IN ('', '[]', 'null')
		)`, ConcernStatusAwaitingRun, FindingDispositionChangedUnverified)
	if err != nil || !unfixedWaitsPending {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
		(fingerprint, finding_id, pulse_run_id, event_type, summary, metadata_json, recorded_at)
		SELECT c.fingerprint, c.issue_id, 'migration:reopen-unfixed-waits', 'reopened_for_review',
			'Legacy wait had no applied repair; returned to the active issue register.',
			'{"policy":"unfixed_waits_are_active"}', ?
		FROM run_concerns c
		WHERE c.status=? AND NOT EXISTS (
			SELECT 1 FROM pulse_fix_attempt_findings af
			JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
			WHERE af.fingerprint=c.fingerprint
				AND af.disposition=?
				AND a.changed_files_json NOT IN ('', '[]', 'null')
		)
		ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO NOTHING`,
		now, ConcernStatusAwaitingRun, FindingDispositionChangedUnverified); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE run_concerns SET
		status=?, resolution_note='Legacy wait had no applied repair; returned to the active issue register.'
		WHERE status=? AND NOT EXISTS (
			SELECT 1 FROM pulse_fix_attempt_findings af
			JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
			WHERE af.fingerprint=run_concerns.fingerprint
				AND af.disposition=?
				AND a.changed_files_json NOT IN ('', '[]', 'null')
		)`, ConcernStatusOpen, ConcernStatusAwaitingRun, FindingDispositionChangedUnverified)
	return err
}

// migrateUnlinkedAwaitingUserFindings repairs the legacy state where Pulse
// marked a finding as awaiting_user without creating an answerable human-input
// request. The label is not a request: it must point at a real row before the
// operator can act. Do not manufacture a question here; only the reviewer that
// understands the finding may decide what should be asked.
//
// This is deliberately idempotent. Once an invalid record is moved back to
// Pulse's queue, it no longer matches the acknowledged/awaiting_user source
// state. An answered request is not migrated: it is a real decision awaiting
// the normal decision-drain turn, not a missing request.
func migrateUnlinkedAwaitingUserFindings(ctx context.Context, db pulseFindingLifecycleDB) error {
	var humanInputsTableCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='report_human_inputs'`).Scan(&humanInputsTableCount); err != nil {
		return err
	}
	// A workflow can open its lifecycle database before the interactive
	// human-input feature has ever created its table. There is nothing to
	// reconcile yet, and treating that as a migration failure would block every
	// ordinary Pulse read.
	if humanInputsTableCount == 0 {
		return nil
	}

	type legacyDecision struct {
		fingerprint string
		findingID   string
		pulseRunID  string
		metadata    string
	}
	rows, err := db.QueryContext(ctx, `SELECT c.fingerprint, e.finding_id, e.pulse_run_id, e.metadata_json
		FROM run_concerns c
		JOIN pulse_finding_events e ON e._id = (
			SELECT latest._id FROM pulse_finding_events latest
			WHERE latest.fingerprint=c.fingerprint
			ORDER BY latest.recorded_at DESC, latest._id DESC
			LIMIT 1
		)
		WHERE c.status=? AND e.event_type=?`, ConcernStatusAcknowledged, FindingDispositionAwaitingUser)
	if err != nil {
		return err
	}
	defer rows.Close()

	legacy := []legacyDecision{}
	for rows.Next() {
		var item legacyDecision
		if err := rows.Scan(&item.fingerprint, &item.findingID, &item.pulseRunID, &item.metadata); err != nil {
			return err
		}
		legacy = append(legacy, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, item := range legacy {
		metadata := map[string]interface{}{}
		_ = json.Unmarshal([]byte(item.metadata), &metadata)
		humanInputID, _ := metadata["human_input_id"].(string)
		humanInputID = strings.TrimSpace(humanInputID)

		// A real request may already be answered and waiting for the schedule's
		// decision-drain turn. Leave that state alone; it is not the historical
		// missing-request defect this migration repairs.
		if humanInputID != "" {
			var requestCount int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM report_human_inputs WHERE id=?`, humanInputID).Scan(&requestCount); err != nil {
				return err
			}
			if requestCount == 1 {
				continue
			}
		}

		note := "Decision request missing: Pulse must re-review this finding and create a linked, answerable human request only if a decision is still needed."
		if _, err := db.ExecContext(ctx, `UPDATE run_concerns
			SET status=?, resolution_note=?, resolved_at='', resolved_by=''
			WHERE fingerprint=? AND status=?`,
			ConcernStatusQueuedForEngineering, note, item.fingerprint, ConcernStatusAcknowledged); err != nil {
			return err
		}
		migrationMetadata, _ := json.Marshal(map[string]string{
			"reason":         "missing_human_input_request",
			"human_input_id": humanInputID,
		})
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
			(fingerprint, finding_id, pulse_run_id, event_type, summary, metadata_json, recorded_at)
			VALUES (?, ?, ?, 'decision_request_missing', ?, ?, ?)
			ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO NOTHING`,
			item.fingerprint, item.findingID, item.pulseRunID, note, string(migrationMetadata), now); err != nil {
			return err
		}
	}
	return nil
}

// migratePreValidationConcernGranularity folds the legacy one-row-per-failed-
// field shape into one lifecycle finding per step. Individual field failures
// remain in pulse_finding_events and the retained per-run pre_validation.json
// logs; the active issue tracker represents the one output-contract repair.
func migratePreValidationConcernGranularity(ctx context.Context, db pulseFindingLifecycleDB) error {
	type legacyRow struct {
		Fingerprint  string
		StepID       string
		FirstSeenRun string
		LastSeenRun  string
	}
	rows, err := db.QueryContext(ctx, `SELECT fingerprint, step_id, first_seen_run, last_seen_run
		FROM run_concerns WHERE phase=? ORDER BY step_id, first_seen_at, fingerprint`, ConcernPhasePreValidation)
	if err != nil {
		return err
	}
	groups := map[string][]legacyRow{}
	for rows.Next() {
		var row legacyRow
		if err := rows.Scan(&row.Fingerprint, &row.StepID, &row.FirstSeenRun, &row.LastSeenRun); err != nil {
			rows.Close()
			return err
		}
		groups[row.StepID] = append(groups[row.StepID], row)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for stepID, legacy := range groups {
		target := preValidationConcernFingerprint(stepID)
		if len(legacy) == 1 && legacy[0].Fingerprint == target {
			continue
		}
		identityRows := make([]pulseIdentityRow, 0, len(legacy))
		runs := map[string]bool{}
		fingerprints := make([]string, 0, len(legacy))
		for _, row := range legacy {
			identityRows = append(identityRows, pulseIdentityRow{Fingerprint: row.Fingerprint, StepID: stepID})
			fingerprints = append(fingerprints, row.Fingerprint)
			if strings.TrimSpace(row.FirstSeenRun) != "" {
				runs[row.FirstSeenRun] = true
			}
			if strings.TrimSpace(row.LastSeenRun) != "" {
				runs[row.LastSeenRun] = true
			}
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(fingerprints)), ",")
		args := make([]interface{}, 0, len(fingerprints))
		for _, fingerprint := range fingerprints {
			args = append(args, fingerprint)
		}
		eventRows, err := db.QueryContext(ctx, `SELECT DISTINCT pulse_run_id FROM pulse_finding_events
			WHERE fingerprint IN (`+placeholders+`) AND pulse_run_id<>''`, args...)
		if err != nil {
			return err
		}
		for eventRows.Next() {
			var runID string
			if err := eventRows.Scan(&runID); err != nil {
				eventRows.Close()
				return err
			}
			runs[runID] = true
		}
		if err := eventRows.Close(); err != nil {
			return err
		}
		if err := mergePulseIdentityGroup(ctx, db, target, identityRows); err != nil {
			return err
		}
		seenCount := len(runs)
		if seenCount == 0 {
			seenCount = 1
		}
		text := fmt.Sprintf("prevalidation gate failed for the step output contract; %d legacy field-level finding(s) were consolidated. Inspect the linked event history and per-run pre_validation.json for every failed check", len(legacy))
		if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET text=?, phase=?, seen_count=? WHERE fingerprint=?`,
			text, ConcernPhasePreValidation, seenCount, target); err != nil {
			return err
		}
	}
	return nil
}

type pulseIdentityRow struct {
	Fingerprint string
	FindingID   string
	IssueKind   string
	TargetKey   string
	StepID      string
}

func migrateDuplicatePulseFindingIdentities(ctx context.Context, db pulseFindingLifecycleDB) error {
	rows, err := db.QueryContext(ctx, `SELECT d.fingerprint, d.finding_id, COALESCE(d.issue_kind, ''), d.target_key, COALESCE(c.step_id, '')
		FROM pulse_finding_details d LEFT JOIN run_concerns c USING (fingerprint)
		ORDER BY d.fingerprint`)
	if err != nil {
		return err
	}
	var identities []pulseIdentityRow
	for rows.Next() {
		var row pulseIdentityRow
		if err := rows.Scan(&row.Fingerprint, &row.FindingID, &row.IssueKind, &row.TargetKey, &row.StepID); err != nil {
			rows.Close()
			return err
		}
		identities = append(identities, row)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	groups := map[string][]pulseIdentityRow{}
	for _, row := range identities {
		value := strings.TrimSpace(row.FindingID)
		if value != "" {
			groups[strings.ToLower(value)] = append(groups[strings.ToLower(value)], row)
		}
	}
	// A harness finding can be split across the fingerprint boundary without
	// ever acquiring a human-assigned finding_id (PLAT-073 cluster I,
	// f2cbf9a1: HARNESS-REFDOC-REVIEW-ARTIFACT-DRIFT existed as two rows,
	// neither with finding_id set, so the pass above could not see them as
	// duplicates). target_key is the identity harness findings actually
	// share in that case — fall back to it, scoped to IssueKindHarness only
	// so a coincidental target_key match between unrelated workflow findings
	// is never merged.
	for _, row := range identities {
		if strings.TrimSpace(row.FindingID) != "" || row.IssueKind != IssueKindHarness {
			continue
		}
		key := strings.TrimSpace(row.TargetKey)
		if key == "" {
			continue
		}
		groupKey := "target_key:" + strings.ToLower(key)
		groups[groupKey] = append(groups[groupKey], row)
	}
	for _, group := range groups {
		// A visible issue id (or, for harness findings, a shared target_key)
		// is an alias for a durable identity, never source material for a new
		// fingerprint. The old migration derived `target` from finding_id
		// even for a singleton, so every PUL-* ID it generated was re-hashed
		// on the next read. Only genuine duplicate legacy rows need
		// collapsing; retain the first stored fingerprint as canonical.
		if len(group) < 2 {
			continue
		}
		target := group[0].Fingerprint
		if err := mergePulseIdentityGroup(ctx, db, target, group); err != nil {
			return err
		}
	}
	return nil
}

// migrateOrphanedPulseFindingEvents finishes identity migrations that were
// interrupted after the canonical detail/concern row moved but before every
// historical event moved. Those orphan events used to be projected as a
// second live lifecycle item even though the canonical finding itself was
// already unique.
func migrateOrphanedPulseFindingEvents(ctx context.Context, db pulseFindingLifecycleDB) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT e.fingerprint, d.fingerprint
		FROM pulse_finding_events e
		JOIN pulse_finding_details d ON lower(d.finding_id)=lower(e.finding_id)
		WHERE e.finding_id<>'' AND e.fingerprint<>d.fingerprint
		ORDER BY e.fingerprint`)
	if err != nil {
		return err
	}
	type eventMove struct{ old, target string }
	var moves []eventMove
	for rows.Next() {
		var move eventMove
		if err := rows.Scan(&move.old, &move.target); err != nil {
			rows.Close()
			return err
		}
		moves = append(moves, move)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, move := range moves {
		// A duplicate event tuple is retained as explicit migration history. It
		// must not remain under the old fingerprint, where backlog projection can
		// mistake it for another actionable case.
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO pulse_finding_events
			(fingerprint,finding_id,pulse_run_id,attempt_id,event_type,summary,metadata_json,recorded_at)
			SELECT ?,e.finding_id,e.pulse_run_id,e.attempt_id,e.event_type||':identity_merge:'||substr(e.fingerprint,1,8),e.summary,e.metadata_json,e.recorded_at
			FROM pulse_finding_events e WHERE e.fingerprint=? AND EXISTS (
				SELECT 1 FROM pulse_finding_events t WHERE t.fingerprint=? AND t.pulse_run_id=e.pulse_run_id AND t.attempt_id=e.attempt_id AND t.event_type=e.event_type
			)`, move.target, move.old, move.target); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO pulse_finding_events
			(fingerprint,finding_id,pulse_run_id,attempt_id,event_type,summary,metadata_json,recorded_at)
			SELECT ?,finding_id,pulse_run_id,attempt_id,event_type,summary,metadata_json,recorded_at
			FROM pulse_finding_events WHERE fingerprint=?`, move.target, move.old); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM pulse_finding_events WHERE fingerprint=?`, move.old); err != nil {
			return err
		}
	}
	return nil
}

func mergePulseIdentityGroup(ctx context.Context, db pulseFindingLifecycleDB, target string, group []pulseIdentityRow) error {
	initialized := false
	for _, row := range group {
		if row.Fingerprint == target {
			initialized = true
			break
		}
	}
	for _, row := range group {
		old := row.Fingerprint
		if old == target {
			continue
		}
		if !initialized {
			// Target columns are named rather than relying on positional order, so
			// adding a column to run_concerns cannot silently break this copy with
			// a "table has N columns but M values were supplied" error.
			if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO run_concerns
				(fingerprint, issue_id, step_id, phase, group_name, text, first_seen_run, first_seen_at, last_seen_run, last_seen_at, seen_count, status, resolved_at, resolved_by, resolution_note, first_seen_platform_version)
				SELECT ?, issue_id, step_id, phase, group_name, text, first_seen_run, first_seen_at, last_seen_run, last_seen_at, seen_count, status, resolved_at, resolved_by, resolution_note, first_seen_platform_version
				FROM run_concerns WHERE fingerprint=?`, target, old); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO pulse_finding_details
				SELECT ?, finding_id, issue_kind, target_key, detail_json, source_run_id, updated_at
				FROM pulse_finding_details WHERE fingerprint=?`, target, old); err != nil {
				return err
			}
			initialized = true
		} else {
			if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET
				seen_count=seen_count+COALESCE((SELECT seen_count FROM run_concerns WHERE fingerprint=?),0),
				first_seen_at=MIN(first_seen_at, COALESCE((SELECT first_seen_at FROM run_concerns WHERE fingerprint=?),first_seen_at)),
				last_seen_at=MAX(last_seen_at, COALESCE((SELECT last_seen_at FROM run_concerns WHERE fingerprint=?),last_seen_at)),
				last_seen_run=COALESCE((SELECT last_seen_run FROM run_concerns WHERE fingerprint=?),last_seen_run),
				text=COALESCE((SELECT text FROM run_concerns WHERE fingerprint=?),text),
				status=CASE WHEN (SELECT status FROM run_concerns WHERE fingerprint=?) IN ('open','fixing','awaiting_verification','awaiting_run','external_action_required') THEN (SELECT status FROM run_concerns WHERE fingerprint=?) ELSE status END
				WHERE fingerprint=?`, old, old, old, old, old, old, old, target); err != nil {
				return err
			}
		}
		for _, stmt := range []string{
			`INSERT OR IGNORE INTO pulse_fix_attempt_findings SELECT attempt_id, ?, finding_id, disposition, summary FROM pulse_fix_attempt_findings WHERE fingerprint=?`,
			`INSERT OR IGNORE INTO pulse_fix_verifications (attempt_id,fingerprint,check_text,verdict,expected,observed,evidence_json,verified_at) SELECT attempt_id,?,check_text,verdict,expected,observed,evidence_json,verified_at FROM pulse_fix_verifications WHERE fingerprint=?`,
		} {
			if _, err := db.ExecContext(ctx, stmt, target, old); err != nil {
				return err
			}
		}
		// Preserve every historical event. If the canonical row already has the
		// same unique event tuple, retain the twin as an explicit migration event
		// instead of silently dropping it through INSERT OR IGNORE.
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO pulse_finding_events
			(fingerprint,finding_id,pulse_run_id,attempt_id,event_type,summary,metadata_json,recorded_at)
			SELECT ?,e.finding_id,e.pulse_run_id,e.attempt_id,e.event_type||':identity_merge:'||substr(e.fingerprint,1,8),e.summary,e.metadata_json,e.recorded_at
			FROM pulse_finding_events e WHERE e.fingerprint=? AND EXISTS (
				SELECT 1 FROM pulse_finding_events t WHERE t.fingerprint=? AND t.pulse_run_id=e.pulse_run_id AND t.attempt_id=e.attempt_id AND t.event_type=e.event_type
			)`, target, old, target); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO pulse_finding_events
			(fingerprint,finding_id,pulse_run_id,attempt_id,event_type,summary,metadata_json,recorded_at)
			SELECT ?,finding_id,pulse_run_id,attempt_id,event_type,summary,metadata_json,recorded_at FROM pulse_finding_events WHERE fingerprint=?`, target, old); err != nil {
			return err
		}
		if err := preserveMergedPulseIssueID(ctx, db, old, target); err != nil {
			return err
		}
		for _, stmt := range []string{
			`DELETE FROM pulse_fix_attempt_findings WHERE fingerprint=?`,
			`DELETE FROM pulse_fix_verifications WHERE fingerprint=?`,
			`DELETE FROM pulse_finding_events WHERE fingerprint=?`,
			`DELETE FROM pulse_finding_details WHERE fingerprint=?`,
			`DELETE FROM run_concerns WHERE fingerprint=?`,
		} {
			if _, err := db.ExecContext(ctx, stmt, old); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizedLifecycleStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// NormalizePulseFindingDisposition canonicalizes tool-provided lifecycle data
// before either validation or persistence. Keeping this at the write boundary
// prevents a value accepted after trimming from being routed by its original,
// untrimmed spelling.
func NormalizePulseFindingDisposition(disposition PulseFindingDisposition) PulseFindingDisposition {
	disposition.Fingerprint = strings.TrimSpace(disposition.Fingerprint)
	disposition.FindingID = strings.TrimSpace(disposition.FindingID)
	disposition.AttemptID = strings.TrimSpace(disposition.AttemptID)
	disposition.Disposition = strings.TrimSpace(disposition.Disposition)
	disposition.Summary = strings.TrimSpace(disposition.Summary)
	disposition.ChangedFiles = normalizedLifecycleStrings(disposition.ChangedFiles)
	disposition.BeforeRefs = normalizedLifecycleStrings(disposition.BeforeRefs)
	disposition.AfterRefs = normalizedLifecycleStrings(disposition.AfterRefs)
	disposition.NextCheck = strings.TrimSpace(disposition.NextCheck)
	disposition.ExternalOwner = strings.TrimSpace(disposition.ExternalOwner)
	disposition.ReasonCode = strings.TrimSpace(disposition.ReasonCode)
	disposition.ReopenCondition = strings.TrimSpace(disposition.ReopenCondition)
	disposition.HumanInputID = strings.TrimSpace(disposition.HumanInputID)
	for index := range disposition.Verification {
		verification := &disposition.Verification[index]
		verification.Check = strings.TrimSpace(verification.Check)
		verification.Verdict = strings.TrimSpace(verification.Verdict)
		verification.Expected = strings.TrimSpace(verification.Expected)
		verification.Observed = strings.TrimSpace(verification.Observed)
		verification.Evidence = normalizedLifecycleStrings(verification.Evidence)
	}
	return disposition
}

func lifecycleJSON(values []string) string {
	encoded, _ := json.Marshal(normalizedLifecycleStrings(values))
	return string(encoded)
}

func lifecycleAttemptID(pulseRunID, module string, findings []PulseFixFindingRef, intendedFiles, beforeRefs []string) string {
	keys := make([]string, 0, len(findings))
	for _, finding := range findings {
		keys = append(keys, strings.TrimSpace(finding.Fingerprint))
	}
	sort.Strings(keys)
	files := normalizedLifecycleStrings(intendedFiles)
	refs := normalizedLifecycleStrings(beforeRefs)
	sort.Strings(files)
	sort.Strings(refs)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(pulseRunID),
		strings.TrimSpace(module),
		strings.Join(keys, "\x00"),
		strings.Join(files, "\x00"),
		strings.Join(refs, "\x00"),
	}, "\x01")))
	return "fix-" + hex.EncodeToString(sum[:])[:16]
}

// pulseDispositionOpensAttempt reports whether a disposition asserts that files
// were changed, which is the only case that needs a fix-attempt record created
// for it when none already exists.
func pulseDispositionOpensAttempt(disposition string) bool {
	switch strings.TrimSpace(disposition) {
	case FindingDispositionFixedVerified, FindingDispositionChangedUnverified:
		return true
	default:
		return false
	}
}

// pulseDispositionSettlesAttempt reports whether a disposition is a verdict on
// an attempt already in flight. These reuse the module's open attempt instead of
// opening a second one, so a later run verifying an earlier changed_unverified
// fix closes the attempt that made the change rather than inventing a fresh
// record for work already done.
func pulseDispositionSettlesAttempt(disposition string) bool {
	switch strings.TrimSpace(disposition) {
	case FindingDispositionFixedVerified, FindingDispositionChangedUnverified,
		FindingDispositionVerifiedNoChange, FindingDispositionFailed:
		return true
	default:
		return false
	}
}

// resolvePulseFixAttemptTx returns the attempt a disposition is recorded
// against, creating one when the disposition claims changed files and the module
// has no open attempt for this finding.
//
// The agent used to declare this itself through a start_pulse_fix_attempt tool
// call before mutating. That write-ahead bought one narrow property — the
// declaration preceded the mutation — and cost a whole error class: an attempt
// id the agent had to carry across turns, and two rejections ("requires
// attempt_id and changed_files", "no fix attempt %q for finding %q") that fired
// after the repair had already been made and could not then be recorded. Nothing
// reads an orphaned attempt: there is no reaper, no incomplete-attempt query, and
// no crash-recovery path, so the ordering guarantee was never load-bearing. The
// record is now created here, at the same moment the disposition that describes
// it is written.
func resolvePulseFixAttemptTx(
	ctx context.Context,
	db pulseFindingLifecycleDB,
	module, pulseRunID, concernStatus string,
	disposition PulseFindingDisposition,
	recordedAt string,
) (string, error) {
	if !pulseDispositionSettlesAttempt(disposition.Disposition) {
		return "", nil
	}
	rows, err := db.QueryContext(ctx, `SELECT a.attempt_id, a.module
		FROM pulse_fix_attempt_findings af
		JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
		WHERE af.fingerprint=? AND a.status IN (?, ?)
		ORDER BY a.started_at DESC`,
		disposition.Fingerprint, ConcernStatusFixing, ConcernStatusAwaitingVerification)
	if err != nil {
		return "", err
	}
	existing := ""
	for rows.Next() {
		var attemptID, attemptModule string
		if err := rows.Scan(&attemptID, &attemptModule); err != nil {
			rows.Close()
			return "", err
		}
		// An attempt belongs to the module that made it; letting another module
		// settle it would let one module take credit for work it did not do.
		if existing == "" && pulsemodules.Normalize(attemptModule) == module {
			existing = attemptID
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if existing != "" {
		return existing, nil
	}
	if !pulseDispositionOpensAttempt(disposition.Disposition) {
		return "", nil
	}
	if concernStatus == ConcernStatusRejected {
		return "", fmt.Errorf("concern %q was rejected and cannot enter fixing without new adjudication; re-file it with new evidence before recording a fix for finding %q",
			disposition.Fingerprint, disposition.FindingID)
	}

	findings := []PulseFixFindingRef{{Fingerprint: disposition.Fingerprint, FindingID: disposition.FindingID}}
	attemptID := lifecycleAttemptID(pulseRunID, module, findings, disposition.ChangedFiles, disposition.BeforeRefs)
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_fix_attempts
		(attempt_id, module, pulse_run_id, summary, status, intended_files_json, before_refs_json, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(attempt_id) DO NOTHING`,
		attemptID, module, strings.TrimSpace(pulseRunID), disposition.Summary,
		ConcernStatusFixing, lifecycleJSON(disposition.ChangedFiles),
		lifecycleJSON(disposition.BeforeRefs), recordedAt); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_fix_attempt_findings
		(attempt_id, fingerprint, finding_id) VALUES (?, ?, ?)
		ON CONFLICT(attempt_id, fingerprint) DO UPDATE SET finding_id=excluded.finding_id`,
		attemptID, disposition.Fingerprint, disposition.FindingID); err != nil {
		return "", err
	}
	metadata, _ := json.Marshal(map[string]interface{}{
		"intended_files": normalizedLifecycleStrings(disposition.ChangedFiles),
		"before_refs":    normalizedLifecycleStrings(disposition.BeforeRefs),
	})
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
		(fingerprint, finding_id, pulse_run_id, attempt_id, event_type, summary, metadata_json, recorded_at)
		VALUES (?, ?, ?, ?, 'fix_started', ?, ?, ?)
		ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO NOTHING`,
		disposition.Fingerprint, disposition.FindingID, pulseRunID, attemptID,
		disposition.Summary, string(metadata), recordedAt); err != nil {
		return "", err
	}
	return attemptID, nil
}

// validateFindingDisposition reports every structural problem with one
// disposition, not just the first.
//
// It used to return on the first violation. On 2026-08-04 one finding
// (PUL-70B1057E) took four sequential record_pulse_result rejections over 24
// minutes to satisfy: wrong disposition value, then a verification proof that
// didn't match the reviewer's evidence, then a next_check that didn't match the
// reviewer's boundary text (all from validateReviewerVerificationDispositions,
// which has the same fix below), and only after all three passed did this
// function's own missing-changed_files check surface — a structural problem
// that had been true the entire time but was never reached. Four round trips to
// learn four facts a single rejection could have stated together, on ONE
// finding.
//
// That anti-pattern already had one fix in this codebase: the result=changed
// check three functions away in pulse_worklist.go merged three separate
// "X is required" rejections into one message naming the whole required set.
// This applies the same treatment here.
func validateFindingDisposition(disposition PulseFindingDisposition) error {
	disposition = NormalizePulseFindingDisposition(disposition)
	var problems []string
	add := func(format string, args ...interface{}) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if disposition.Fingerprint == "" || disposition.FindingID == "" {
		add("requires issue_id from get_pulse_state(view=\"backlog\"); lifecycle identity was not resolved (got %s)",
			pulseArrivalReport(pulseStringArrival("issue_id", disposition.FindingID)))
	}
	if disposition.Summary == "" {
		add("requires summary: one sentence stating what was done and what it means for this finding")
	}
	dispositionKnown := pulseValueAllowed(disposition.Disposition, pulseFindingDispositionValues)
	if _, retired := pulseRetiredFindingDispositions[strings.TrimSpace(disposition.Disposition)]; retired {
		add("disposition %q is retired: Pulse does not park issues for a later pass. Fix it now (fixed_verified or changed_unverified), close it when nothing is wrong (verified_no_change or rejected), ask the user with a linked decision (awaiting_user), hand a platform-owned defect off (external_action_required), or record failed so it stays open and is retried", disposition.Disposition)
	} else if !dispositionKnown {
		add("has invalid disposition %q. Must be one of: %s", disposition.Disposition, pulseAllowed(pulseFindingDispositionValues))
	}

	passed, failed, inconclusive := 0, 0, 0
	for index, verification := range disposition.Verification {
		if strings.TrimSpace(verification.Check) == "" {
			add("verification[%d] requires check naming what was run or inspected; each verification entry is {\"check\", \"verdict\", \"expected\", \"observed\", \"evidence\"} where check and verdict are required and verdict is one of: %s",
				index, pulseAllowed(pulseVerificationVerdictValues))
			continue
		}
		switch strings.TrimSpace(verification.Verdict) {
		case VerificationPassed:
			passed++
		case VerificationFailed:
			failed++
		case VerificationInconclusive:
			inconclusive++
		default:
			add("verification[%d] has invalid verdict %q. Must be one of: %s", index, verification.Verdict, pulseAllowed(pulseVerificationVerdictValues))
		}
	}
	verdictCounts := fmt.Sprintf("passed=%d, failed=%d, inconclusive=%d", passed, failed, inconclusive)

	// Type-specific rules only apply once the disposition value itself is
	// known-good — checking them against an invalid value would report
	// requirements for a type the agent may not have meant.
	if dispositionKnown {
		switch disposition.Disposition {
		case FindingDispositionFixedVerified:
			if len(disposition.ChangedFiles) == 0 {
				add("fixed_verified requires changed_files (got %s): the exact workspace-relative files this fix changed. Use verified_no_change when a check proved the problem is absent without changing any file",
					pulseArrivalReport(pulseListArrival("changed_files", disposition.ChangedFiles)))
			}
			if len(disposition.BeforeRefs) != len(disposition.AfterRefs) {
				add("fixed_verified requires before_refs and after_refs as equal-length positional pairs (got before_refs=%d, after_refs=%d); supply the matching after_ref for each before_ref, or omit both arrays",
					len(disposition.BeforeRefs), len(disposition.AfterRefs))
			}
			if passed == 0 || failed > 0 || inconclusive > 0 {
				add("fixed_verified requires at least one passed verification and no failed or inconclusive check (got %s). Use changed_unverified when the proof has not arrived yet, or failed when a check failed", verdictCounts)
			}
		case FindingDispositionChangedUnverified:
			if len(disposition.ChangedFiles) == 0 {
				add("changed_unverified requires changed_files (got %s): the exact workspace-relative files this fix changed. Use awaiting_run when nothing was changed and only a scheduled run can produce the evidence",
					pulseArrivalReport(pulseListArrival("changed_files", disposition.ChangedFiles)))
			}
			if len(disposition.BeforeRefs) != len(disposition.AfterRefs) {
				add("changed_unverified requires before_refs and after_refs as equal-length positional pairs (got before_refs=%d, after_refs=%d); supply the matching after_ref for each before_ref, or omit both arrays",
					len(disposition.BeforeRefs), len(disposition.AfterRefs))
			}
			// Applying a repair closes the issue. Future normal workflow evidence
			// reopens the same issue when the concern recurs; Pulse does not keep a
			// second active verification queue or schedule a dedicated proof run.
			// A known failed immediate check still means the repair was not applied
			// successfully and must remain open.
			if failed > 0 {
				add("changed_unverified cannot contain a failed immediate check (got %s). Use failed when the applied change did not pass its immediate checks", verdictCounts)
			}
		case FindingDispositionVerifiedNoChange:
			if passed == 0 || failed > 0 || inconclusive > 0 {
				add("verified_no_change requires at least one passed verification and no failed or inconclusive check (got %s). verified_no_change means a check proved this is not (or is no longer) a problem without changing any file", verdictCounts)
			}
		case FindingDispositionFailed:
			if len(disposition.Verification) > 0 && failed == 0 {
				add("failed supplied %d verification entries but none with verdict %q (got %s); include the check that failed, or omit verification entirely when no check was run",
					len(disposition.Verification), VerificationFailed, verdictCounts)
			}
		case FindingDispositionAwaitingRun:
			// Naming the evidence boundary is what separates waiting from stalling:
			// without it nobody can tell whether the run that would resolve this has
			// already happened.
			if disposition.NextCheck == "" {
				add("awaiting_run requires next_check naming the run or evidence that will resolve it")
			}
			if len(disposition.ChangedFiles) > 0 {
				add("awaiting_run changed files; a finding with a fix applied is changed_unverified, not awaiting_run")
			}
		case FindingDispositionQueuedForEngineering:
			if disposition.NextCheck == "" {
				add("queued_for_engineering requires next_check naming the next Engineering/Pulse pass or concrete repair boundary; use blocked only when no safe action exists")
			}
			if len(disposition.ChangedFiles) > 0 {
				add("queued_for_engineering changed files; a finding with a repair applied is changed_unverified or fixed_verified, not queued_for_engineering")
			}
		case FindingDispositionAwaitingUser:
			// A finding cannot wait on a decision nobody was asked for. Requiring
			// the question id here is what turns "awaiting_user" from a label into
			// something the operator can actually act on.
			if disposition.HumanInputID == "" {
				add("awaiting_user requires human_input_id: create the decision with create_human_input_request first, or use blocked/proposal_only if no question is being asked")
			}
		case FindingDispositionExternalAction:
			if disposition.ExternalOwner == "" || disposition.ReasonCode == "" || disposition.ReopenCondition == "" {
				add("external_action_required requires all of external_owner, reason_code, and reopen_condition (got %s). external_owner must be one of: %s. reason_code is a stable slug such as missing_platform_tool, permission_boundary, vendor_issue, policy, or accepted_risk, and reopen_condition names the evidence or capability that would make this actionable again",
					pulseArrivalReport(
						pulseStringArrival("external_owner", disposition.ExternalOwner),
						pulseStringArrival("reason_code", disposition.ReasonCode),
						pulseStringArrival("reopen_condition", disposition.ReopenCondition)),
					pulseAllowed(pulseExternalOwnerValues))
			} else if !pulseValueAllowed(disposition.ExternalOwner, pulseExternalOwnerValues) {
				add("external_action_required has invalid external_owner %q. Must be one of: %s. Use \"platform\" for shared runtime, harness, bridge, or product-side issues; \"user\" for a decision only the operator can make; \"vendor\" for a third-party service; \"workflow_owner\" for another workflow's own configuration",
					disposition.ExternalOwner, pulseAllowed(pulseExternalOwnerValues))
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return FormatPulseDispositionProblems(disposition.FindingID, problems)
}

// FormatPulseDispositionProblems joins every violation for one finding into a
// single numbered message, so one rejection can be fixed in one retry instead
// of one violation per round trip. Exported for pulse_worklist.go's reviewer
// cross-validation, which needs the identical combined-message shape.
func FormatPulseDispositionProblems(findingID string, problems []string) error {
	if len(problems) == 1 {
		return fmt.Errorf("finding %q %s", findingID, problems[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "finding %q has %d problems — fix all of them before retrying:", findingID, len(problems))
	for i, problem := range problems {
		fmt.Fprintf(&b, "\n%d) %s", i+1, problem)
	}
	return errors.New(b.String())
}

func lifecycleStatusForDisposition(disposition string) (status, eventType, resolvedBy string) {
	switch strings.TrimSpace(disposition) {
	case FindingDispositionFixedVerified, FindingDispositionVerifiedNoChange:
		return ConcernStatusResolved, "closed", "workflow_builder"
	case FindingDispositionChangedUnverified:
		return ConcernStatusResolved, "fix_applied", "workflow_builder"
	case FindingDispositionRejected:
		return ConcernStatusRejected, "rejected", "workflow_builder"
	case FindingDispositionFailed:
		return ConcernStatusOpen, "verification_failed", ""
	case FindingDispositionProposalOnly:
		return ConcernStatusAcknowledged, "proposal_recorded", ""
	case FindingDispositionAwaitingUser:
		return ConcernStatusAcknowledged, "awaiting_user", ""
	case FindingDispositionQueuedForEngineering:
		return ConcernStatusQueuedForEngineering, "queued_for_engineering", ""
	case FindingDispositionBlocked:
		return ConcernStatusAcknowledged, "blocked", ""
	case FindingDispositionAwaitingRun:
		return ConcernStatusOpen, "reopened_for_review", ""
	case FindingDispositionExternalAction:
		return ConcernStatusExternalActionRequired, "external_action_required", "pulse"
	default:
		return ConcernStatusOpen, "updated", ""
	}
}

// RecordPulseFindingDispositionsTx writes per-finding outcomes in the same
// transaction as the module audit. This prevents "module changed" from being
// committed while its issue/test lifecycle silently remains open.
func RecordPulseFindingDispositionsTx(
	ctx context.Context,
	db pulseFindingLifecycleDB,
	module, pulseRunID string,
	dispositions []PulseFindingDisposition,
	recordedAt string,
) error {
	module = pulsemodules.Normalize(module)
	if len(dispositions) == 0 {
		return nil
	}
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return err
	}
	if strings.TrimSpace(recordedAt) == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	type attemptAggregate struct {
		changedFiles []string
		beforeRefs   []string
		afterRefs    []string
		status       string
	}
	attempts := map[string]*attemptAggregate{}

	for _, disposition := range dispositions {
		disposition = NormalizePulseFindingDisposition(disposition)
		if err := validateFindingDisposition(disposition); err != nil {
			return err
		}
		if isPulseAdvisorModule(module) &&
			disposition.Disposition == FindingDispositionProposalOnly && disposition.NextCheck == "" {
			return fmt.Errorf("%s finding %q cannot use proposal_only without next_check: proposal_only is reserved for a recommendation waiting on a named future evidence boundary; create a pending human decision and use awaiting_user for an actionable strategy/goal change, or route a safe technical prerequisite to the Fixer",
				module, disposition.FindingID)
		}
		fingerprint := disposition.Fingerprint
		findingID := disposition.FindingID
		attemptID := disposition.AttemptID
		var concernExists int
		var concernStatus string
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(status), '') FROM run_concerns WHERE fingerprint=?`,
			fingerprint).Scan(&concernExists, &concernStatus); err != nil {
			return err
		}
		if concernExists != 1 {
			return fmt.Errorf("no concern with fingerprint %q for finding %q; fingerprint must be copied verbatim from a get_pulse_state(view=\"backlog\") item's fingerprint field, not from its issue.id", fingerprint, findingID)
		}
		// issue_kind is decided once at filing; status is decided here, later and
		// repeatedly, by a different tool. Nothing used to compare them, so a
		// finding could be filed as harness_issue ("no workflow-level repair can
		// fix this") and then parked as queued_for_engineering ("a workflow-level
		// Engineering Review pass will fix this"). That pair is self-contradictory
		// and self-perpetuating: every later pass re-reads it as actionable, spends
		// a reviewer slot rediscovering that it is not, and re-defers it.
		//
		// Only this one pairing is rejected. A harness finding that is resolved,
		// acknowledged, or awaiting_verification is legitimate — the behavior can
		// stop, be disproven, or be worked around from inside the workflow.
		if disposition.Disposition == FindingDispositionQueuedForEngineering {
			var issueKind string
			err := db.QueryRowContext(ctx,
				`SELECT COALESCE(issue_kind, '') FROM pulse_finding_details WHERE fingerprint=?`, fingerprint,
			).Scan(&issueKind)
			// Plain CONCERNS: findings carry no details row at all, so no issue_kind
			// was ever claimed and there is nothing to contradict.
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil && strings.TrimSpace(issueKind) == IssueKindHarness {
				return fmt.Errorf("finding %q was filed as %s and cannot be queued_for_engineering: no workflow-level Engineering Review pass can repair a boundary the workflow does not own, so queueing it here means it is rediscovered and re-deferred every pass. Either use external_action_required with external_owner=\"platform\", a reason_code, and a reopen_condition so it reaches docs/bugs/pulse_platform/index.md, or re-file it as %s if this workflow's own plan, config, code, or data does own the failure",
					findingID, IssueKindHarness, IssueKindWorkflow)
			}
		}
		if isPulseAdvisorModule(module) {
			var detailJSON string
			err := db.QueryRowContext(ctx, `SELECT detail_json FROM pulse_finding_details WHERE fingerprint=?`, fingerprint).Scan(&detailJSON)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil && strings.TrimSpace(detailJSON) != "" {
				var details PulseFindingDetails
				if err := json.Unmarshal([]byte(detailJSON), &details); err != nil {
					return fmt.Errorf("decode routing contract for finding %q: %w", findingID, err)
				}
				details = normalizePulseFindingDetails(details)
				switch details.RecommendedRoute {
				case pulseFindingRouteDecisionRequired:
					if disposition.Disposition == FindingDispositionAwaitingUser {
						break
					}
					if !pulseDispositionConsumesLinkedDecision(disposition.Disposition) {
						return fmt.Errorf("%s finding %q is routed decision_required and must use awaiting_user with a linked pending decision; got %s", module, findingID, disposition.Disposition)
					}
					var linkedDecisionStatus string
					err := db.QueryRowContext(ctx, `SELECT COALESCE(h.status, '')
						FROM pulse_finding_events e
						JOIN report_human_inputs h
						  ON h.id=json_extract(e.metadata_json, '$.human_input_id')
						WHERE e.fingerprint=? AND e.event_type='awaiting_user'
						ORDER BY e.recorded_at DESC, e._id DESC LIMIT 1`, fingerprint).Scan(&linkedDecisionStatus)
					if err == sql.ErrNoRows {
						return fmt.Errorf("%s finding %q is routed decision_required and cannot become %s without a prior linked awaiting_user decision", module, findingID, disposition.Disposition)
					}
					if err != nil {
						return err
					}
					if linkedDecisionStatus != "answered" {
						return fmt.Errorf("%s finding %q is routed decision_required and cannot become %s while its linked decision has status %q", module, findingID, disposition.Disposition, linkedDecisionStatus)
					}
				case pulseFindingRouteEvidenceWait:
					if disposition.Disposition != FindingDispositionProposalOnly {
						return fmt.Errorf("%s finding %q is routed evidence_wait and must use proposal_only; got %s", module, findingID, disposition.Disposition)
					}
					if disposition.NextCheck != details.NextCheck {
						return fmt.Errorf("%s finding %q must preserve the routed next_check exactly; got %q, want %q", module, findingID, disposition.NextCheck, details.NextCheck)
					}
				case pulseFindingRouteFixerHandoff:
					if disposition.Disposition == FindingDispositionProposalOnly || disposition.Disposition == FindingDispositionAwaitingUser {
						return fmt.Errorf("%s finding %q is routed fixer_handoff and cannot be parked as %s", module, findingID, disposition.Disposition)
					}
				case pulseFindingRouteNone:
					return fmt.Errorf("%s finding %q was routed none and must not have been filed as an active concern", module, findingID)
				}
			}
		}
		// Prove the decision exists and is still open. A claimed id is not
		// evidence: an already-answered or invented question would leave the
		// finding parked on a decision the operator can never make.
		if disposition.Disposition == FindingDispositionAwaitingUser {
			var inputStatus, inputSource string
			err := db.QueryRowContext(ctx,
				`SELECT status, COALESCE(source, '') FROM report_human_inputs WHERE id=?`, disposition.HumanInputID,
			).Scan(&inputStatus, &inputSource)
			if err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("awaiting_user finding %q references human input %q, which does not exist", findingID, disposition.HumanInputID)
				}
				return err
			}
			if inputStatus != "pending" {
				return fmt.Errorf("awaiting_user finding %q references human input %q with status %q; a finding can only wait on a pending decision", findingID, disposition.HumanInputID, inputStatus)
			}
			expectedSource, expectedPrefix := "", ""
			if isPulseAdvisorModule(module) {
				expectedSource, expectedPrefix = pulseReviewDecisionOwnership(module)
			}
			if expectedSource != "" {
				if strings.TrimSpace(inputSource) != expectedSource {
					return fmt.Errorf("awaiting_user %s finding %q references human input %q with source %q; create the decision with source=%q so the UI preserves who asked",
						module, findingID, disposition.HumanInputID, inputSource, expectedSource)
				}
				if !strings.HasPrefix(disposition.HumanInputID, expectedPrefix) {
					return fmt.Errorf("awaiting_user %s finding %q references human input %q; decision ids for this module must start with %q",
						module, findingID, disposition.HumanInputID, expectedPrefix)
				}
			}
		}
		if attemptID != "" {
			var attemptModule, attemptRun string
			if err := db.QueryRowContext(ctx, `SELECT module, pulse_run_id FROM pulse_fix_attempts WHERE attempt_id=?`, attemptID).Scan(&attemptModule, &attemptRun); err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("no fix attempt %q for finding %q; omit attempt_id and the backend opens or reuses the right attempt for this module and finding", attemptID, findingID)
				}
				return err
			}
			// The module must match: an attempt belongs to the module that made
			// it, and letting another module close it would let one module take
			// credit for work it did not do.
			//
			// The run deliberately need not match. changed_unverified exists
			// precisely so a fix whose proof needs a future run is recorded now
			// and verified later — fix-verification, post-run-monitor and the
			// Fixer contract all instruct exactly that, with reason
			// awaiting_next_valid_run. Requiring the attempt to belong to the
			// closing run made that impossible: the evidence arrives a run or
			// more after the attempt, and the disposition that would record it
			// was rejected as belonging to a previous Pulse run. social-media hit
			// this on 2026-08-01 and correctly preserved the unresolved state
			// rather than forcing a second lifecycle write, which would have
			// invented a fresh attempt for work already done.
			if pulsemodules.Normalize(attemptModule) != module {
				return fmt.Errorf("fix attempt %q belongs to module %q, not %q", attemptID, attemptModule, module)
			}
			var linked int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_fix_attempt_findings
				WHERE attempt_id=? AND fingerprint=?`, attemptID, fingerprint).Scan(&linked); err != nil {
				return err
			}
			if linked != 1 {
				return fmt.Errorf("fix attempt %q is not linked to concern %q (finding %q); an attempt can only settle the findings it was opened for, so omit attempt_id and let the backend open one for this finding", attemptID, fingerprint, findingID)
			}
		} else {
			resolved, resolveErr := resolvePulseFixAttemptTx(ctx, db, module, pulseRunID, concernStatus, disposition, recordedAt)
			if resolveErr != nil {
				return resolveErr
			}
			attemptID = resolved
		}

		verificationAttemptID := attemptID
		if verificationAttemptID == "" {
			// The historical uniqueness key starts with attempt_id. Give
			// non-attempt dispositions a run-scoped identity so the same check
			// accumulates across Pulse runs instead of overwriting one row.
			verificationAttemptID = "disposition:" + strings.TrimSpace(pulseRunID)
		}
		for _, verification := range disposition.Verification {
			if _, err := db.ExecContext(ctx, `INSERT INTO pulse_fix_verifications
				(attempt_id, fingerprint, check_text, verdict, expected, observed, evidence_json, verified_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(attempt_id, fingerprint, check_text) DO UPDATE SET
					verdict=excluded.verdict, expected=excluded.expected, observed=excluded.observed,
					evidence_json=excluded.evidence_json, verified_at=excluded.verified_at`,
				verificationAttemptID, fingerprint, verification.Check, verification.Verdict,
				verification.Expected, verification.Observed,
				lifecycleJSON(verification.Evidence), recordedAt); err != nil {
				return err
			}
		}

		status, eventType, resolvedBy := lifecycleStatusForDisposition(disposition.Disposition)
		resolvedAt := ""
		if status == ConcernStatusResolved || status == ConcernStatusRejected || status == ConcernStatusExternalActionRequired {
			resolvedAt = recordedAt
		}
		if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET
			status=?, resolved_at=?, resolved_by=?, resolution_note=?
			WHERE fingerprint=?`,
			status, resolvedAt, resolvedBy, strings.TrimSpace(disposition.Summary), fingerprint); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string]interface{}{
			"disposition":      disposition.Disposition,
			"changed_files":    normalizedLifecycleStrings(disposition.ChangedFiles),
			"before_refs":      normalizedLifecycleStrings(disposition.BeforeRefs),
			"after_refs":       normalizedLifecycleStrings(disposition.AfterRefs),
			"next_check":       strings.TrimSpace(disposition.NextCheck),
			"external_owner":   disposition.ExternalOwner,
			"reason_code":      disposition.ReasonCode,
			"reopen_condition": disposition.ReopenCondition,
			"human_input_id":   disposition.HumanInputID,
		})
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
			(fingerprint, finding_id, pulse_run_id, attempt_id, event_type, summary, metadata_json, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO UPDATE SET
				summary=excluded.summary, metadata_json=excluded.metadata_json, recorded_at=excluded.recorded_at`,
			fingerprint, findingID, pulseRunID, attemptID, eventType,
			strings.TrimSpace(disposition.Summary), string(metadata), recordedAt); err != nil {
			return err
		}
		if pulseDispositionConsumesLinkedDecision(disposition.Disposition) {
			if err := consumeLinkedPulseDecisionTx(ctx, db, fingerprint, disposition.Summary, recordedAt); err != nil {
				return err
			}
		}
		if attemptID != "" {
			if _, err := db.ExecContext(ctx, `UPDATE pulse_fix_attempt_findings SET
				finding_id=?, disposition=?, summary=?
				WHERE attempt_id=? AND fingerprint=?`,
				findingID, disposition.Disposition, strings.TrimSpace(disposition.Summary), attemptID, fingerprint); err != nil {
				return err
			}
			aggregate := attempts[attemptID]
			if aggregate == nil {
				aggregate = &attemptAggregate{status: "verified"}
				attempts[attemptID] = aggregate
			}
			aggregate.changedFiles = append(aggregate.changedFiles, disposition.ChangedFiles...)
			aggregate.beforeRefs = append(aggregate.beforeRefs, disposition.BeforeRefs...)
			aggregate.afterRefs = append(aggregate.afterRefs, disposition.AfterRefs...)
			if disposition.Disposition == FindingDispositionFailed {
				aggregate.status = "failed"
			} else if disposition.Disposition == FindingDispositionChangedUnverified && aggregate.status != "failed" {
				aggregate.status = "applied"
			} else if aggregate.status != "failed" && aggregate.status != ConcernStatusAwaitingVerification &&
				disposition.Disposition != FindingDispositionFixedVerified {
				aggregate.status = "applied"
			}
		}
	}
	for attemptID, aggregate := range attempts {
		if _, err := db.ExecContext(ctx, `UPDATE pulse_fix_attempts SET
			status=?, changed_files_json=?, before_refs_json=?, after_refs_json=?, completed_at=?
			WHERE attempt_id=?`,
			aggregate.status, lifecycleJSON(aggregate.changedFiles), lifecycleJSON(aggregate.beforeRefs),
			lifecycleJSON(aggregate.afterRefs), recordedAt, attemptID); err != nil {
			return err
		}
	}
	return nil
}

// pulseDispositionConsumesLinkedDecision identifies lifecycle outcomes that
// prove an earlier awaiting_user answer has actually been used. Merely reading
// an answer, failing a check, blocking, or waiting for evidence must not consume
// it. A changed_unverified repair has still used the decision even though its
// behavioral proof arrives on a later run.
func pulseDispositionConsumesLinkedDecision(disposition string) bool {
	switch strings.TrimSpace(disposition) {
	case FindingDispositionFixedVerified, FindingDispositionVerifiedNoChange,
		FindingDispositionChangedUnverified, FindingDispositionRejected:
		return true
	default:
		return false
	}
}

// consumeLinkedPulseDecisionTx closes the durable human-input loop when a
// finding with a previously linked awaiting_user decision reaches an outcome.
// The link comes from the immutable lifecycle event, not from prose or a guessed
// ID. Only an answered row transitions; pending, already-consumed, or unrelated
// questions are untouched.
func consumeLinkedPulseDecisionTx(ctx context.Context, db pulseFindingLifecycleDB, fingerprint, summary, recordedAt string) error {
	var humanInputID string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(json_extract(metadata_json, '$.human_input_id'), '')
		FROM pulse_finding_events
		WHERE fingerprint=? AND event_type='awaiting_user'
		ORDER BY recorded_at DESC, _id DESC LIMIT 1`, fingerprint).Scan(&humanInputID)
	if errors.Is(err, sql.ErrNoRows) || strings.TrimSpace(humanInputID) == "" {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE report_human_inputs SET
		status='consumed', consumed_by='pulse', outcome_summary=?, consumed_at=?, updated_at=?
		WHERE id=? AND status='answered'`,
		strings.TrimSpace(summary), recordedAt, recordedAt, strings.TrimSpace(humanInputID))
	return err
}

func decodeLifecycleStrings(raw string) []string {
	var values []string
	_ = json.Unmarshal([]byte(raw), &values)
	if values == nil {
		return []string{}
	}
	return values
}

// LoadPulseReviewVerificationCandidates returns the exact changed_unverified
// attempts a module is allowed to verify. Evidence arrival is still a reviewer
// judgment, but attempt eligibility is not: it is derived from the lifecycle
// tables and cannot be widened by caller-authored prose.
func LoadPulseReviewVerificationCandidates(
	ctx context.Context,
	workspacePath, module string,
) ([]PulseReviewVerificationCandidate, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulseReviewVerificationCandidate{}, err
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return nil, err
	}

	module = pulsemodules.Normalize(module)
	acceptedModules := []string{}
	for _, candidate := range pulsemodules.IDs() {
		if pulsemodules.Normalize(candidate) == module {
			acceptedModules = append(acceptedModules, candidate)
		}
	}
	if len(acceptedModules) == 0 {
		return []PulseReviewVerificationCandidate{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(acceptedModules)), ",")
	args := make([]interface{}, 0, len(acceptedModules))
	for _, accepted := range acceptedModules {
		args = append(args, accepted)
	}
	rows, err := db.QueryContext(ctx, `SELECT af.finding_id, af.fingerprint, a.attempt_id
		FROM pulse_fix_attempt_findings af
		JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
		JOIN run_concerns c ON c.fingerprint=af.fingerprint
		WHERE af.disposition=? AND a.status=? AND c.status=?
			AND a.module IN (`+placeholders+`)
		ORDER BY a.completed_at ASC, a.started_at ASC, af.finding_id ASC`,
		append([]interface{}{
			FindingDispositionChangedUnverified,
			ConcernStatusAwaitingVerification,
			ConcernStatusAwaitingVerification,
		}, args...)...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PulseReviewVerificationCandidate{}
	for rows.Next() {
		var candidate PulseReviewVerificationCandidate
		if err := rows.Scan(&candidate.FindingID, &candidate.Fingerprint, &candidate.AttemptID); err != nil {
			return nil, err
		}
		var metadataJSON string
		if err := db.QueryRowContext(ctx, `SELECT metadata_json FROM pulse_finding_events
			WHERE fingerprint=? AND attempt_id=? AND event_type='verification_inconclusive'
			ORDER BY recorded_at DESC LIMIT 1`, candidate.Fingerprint, candidate.AttemptID).Scan(&metadataJSON); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		metadata := map[string]interface{}{}
		_ = json.Unmarshal([]byte(metadataJSON), &metadata)
		candidate.NextCheck, _ = metadata["next_check"].(string)
		candidate.NextCheck = strings.TrimSpace(candidate.NextCheck)
		out = append(out, candidate)
	}
	return out, rows.Err()
}

// LoadPulseFindingLifecycles returns current issue state plus its complete fix
// and verification history. A module filter includes reviewer concerns filed by
// that module and any step concern linked to one of that module's fix attempts.
// A negative limit returns every matching finding; zero uses the bounded default
// for callers that explicitly want a preview.
func LoadPulseFindingLifecycles(ctx context.Context, workspacePath, module string, limit int) ([]PulseFindingLifecycle, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulseFindingLifecycle{}, err
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 100
	}
	module = pulsemodules.Normalize(module)
	// Lead with the step carrying the most unresolved work, and keep its rows
	// together.
	//
	// Recency put whatever was filed last on top, which is arbitrary when a
	// whole cluster is filed in one run. Fingerprints hash the finding text, so
	// one schema mismatch files once per field it names: social-media held 39
	// concerns from execute-find-opportunities, all opportunities.json failing
	// its validation_schema. Interleaved by timestamp they read as unrelated
	// one-offs, and successive Bug Review passes worked the top of the list and
	// left all 39 untouched.
	//
	// Clustering only helped where it was applied: an earlier reorder landed on
	// a view the Fixer never reads, so this backlog query is where it matters.
	query := fmt.Sprintf(`SELECT c.fingerprint, c.issue_id, c.step_id, c.phase, c.group_name, c.text,
			c.first_seen_run, c.first_seen_at, c.last_seen_run, c.last_seen_at, c.seen_count,
			c.status, c.resolution_note, COALESCE(d.detail_json, '')
		FROM run_concerns c
		LEFT JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint
		LEFT JOIN (
			SELECT step_id, COUNT(*) AS active_count, MAX(seen_count) AS peak_seen
			FROM run_concerns
			WHERE status NOT IN ('resolved', 'rejected', 'external_action_required')
			GROUP BY step_id
		) cluster ON cluster.step_id = c.step_id
		WHERE ?='' OR c.step_id=? OR EXISTS (
			SELECT 1 FROM pulse_fix_attempt_findings af
			JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
			WHERE af.fingerprint=c.fingerprint AND a.module=?
		)
		ORDER BY COALESCE(cluster.active_count, 0) DESC, COALESCE(cluster.peak_seen, 0) DESC,
			c.step_id ASC, c.last_seen_at DESC, c.seen_count DESC`)
	args := []interface{}{module, module, module}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PulseFindingLifecycle{}
	for rows.Next() {
		var finding PulseFindingLifecycle
		var detailJSON string
		if err := rows.Scan(&finding.Fingerprint, &finding.IssueID, &finding.StepID, &finding.Phase, &finding.GroupName,
			&finding.Text, &finding.FirstSeenRun, &finding.FirstSeenAt, &finding.LastSeenRun,
			&finding.LastSeenAt, &finding.SeenCount, &finding.Status, &finding.ResolutionNote,
			&detailJSON); err != nil {
			return nil, err
		}
		if strings.TrimSpace(detailJSON) != "" {
			var details PulseFindingDetails
			if err := json.Unmarshal([]byte(detailJSON), &details); err == nil {
				details = normalizePulseFindingDetails(details)
				if details.IssueKind == "harness_issue" {
					platform, platformErr := loadPulseHarnessPlatformIssue(ctx, details.TargetKey)
					if platformErr != nil {
						return nil, platformErr
					}
					details.Platform = platform
				}
				finding.Details = &details
				finding.FindingID = details.FindingID
			}
		}
		if finding.Phase == ConcernPhaseReview {
			finding.Module = pulsemodules.Normalize(finding.StepID)
		}
		finding.Attempts = []PulseFixAttempt{}
		finding.Verification = []PulseFindingVerification{}
		finding.Events = []PulseFindingEvent{}
		out = append(out, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for index := range out {
		fingerprint := out[index].Fingerprint
		attemptRows, err := db.QueryContext(ctx, `SELECT a.attempt_id, a.module, a.pulse_run_id, a.summary,
				a.status, a.intended_files_json, a.changed_files_json, a.before_refs_json,
				a.after_refs_json, a.started_at, a.completed_at, af.finding_id,
				af.disposition, af.summary
			FROM pulse_fix_attempt_findings af
			JOIN pulse_fix_attempts a ON a.attempt_id=af.attempt_id
			WHERE af.fingerprint=?
			ORDER BY a.started_at DESC`, fingerprint)
		if err != nil {
			return nil, err
		}
		for attemptRows.Next() {
			var attempt PulseFixAttempt
			var intendedJSON, changedJSON, beforeJSON, afterJSON, findingID, disposition, findingSummary string
			if err := attemptRows.Scan(&attempt.AttemptID, &attempt.Module, &attempt.PulseRunID,
				&attempt.Summary, &attempt.Status, &intendedJSON, &changedJSON, &beforeJSON,
				&afterJSON, &attempt.StartedAt, &attempt.CompletedAt, &findingID,
				&disposition, &findingSummary); err != nil {
				attemptRows.Close()
				return nil, err
			}
			attempt.IntendedFiles = decodeLifecycleStrings(intendedJSON)
			attempt.ChangedFiles = decodeLifecycleStrings(changedJSON)
			attempt.BeforeRefs = decodeLifecycleStrings(beforeJSON)
			attempt.AfterRefs = decodeLifecycleStrings(afterJSON)
			attempt.Findings = []PulseFixFindingRef{{
				Fingerprint: fingerprint,
				FindingID:   findingID,
				Disposition: disposition,
				Summary:     findingSummary,
			}}
			if out[index].FindingID == "" {
				out[index].FindingID = findingID
			}
			if out[index].Module == "" {
				out[index].Module = attempt.Module
			}
			out[index].Attempts = append(out[index].Attempts, attempt)
		}
		attemptRows.Close()

		verificationRows, err := db.QueryContext(ctx, `SELECT check_text, verdict, expected, observed,
				evidence_json, verified_at FROM pulse_fix_verifications
			WHERE fingerprint=? ORDER BY verified_at DESC`, fingerprint)
		if err != nil {
			return nil, err
		}
		for verificationRows.Next() {
			var verification PulseFindingVerification
			var evidenceJSON string
			if err := verificationRows.Scan(&verification.Check, &verification.Verdict,
				&verification.Expected, &verification.Observed, &evidenceJSON, &verification.At); err != nil {
				verificationRows.Close()
				return nil, err
			}
			verification.Evidence = decodeLifecycleStrings(evidenceJSON)
			out[index].Verification = append(out[index].Verification, verification)
		}
		verificationRows.Close()

		eventRows, err := db.QueryContext(ctx, `SELECT finding_id, event_type, summary, pulse_run_id, attempt_id,
				metadata_json, recorded_at FROM pulse_finding_events
			WHERE fingerprint=? ORDER BY recorded_at DESC`, fingerprint)
		if err != nil {
			return nil, err
		}
		for eventRows.Next() {
			var event PulseFindingEvent
			var metadataJSON string
			if err := eventRows.Scan(&event.FindingID, &event.EventType, &event.Summary, &event.PulseRunID,
				&event.AttemptID, &metadataJSON, &event.RecordedAt); err != nil {
				eventRows.Close()
				return nil, err
			}
			_ = json.Unmarshal([]byte(metadataJSON), &event.Metadata)
			if out[index].Status == ConcernStatusExternalActionRequired && event.EventType == "external_action_required" {
				if owner, ok := event.Metadata["external_owner"].(string); ok {
					out[index].ExternalOwner = owner
				}
				if reason, ok := event.Metadata["reason_code"].(string); ok {
					out[index].ReasonCode = reason
				}
				if condition, ok := event.Metadata["reopen_condition"].(string); ok {
					out[index].ReopenCondition = condition
				}
			}
			if out[index].FindingID == "" && event.FindingID != "" {
				out[index].FindingID = event.FindingID
			}
			out[index].Events = append(out[index].Events, event)
		}
		eventRows.Close()
		out[index].Kind = PulseFindingKindForLifecycle(out[index])
		out[index].Issue = NewPulseIssue(out[index])
		// Ordinary and migrated concerns predate reviewer-assigned IDs. Expose
		// the deterministic compact issue ID through the legacy lifecycle field
		// too, because fixer write tools use finding_id as the durable human
		// address while fingerprint remains the internal matching key.
		if strings.TrimSpace(out[index].FindingID) == "" {
			out[index].FindingID = out[index].Issue.ID
		}
	}
	return out, nil
}
