package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsestore"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	_ "modernc.org/sqlite"
)

var reportHumanInputStoreMu sync.Mutex

type ReportHumanInputOption struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ReportHumanInputApplyContract is the machine-readable handoff for an
// answered operator decision. Pre-run uses it to route work; it must never
// infer an implementation from the operator-facing context prose.
type ReportHumanInputApplyContract struct {
	Mode          string   `json:"mode,omitempty"`
	IssueID       string   `json:"issue_id,omitempty"`
	ApprovedScope string   `json:"approved_scope,omitempty"`
	PreRunChecks  []string `json:"pre_run_checks,omitempty"`
	PostRunProof  string   `json:"post_run_proof,omitempty"`
	FailurePolicy string   `json:"failure_policy,omitempty"`
}

type ReportHumanInput struct {
	ID                string                        `json:"id"`
	WorkspacePath     string                        `json:"workspace_path"`
	Source            string                        `json:"source"`
	Priority          string                        `json:"priority"`
	Question          string                        `json:"question"`
	Context           string                        `json:"context,omitempty"`
	Options           []ReportHumanInputOption      `json:"options"`
	AllowFreeText     bool                          `json:"allow_free_text"`
	Status            string                        `json:"status"`
	SelectedOptionID  string                        `json:"selected_option_id,omitempty"`
	Note              string                        `json:"note,omitempty"`
	RunID             string                        `json:"run_id,omitempty"`
	Evidence          string                        `json:"evidence,omitempty"`
	CreatedBy         string                        `json:"created_by,omitempty"`
	AnsweredBy        string                        `json:"answered_by,omitempty"`
	AnsweredByKind    string                        `json:"answered_by_kind,omitempty"`
	AnsweredVia       string                        `json:"answered_via,omitempty"`
	AnsweredSessionID string                        `json:"answered_session_id,omitempty"`
	ConsumedBy        string                        `json:"consumed_by,omitempty"`
	OutcomeSummary    string                        `json:"outcome_summary,omitempty"`
	CreatedAt         string                        `json:"created_at"`
	UpdatedAt         string                        `json:"updated_at"`
	AnsweredAt        string                        `json:"answered_at,omitempty"`
	ConsumedAt        string                        `json:"consumed_at,omitempty"`
	DismissedAt       string                        `json:"dismissed_at,omitempty"`
	ClaimToken        string                        `json:"claim_token,omitempty"`
	ClaimedAt         string                        `json:"claimed_at,omitempty"`
	ClaimExpiresAt    string                        `json:"claim_expires_at,omitempty"`
	ApplyContract     ReportHumanInputApplyContract `json:"apply_contract,omitempty"`
}

type ReportHumanInputCreateRequest struct {
	WorkspacePath string                        `json:"workspace_path"`
	InputID       string                        `json:"input_id"`
	Source        string                        `json:"source"`
	Priority      string                        `json:"priority"`
	Question      string                        `json:"question"`
	Context       string                        `json:"context"`
	Options       []ReportHumanInputOption      `json:"options"`
	AllowFreeText bool                          `json:"allow_free_text"`
	RunID         string                        `json:"run_id"`
	Evidence      string                        `json:"evidence"`
	CreatedBy     string                        `json:"created_by"`
	CreatedByKind string                        `json:"-"`
	CreatedVia    string                        `json:"-"`
	SessionID     string                        `json:"-"`
	ApplyContract ReportHumanInputApplyContract `json:"apply_contract"`
}

type ReportHumanInputAnswerRequest struct {
	WorkspacePath    string `json:"workspace_path"`
	SelectedOptionID string `json:"selected_option_id"`
	Note             string `json:"note"`
	AnsweredBy       string `json:"answered_by"`
	AnsweredByKind   string `json:"-"`
	AnsweredVia      string `json:"-"`
	SessionID        string `json:"-"`
}

type ReportHumanInputConsumeRequest struct {
	WorkspacePath  string `json:"workspace_path"`
	OutcomeSummary string `json:"outcome_summary"`
	ConsumedBy     string `json:"consumed_by"`
	ConsumedByKind string `json:"-"`
	ConsumedVia    string `json:"-"`
	SessionID      string `json:"-"`
}

type reportHumanInputEvent struct {
	InputID   string
	EventType string
	Status    string
	ActorID   string
	ActorKind string
	Channel   string
	SessionID string
	Details   string
	CreatedAt string
}

func normalizeReportHumanInputWorkspacePath(workspacePath string) (string, error) {
	cleaned := filepath.ToSlash(filepath.Clean(strings.Trim(strings.TrimSpace(workspacePath), "/")))
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("workspace_path is required")
	}
	if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "\x00") {
		return "", fmt.Errorf("workspace_path must be workspace-relative")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", fmt.Errorf("workspace_path cannot contain ..")
		}
	}
	return cleaned, nil
}

func reportHumanInputDBPath(workspacePath string) (string, string, error) {
	normalized, err := normalizeReportHumanInputWorkspacePath(workspacePath)
	if err != nil {
		return "", "", err
	}
	root, err := filepath.Abs(getWorkspaceDocsAbsPath())
	if err != nil {
		return "", "", err
	}
	dbPath, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(normalized), "db", "db.sqlite"))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, dbPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("workspace_path escapes workspace docs root")
	}
	return normalized, dbPath, nil
}

func openReportHumanInputDB(ctx context.Context, workspacePath string, create bool) (string, *sql.DB, error) {
	normalized, dbPath, err := reportHumanInputDBPath(workspacePath)
	if err != nil {
		return "", nil, err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return "", nil, err
		}
	} else if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return normalized, nil, nil
		}
		return "", nil, err
	}
	// WAL mode is required, not optional: in the default rollback-journal mode
	// a writer's transaction takes an exclusive lock on the whole file that
	// blocks every concurrent reader, not just other writers. This function is
	// opened fresh per call from many call sites in pulse_worklist.go, and
	// get_pulse_state(view=module) alone opens/closes it 5 times sequentially
	// for one logical call -- each one a fresh collision window against any
	// concurrent step writing to this same db.sqlite. Embedding the pragmas in
	// the DSN (rather than a runtime PRAGMA ExecContext call, as before)
	// matters too: this *sql.DB has no SetMaxOpenConns cap, so a runtime
	// PRAGMA on one pooled connection would silently not apply if the pool
	// opened a second connection for a concurrent query. sqliteopen.DSN is
	// the same helper query_workflow_db's own workspace-service backend
	// uses, so both surfaces stay in sync on this instead of each
	// hand-rolling their own DSN string.
	db, err := sql.Open("sqlite", sqliteopen.DSN(dbPath))
	if err != nil {
		return "", nil, err
	}
	if err := ensureReportHumanInputSchema(ctx, db); err != nil {
		_ = db.Close()
		return "", nil, err
	}
	if _, err := pulsestore.Ensure(ctx, db, dbPath); err != nil {
		_ = db.Close()
		return "", nil, fmt.Errorf("migrate compact Pulse store: %w", err)
	}
	if create {
		if err := pulsestore.RefreshCompatibility(ctx, db); err != nil {
			_ = db.Close()
			return "", nil, fmt.Errorf("install compact Pulse compatibility: %w", err)
		}
	}
	return normalized, db, nil
}

func ensureReportHumanInputSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS report_human_inputs (
			id TEXT PRIMARY KEY,
			workspace_path TEXT NOT NULL,
			source TEXT NOT NULL,
			priority TEXT NOT NULL DEFAULT 'medium',
			question TEXT NOT NULL,
			context TEXT NOT NULL DEFAULT '',
			options_json TEXT NOT NULL DEFAULT '[]',
			allow_free_text INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			selected_option_id TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			run_id TEXT NOT NULL DEFAULT '',
			evidence TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			answered_by TEXT NOT NULL DEFAULT '',
			answered_by_kind TEXT NOT NULL DEFAULT '',
			answered_via TEXT NOT NULL DEFAULT '',
			answered_session_id TEXT NOT NULL DEFAULT '',
			consumed_by TEXT NOT NULL DEFAULT '',
			outcome_summary TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			answered_at TEXT NOT NULL DEFAULT '',
			consumed_at TEXT NOT NULL DEFAULT '',
			dismissed_at TEXT NOT NULL DEFAULT '',
			claim_token TEXT NOT NULL DEFAULT '',
			claimed_at TEXT NOT NULL DEFAULT '',
			claim_expires_at TEXT NOT NULL DEFAULT '',
			apply_contract_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_report_human_inputs_status ON report_human_inputs(status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_report_human_inputs_source ON report_human_inputs(source, status, updated_at)`,
		`CREATE TABLE IF NOT EXISTS report_human_input_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_path TEXT NOT NULL,
			input_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			status TEXT NOT NULL,
			actor_id TEXT NOT NULL DEFAULT '',
			actor_kind TEXT NOT NULL DEFAULT '',
			channel TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_report_human_input_events_lookup
			ON report_human_input_events(workspace_path, input_id, id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	for name, definition := range map[string]string{
		"claim_token":         "TEXT NOT NULL DEFAULT ''",
		"claimed_at":          "TEXT NOT NULL DEFAULT ''",
		"claim_expires_at":    "TEXT NOT NULL DEFAULT ''",
		"answered_by_kind":    "TEXT NOT NULL DEFAULT ''",
		"answered_via":        "TEXT NOT NULL DEFAULT ''",
		"answered_session_id": "TEXT NOT NULL DEFAULT ''",
		"apply_contract_json": "TEXT NOT NULL DEFAULT '{}'",
	} {
		if err := ensureReportHumanInputColumn(ctx, db, name, definition); err != nil {
			return err
		}
	}
	// Historical rows predate trusted channel attribution. Preserve their user
	// label but classify the provenance honestly instead of leaving an empty
	// value that callers may mistake for a current verified UI answer.
	// Each migration below is probed with a SELECT first: this function runs on
	// every open, including the pure reads behind get_pulse_state, and a no-op
	// UPDATE still takes the write lock (see sqliteRowsExist).
	if err := sqliteExecIfRows(ctx, db,
		`SELECT 1 FROM report_human_inputs WHERE answered_by<>'' AND answered_by_kind=''`, nil,
		`UPDATE report_human_inputs
		SET answered_by_kind='legacy_unattributed', answered_via='legacy'
		WHERE answered_by<>'' AND answered_by_kind=''`); err != nil {
		return err
	}
	// Preserve pending/answered decisions while moving their attribution to the
	// single live Strategic Review module. IDs remain stable historical keys;
	// only new decisions use the strategic-proposal- namespace.
	if err := sqliteExecIfRows(ctx, db,
		`SELECT 1 FROM report_human_inputs WHERE source IN ('strategy_auditor', 'goal_advisor')`, nil,
		`UPDATE report_human_inputs SET source='strategic_review'
		WHERE source IN ('strategy_auditor', 'goal_advisor')`); err != nil {
		return err
	}
	// Prompt-contract decisions existed before structured apply contracts. Their
	// stable reviewer-owned namespace is a safe one-time migration boundary;
	// arbitrary legacy prose remains manual rather than being guessed at.
	promptContractMigration, _ := json.Marshal(ReportHumanInputApplyContract{
		Mode:          "targeted_fixer",
		ApprovedScope: "Extract one versioned shared prompt contract at a time; preserve exact step inputs, outputs, validation, routes, and side-effect ordering.",
		PreRunChecks:  []string{"validate_plan_change"},
		PostRunProof:  "one post-change producing run",
		FailurePolicy: "continue_unchanged",
	})
	if err := sqliteExecIfRows(ctx, db,
		`SELECT 1 FROM report_human_inputs
		WHERE source='technical_review'
		  AND id LIKE 'technical-decision-prompt-contract-consolidation-%'
		  AND (apply_contract_json='' OR apply_contract_json='{}')`, nil,
		`UPDATE report_human_inputs
		SET apply_contract_json=?
		WHERE source='technical_review'
		  AND id LIKE 'technical-decision-prompt-contract-consolidation-%'
		  AND (apply_contract_json='' OR apply_contract_json='{}')`, string(promptContractMigration)); err != nil {
		return err
	}
	return nil
}

type reportHumanInputEventExecer interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func writeReportHumanInputEvent(ctx context.Context, execer reportHumanInputEventExecer, workspacePath string, event reportHumanInputEvent) error {
	event.ActorKind = normalizeReportHumanInputActorKind(event.ActorKind)
	event.Channel = strings.TrimSpace(event.Channel)
	if event.Channel == "" {
		event.Channel = "unknown"
	}
	if event.CreatedAt == "" {
		event.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := execer.ExecContext(ctx, `INSERT INTO report_human_input_events
		(workspace_path, input_id, event_type, status, actor_id, actor_kind, channel, session_id, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, workspacePath, strings.TrimSpace(event.InputID),
		strings.TrimSpace(event.EventType), strings.TrimSpace(event.Status), strings.TrimSpace(event.ActorID),
		event.ActorKind, event.Channel, strings.TrimSpace(event.SessionID), strings.TrimSpace(event.Details), event.CreatedAt)
	return err
}

func normalizeReportHumanInputActorKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "human_ui", "human_via_chat", "agent", "system", "migration":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return "legacy_unattributed"
	}
}

func normalizeReportHumanInputApplyContract(contract ReportHumanInputApplyContract) (ReportHumanInputApplyContract, error) {
	contract.Mode = strings.ToLower(strings.TrimSpace(contract.Mode))
	contract.IssueID = strings.TrimSpace(contract.IssueID)
	contract.ApprovedScope = strings.TrimSpace(contract.ApprovedScope)
	contract.PostRunProof = strings.TrimSpace(contract.PostRunProof)
	contract.FailurePolicy = strings.ToLower(strings.TrimSpace(contract.FailurePolicy))
	if contract.Mode == "" {
		return ReportHumanInputApplyContract{}, nil // legacy/prose-only: never auto-apply.
	}
	switch contract.Mode {
	case "no_change", "direct_apply", "targeted_fixer", "external_wait":
	default:
		return ReportHumanInputApplyContract{}, fmt.Errorf("apply_contract.mode must be no_change, direct_apply, targeted_fixer, or external_wait")
	}
	if contract.Mode == "targeted_fixer" && contract.ApprovedScope == "" {
		return ReportHumanInputApplyContract{}, fmt.Errorf("targeted_fixer apply_contract requires approved_scope")
	}
	if contract.FailurePolicy == "" {
		contract.FailurePolicy = "continue_unchanged"
	}
	if contract.FailurePolicy != "continue_unchanged" && contract.FailurePolicy != "block_run" {
		return ReportHumanInputApplyContract{}, fmt.Errorf("apply_contract.failure_policy must be continue_unchanged or block_run")
	}
	checks := make([]string, 0, len(contract.PreRunChecks))
	seen := map[string]bool{}
	for _, check := range contract.PreRunChecks {
		check = strings.TrimSpace(check)
		if check != "" && !seen[check] {
			seen[check] = true
			checks = append(checks, check)
		}
	}
	contract.PreRunChecks = checks
	return contract, nil
}

// ReportHumanInputFixerCandidate is an explicitly approved repair that must
// outrank normal generic Fixer queue selection. The linked issue is resolved
// from the durable awaiting_user event because a reviewer has to create the
// decision before it can know the finding's public PUL id.
type ReportHumanInputFixerCandidate struct {
	InputID       string                        `json:"input_id"`
	IssueID       string                        `json:"issue_id,omitempty"`
	WorkspacePath string                        `json:"workspace_path"`
	ApplyContract ReportHumanInputApplyContract `json:"apply_contract"`
}

func reportHumanInputFixerCandidates(inputs []ReportHumanInput, findings []step_based_workflow.PulseFindingLifecycle) []ReportHumanInputFixerCandidate {
	linkedIssues := make(map[string]string)
	for _, finding := range findings {
		issueID := step_based_workflow.NewPulseIssue(finding).ID
		for _, event := range finding.Events {
			if event.EventType != "awaiting_user" {
				continue
			}
			inputID, _ := event.Metadata["human_input_id"].(string)
			if inputID != "" && linkedIssues[inputID] == "" {
				linkedIssues[inputID] = issueID
			}
		}
	}
	candidates := make([]ReportHumanInputFixerCandidate, 0)
	for _, input := range inputs {
		contract := input.ApplyContract
		if !strings.EqualFold(strings.TrimSpace(contract.Mode), "targeted_fixer") || !strings.EqualFold(strings.TrimSpace(input.SelectedOptionID), "approve") {
			continue
		}
		issueID := linkedIssues[input.ID]
		if issueID == "" {
			issueID = strings.ToUpper(strings.TrimSpace(contract.IssueID))
		}
		candidates = append(candidates, ReportHumanInputFixerCandidate{
			InputID: input.ID, IssueID: issueID, WorkspacePath: input.WorkspacePath, ApplyContract: contract,
		})
	}
	return candidates
}

func listApprovedTargetedFixerCandidates(ctx context.Context, workspacePath string) ([]ReportHumanInputFixerCandidate, error) {
	inputs, err := listReportHumanInputs(ctx, workspacePath, "answered", "")
	if err != nil || len(inputs) == 0 {
		return []ReportHumanInputFixerCandidate{}, err
	}
	findings, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, inputs[0].WorkspacePath, "", -1)
	if err != nil {
		return nil, err
	}
	return reportHumanInputFixerCandidates(inputs, findings), nil
}

func ensureReportHumanInputColumn(ctx context.Context, db *sql.DB, column, definition string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(report_human_inputs)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if strings.EqualFold(name, column) {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE report_human_inputs ADD COLUMN %s %s", column, definition))
	return err
}

func createReportHumanInput(ctx context.Context, workspacePath string, req ReportHumanInputCreateRequest) (*ReportHumanInput, error) {
	defer publishHumanInputsChanged(workspacePath) // live feed: header count + decisions pane
	if normalizeReportHumanInputSource(req.Source) == "user_suggestion" && req.CreatedVia != "suggestion_tool" {
		return nil, fmt.Errorf("use submit_workflow_suggestion to leave a user suggestion")
	}
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()

	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	question := strings.TrimSpace(req.Question)
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}
	source := normalizeReportHumanInputSource(req.Source)
	priority := normalizeReportHumanInputPriority(req.Priority)
	options, err := normalizeReportHumanInputOptions(req.Options)
	if err != nil {
		return nil, err
	}
	allowFreeText := req.AllowFreeText || len(options) == 0
	id := strings.TrimSpace(req.InputID)
	if id == "" {
		id = newReportHumanInputID(question)
	}
	id = normalizeReportHumanInputID(id)
	if id == "" {
		return nil, fmt.Errorf("input_id is invalid")
	}

	existing, err := getReportHumanInputByID(ctx, db, normalized, id)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Source == "user_suggestion" {
		return nil, fmt.Errorf("user suggestions cannot be overwritten; submit a new suggestion")
	}
	if existing != nil && existing.Status != "pending" {
		return nil, fmt.Errorf("input_id %q already exists with status %q", id, existing.Status)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	optionsJSON, _ := json.Marshal(options)
	applyContract, err := normalizeReportHumanInputApplyContract(req.ApplyContract)
	if err != nil {
		return nil, err
	}
	input := &ReportHumanInput{
		ID:            id,
		WorkspacePath: normalized,
		Source:        source,
		Priority:      priority,
		Question:      question,
		Context:       strings.TrimSpace(req.Context),
		AllowFreeText: allowFreeText,
		RunID:         strings.TrimSpace(req.RunID),
		Evidence:      strings.TrimSpace(req.Evidence),
		CreatedBy:     strings.TrimSpace(req.CreatedBy),
		CreatedAt:     now,
		UpdatedAt:     now,
		ApplyContract: applyContract,
	}
	applyContractJSON, _ := json.Marshal(input.ApplyContract)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	eventType := "created"
	if existing == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO report_human_inputs
			(id, workspace_path, source, priority, question, context, options_json, allow_free_text, status, run_id, evidence, created_by, apply_contract_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?)`,
			input.ID, input.WorkspacePath, input.Source, input.Priority, input.Question, input.Context, string(optionsJSON), boolToInt(input.AllowFreeText),
			input.RunID, input.Evidence, input.CreatedBy, string(applyContractJSON), input.CreatedAt, input.UpdatedAt)
	} else {
		eventType = "refreshed"
		_, err = tx.ExecContext(ctx, `UPDATE report_human_inputs
			SET source=?, priority=?, question=?, context=?, options_json=?, allow_free_text=?, run_id=?, evidence=?, created_by=?, apply_contract_json=?, updated_at=?
			WHERE id=? AND workspace_path=? AND status='pending'`,
			input.Source, input.Priority, input.Question, input.Context, string(optionsJSON), boolToInt(input.AllowFreeText),
			input.RunID, input.Evidence, input.CreatedBy, string(applyContractJSON), input.UpdatedAt, input.ID, input.WorkspacePath)
		input.CreatedAt = existing.CreatedAt
	}
	if err != nil {
		return nil, err
	}
	if err := writeReportHumanInputEvent(ctx, tx, normalized, reportHumanInputEvent{
		InputID: input.ID, EventType: eventType, Status: "pending", ActorID: input.CreatedBy,
		ActorKind: req.CreatedByKind, Channel: req.CreatedVia, SessionID: req.SessionID, CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getReportHumanInputByID(ctx, db, normalized, id)
}

func listReportHumanInputs(ctx context.Context, workspacePath, status, source string) ([]ReportHumanInput, error) {
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()

	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return []ReportHumanInput{}, nil
	}
	defer db.Close()

	clauses := []string{"workspace_path = ?"}
	args := []interface{}{normalized}
	if s := strings.TrimSpace(status); s != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, s)
	}
	if s := strings.TrimSpace(source); s != "" {
		clauses = append(clauses, "source = ?")
		args = append(args, normalizeReportHumanInputSource(s))
	}
	query := `SELECT id, workspace_path, source, priority, question, context, options_json, allow_free_text, status,
		selected_option_id, note, run_id, evidence, created_by, answered_by, answered_by_kind, answered_via, answered_session_id, consumed_by, outcome_summary,
		created_at, updated_at, answered_at, consumed_at, dismissed_at, claim_token, claimed_at, claim_expires_at, apply_contract_json
		FROM report_human_inputs WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY CASE status WHEN 'pending' THEN 0 WHEN 'answered' THEN 1 WHEN 'claimed' THEN 2 WHEN 'dismissed' THEN 3 ELSE 4 END,
			datetime(updated_at) DESC, id DESC`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		if isReportHumanInputsMissingTable(err) {
			return []ReportHumanInput{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	var inputs []ReportHumanInput
	for rows.Next() {
		input, err := scanReportHumanInput(rows)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, *input)
	}
	return inputs, rows.Err()
}

func answerReportHumanInput(ctx context.Context, workspacePath, inputID string, req ReportHumanInputAnswerRequest) (*ReportHumanInput, error) {
	defer publishHumanInputsChanged(workspacePath) // live feed: header count + decisions pane
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()

	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("input_id %q not found", inputID)
	}
	defer db.Close()

	input, err := getReportHumanInputByID(ctx, db, normalized, inputID)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("input_id %q not found", inputID)
	}
	if err := requireSuggestionOwner(ctx, normalized, input); err != nil {
		return nil, err
	}
	if input.Status == "consumed" || input.Status == "dismissed" || input.Status == "claimed" {
		return nil, fmt.Errorf("input_id %q is %s", inputID, input.Status)
	}

	selected := strings.TrimSpace(req.SelectedOptionID)
	note := strings.TrimSpace(req.Note)
	if selected != "" && !reportHumanInputOptionExists(input.Options, selected) {
		return nil, fmt.Errorf("selected_option_id %q is not valid for input_id %q", selected, inputID)
	}
	if !input.AllowFreeText && len(input.Options) > 0 {
		note = ""
	}
	if selected == "" && note == "" {
		if len(input.Options) > 0 {
			if input.AllowFreeText {
				return nil, fmt.Errorf("select an option or provide a note")
			}
			return nil, fmt.Errorf("selected_option_id is required")
		}
		return nil, fmt.Errorf("note is required for free-text input")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// The WHERE clause re-checks status inside the same statement that writes
	// it (PLAT-073 cluster I, cf457bdd/7602e2ac): the in-process mutex above
	// only serializes goroutines in this server — a concurrent writer in
	// another process (the documented chat/schedule concurrency contract) can
	// still consume or dismiss this row between the read above and this
	// write. Without this guard that write silently reverts status back to
	// 'answered' while leaving the prior consumed_at/outcome_summary in
	// place, producing a row that is simultaneously "answered" and
	// "consumed" — exactly the state loop_closure observed live.
	result, err := tx.ExecContext(ctx, `UPDATE report_human_inputs
		SET status='answered', selected_option_id=?, note=?, answered_by=?, answered_by_kind=?, answered_via=?, answered_session_id=?, answered_at=?, updated_at=?
		WHERE id=? AND workspace_path=? AND status NOT IN ('consumed', 'dismissed', 'claimed')`,
		selected, note, strings.TrimSpace(req.AnsweredBy), normalizeReportHumanInputActorKind(req.AnsweredByKind),
		strings.TrimSpace(req.AnsweredVia), strings.TrimSpace(req.SessionID), now, now, input.ID, normalized)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if affected == 0 {
		return nil, fmt.Errorf("input_id %q was consumed, dismissed, or claimed by another writer before this answer could be saved", inputID)
	}
	if err := step_based_workflow.SyncPulseImprovementDecisionTx(ctx, tx, input.ID, selected, false, "", now); err != nil {
		return nil, err
	}
	// The current row already owns the answer. The append-only audit trail keeps
	// provenance, not a second permanent copy of potentially sensitive free text.
	details, _ := json.Marshal(map[string]interface{}{
		"selected_option_id": selected,
		"note_present":       note != "",
	})
	if err := writeReportHumanInputEvent(ctx, tx, normalized, reportHumanInputEvent{
		InputID: input.ID, EventType: "answered", Status: "answered", ActorID: req.AnsweredBy,
		ActorKind: req.AnsweredByKind, Channel: req.AnsweredVia, SessionID: req.SessionID,
		Details: string(details), CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getReportHumanInputByID(ctx, db, normalized, input.ID)
}

func dismissReportHumanInput(ctx context.Context, workspacePath, inputID string, req ReportHumanInputAnswerRequest) (*ReportHumanInput, error) {
	defer publishHumanInputsChanged(workspacePath) // live feed: header count + decisions pane
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()

	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("input_id %q not found", inputID)
	}
	defer db.Close()

	input, err := getReportHumanInputByID(ctx, db, normalized, inputID)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("input_id %q not found", inputID)
	}
	if err := requireSuggestionOwner(ctx, normalized, input); err != nil {
		return nil, err
	}
	if input.Status == "consumed" || input.Status == "claimed" {
		return nil, fmt.Errorf("input_id %q is %s and cannot be dismissed", inputID, input.Status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Same concurrent-writer guard as answerReportHumanInput (PLAT-073 cluster I).
	result, err := tx.ExecContext(ctx, `UPDATE report_human_inputs
		SET status='dismissed', answered_by=?, answered_by_kind=?, answered_via=?, answered_session_id=?, dismissed_at=?, updated_at=?
		WHERE id=? AND workspace_path=? AND status NOT IN ('consumed', 'claimed')`,
		strings.TrimSpace(req.AnsweredBy), normalizeReportHumanInputActorKind(req.AnsweredByKind),
		strings.TrimSpace(req.AnsweredVia), strings.TrimSpace(req.SessionID), now, now, input.ID, normalized)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if affected == 0 {
		return nil, fmt.Errorf("input_id %q was consumed or claimed by another writer before it could be dismissed", inputID)
	}
	if err := writeReportHumanInputEvent(ctx, tx, normalized, reportHumanInputEvent{
		InputID: input.ID, EventType: "dismissed", Status: "dismissed", ActorID: req.AnsweredBy,
		ActorKind: req.AnsweredByKind, Channel: req.AnsweredVia, SessionID: req.SessionID, CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getReportHumanInputByID(ctx, db, normalized, input.ID)
}

func consumeReportHumanInput(ctx context.Context, workspacePath, inputID string, req ReportHumanInputConsumeRequest) (*ReportHumanInput, error) {
	defer publishHumanInputsChanged(workspacePath) // live feed: header count + decisions pane
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()

	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("input_id %q not found", inputID)
	}
	defer db.Close()

	input, err := getReportHumanInputByID(ctx, db, normalized, inputID)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("input_id %q not found", inputID)
	}
	if err := requireSuggestionOwner(ctx, normalized, input); err != nil {
		return nil, err
	}
	if input.Status != "answered" && input.Status != "claimed" {
		return nil, fmt.Errorf("input_id %q must be answered or claimed before it can be consumed; current status=%q", inputID, input.Status)
	}
	outcome := strings.TrimSpace(req.OutcomeSummary)
	if outcome == "" {
		return nil, fmt.Errorf("outcome_summary is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE report_human_inputs
		SET status='consumed', consumed_by=?, outcome_summary=?, consumed_at=?, updated_at=?,
		    claim_token='', claimed_at='', claim_expires_at=''
		WHERE id=? AND workspace_path=? AND status IN ('answered', 'claimed')`,
		strings.TrimSpace(req.ConsumedBy), outcome, now, now, input.ID, normalized)
	if err != nil {
		return nil, err
	}
	// The WHERE guard above already existed but its zero-row case was never
	// checked, so a concurrent writer racing this one could make the guard
	// silently no-op while this call still reported success and wrote a
	// "consumed" audit event for a row it never actually changed (PLAT-073
	// cluster I).
	if affected, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if affected == 0 {
		return nil, fmt.Errorf("input_id %q was not in 'answered' or 'claimed' state when this consumption was applied (concurrent writer): current status=%q", inputID, input.Status)
	}
	if err := step_based_workflow.SyncPulseImprovementDecisionTx(ctx, tx, input.ID, input.SelectedOptionID, true, outcome, now); err != nil {
		return nil, err
	}
	if err := writeReportHumanInputEvent(ctx, tx, normalized, reportHumanInputEvent{
		InputID: input.ID, EventType: "consumed", Status: "consumed", ActorID: req.ConsumedBy,
		ActorKind: req.ConsumedByKind, Channel: req.ConsumedVia, SessionID: req.SessionID,
		Details: outcome, CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getReportHumanInputByID(ctx, db, normalized, input.ID)
}

func getReportHumanInputByID(ctx context.Context, db *sql.DB, workspacePath, inputID string) (*ReportHumanInput, error) {
	row := db.QueryRowContext(ctx, `SELECT id, workspace_path, source, priority, question, context, options_json, allow_free_text, status,
		selected_option_id, note, run_id, evidence, created_by, answered_by, answered_by_kind, answered_via, answered_session_id, consumed_by, outcome_summary,
		created_at, updated_at, answered_at, consumed_at, dismissed_at, claim_token, claimed_at, claim_expires_at, apply_contract_json
		FROM report_human_inputs WHERE workspace_path=? AND id=?`, workspacePath, strings.TrimSpace(inputID))
	input, err := scanReportHumanInput(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if isReportHumanInputsMissingTable(err) {
		return nil, nil
	}
	return input, err
}

type reportHumanInputScanner interface {
	Scan(dest ...interface{}) error
}

func scanReportHumanInput(row reportHumanInputScanner) (*ReportHumanInput, error) {
	var input ReportHumanInput
	var optionsJSON string
	var applyContractJSON string
	var allowFreeText int
	if err := row.Scan(
		&input.ID, &input.WorkspacePath, &input.Source, &input.Priority, &input.Question, &input.Context,
		&optionsJSON, &allowFreeText, &input.Status, &input.SelectedOptionID, &input.Note, &input.RunID,
		&input.Evidence, &input.CreatedBy, &input.AnsweredBy, &input.AnsweredByKind, &input.AnsweredVia, &input.AnsweredSessionID, &input.ConsumedBy, &input.OutcomeSummary,
		&input.CreatedAt, &input.UpdatedAt, &input.AnsweredAt, &input.ConsumedAt, &input.DismissedAt,
		&input.ClaimToken, &input.ClaimedAt, &input.ClaimExpiresAt, &applyContractJSON,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(optionsJSON), &input.Options)
	if input.Options == nil {
		input.Options = []ReportHumanInputOption{}
	}
	input.AllowFreeText = allowFreeText != 0
	_ = json.Unmarshal([]byte(applyContractJSON), &input.ApplyContract)
	return &input, nil
}

func createReportHumanInputTools() ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	createTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "create_human_input_request",
			Description: "Create or refresh a structured non-blocking workflow question for the user. Review decisions are stored in that workflow's db/db.sqlite and answered inside the Pulse/report panel. Attribute new review requests to source=\"technical_review\" or \"strategic_review\"; reserve source=\"pulse\" for generic Pulse coordination. For any decision that authorizes a workflow change, supply apply_contract: pre-run routes it deterministically and never infers a repair from user-facing prose. Use targeted_fixer for prompt, plan, route, validation, database, tool, model, or cross-artifact changes; direct_apply only for a known single setting and exact static check. This tool is called through the custom HTTP route, so the JSON body must match the published schema exactly; apply_contract is an object, but approved_scope and post_run_proof inside it are plain strings.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string", "description": "Workflow-relative path, for example Workflow/social-media. Required; requests are stored in that workflow's db/db.sqlite."},
					"input_id":       map[string]interface{}{"type": "string", "description": "Optional stable id. Reuse this for the same still-open question so Pulse refreshes it instead of duplicating it."},
					"source":         map[string]interface{}{"type": "string", "enum": []string{"pulse", "technical_review", "architecture_review", "strategic_review"}, "description": "Who is asking. Use the canonical reviewer identity; defaults to pulse only for generic Pulse coordination."},
					"priority":       map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high"}, "description": "How important the answer is. Defaults to medium."},
					"question":       map[string]interface{}{"type": "string", "description": "The exact user-facing question in ONE short plain sentence -- the kind a busy operator reads in three seconds, not an analyst's framing of the problem."},
					"context":        map[string]interface{}{"type": "string", "description": "Short explanation of why this matters and what will happen next, for a non-technical operator, not a technical report. One to three short sentences PER SECTION, plain language, no jargon, no walked-through derivation -- state the single number or fact that matters and the conclusion, not how you got there; the full analysis belongs in the reviewer's findings file, not this question. For plan-change proposals, use newline-separated labeled sections exactly like: Proposal:\n...\nExact intended edits if approved:\n(1) ...\n(2) ...\nRationale:\n...\nExpected impact:\n...\nRisk:\n... -- each section still capped at one to three short sentences. Keep evidence paths in the separate evidence field, never inline citations or file paths in context."},
					"options": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id":          map[string]interface{}{"type": "string", "description": "Stable machine id, e.g. approve, wait, use_email."},
								"title":       map[string]interface{}{"type": "string", "description": "Short option label shown to the user."},
								"description": map[string]interface{}{"type": "string", "description": "One short sentence explaining the tradeoff."},
							},
							"required": []string{"id", "title"},
						},
						"description": "Optional choice list. Each option needs an id, title, and ideally a short description.",
					},
					"allow_free_text": map[string]interface{}{"type": "boolean", "description": "Allow the user to write a custom answer instead of selecting an option, or add a note alongside an option. If no options are provided, free text is automatically allowed."},
					"run_id":          map[string]interface{}{"type": "string", "description": "Optional schedule/run id connected to the request."},
					"evidence":        map[string]interface{}{"type": "string", "description": "Evidence paths/ids that justify the question."},
					"apply_contract": map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
						"mode":           map[string]interface{}{"type": "string", "enum": []string{"no_change", "direct_apply", "targeted_fixer", "external_wait"}, "description": "Deterministic pre-run handling. Omit only for legacy/informational requests that must not be auto-applied."},
						"issue_id":       map[string]interface{}{"type": "string", "description": "Optional linked canonical PUL issue id when already known."},
						"approved_scope": map[string]interface{}{"type": "string", "description": "Bounded implementation authority as one plain string, not a nested object or array. Required for targeted_fixer."},
						"pre_run_checks": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Exact static/dry-run checks required before the decision may be consumed."},
						"post_run_proof": map[string]interface{}{"type": "string", "description": "Evidence a later producing run must provide, expressed as one plain string rather than an array; this never substitutes for pre-run checks."},
						"failure_policy": map[string]interface{}{"type": "string", "enum": []string{"continue_unchanged", "block_run"}, "description": "Whether a failed application may continue with the old safe plan or must block this run."},
					}},
				},
				"required": []string{"workspace_path", "question"},
			}),
		},
	}
	getTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "get_human_input_request",
			Description: "Read one existing Pulse/report decision by its exact workflow path and input id. Use this before applying an answered decision so its context, selected option, evidence, and lifecycle status are read from the canonical typed record rather than inferred from a summary or queried directly from SQLite.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string", "description": "Exact workflow-relative path, for example Workflow/rtslatency."},
					"input_id":       map[string]interface{}{"type": "string", "description": "Exact decision id supplied by the scheduler, Pulse state, or chat context."},
				},
				"required": []string{"workspace_path", "input_id"},
			}),
		},
	}
	listApprovedFixerTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "list_approved_fixer_decisions",
			Description: "Read the durable queue of answered decisions that explicitly authorize a targeted Pulse Fixer repair. Use this once at the start of a Fix phase before ordinary issue selection. Each returned candidate is mandatory intake: use its exact input_id and issue_id, read both canonical records, and never let normal repair_eligible filtering skip it.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string", "description": "Exact workflow-relative path, for example Workflow/social-media."},
				},
				"required": []string{"workspace_path"},
			}),
		},
	}
	consumeTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "mark_human_input_consumed",
			Description: "Mark an answered workflow decision as consumed after Pulse or Goal Advisor has used the answer and recorded the outcome. This keeps history but removes it from the pending-for-agent queue.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path":  map[string]interface{}{"type": "string", "description": "Workflow-relative path, for example Workflow/social-media."},
					"input_id":        map[string]interface{}{"type": "string", "description": "The input id returned by create_human_input_request."},
					"outcome_summary": map[string]interface{}{"type": "string", "description": "What you did with the answer, in one sentence."},
					"consumed_by":     map[string]interface{}{"type": "string", "description": "Optional actor label. Defaults to agent."},
				},
				"required": []string{"workspace_path", "input_id", "outcome_summary"},
			}),
		},
	}
	answerTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "answer_human_input_request",
			Description: "Record the current user's explicit answer to an existing pending Pulse/report decision. Call this only after the user clearly selects an option or gives a final free-text answer; never infer an answer from discussion. This changes the request to answered so a later Pulse or Goal Advisor run can apply it. It does not apply the decision or mark it consumed.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path":     map[string]interface{}{"type": "string", "description": "Workflow-relative path, for example Workflow/social-media."},
					"input_id":           map[string]interface{}{"type": "string", "description": "Exact decision id supplied in the chat context."},
					"selected_option_id": map[string]interface{}{"type": "string", "description": "Exact option id supplied in the chat context when the user selects an option."},
					"note":               map[string]interface{}{"type": "string", "description": "The user's final free-text answer or optional note. Free text is accepted only when the request allows it."},
				},
				"required": []string{"workspace_path", "input_id"},
			}),
		},
	}
	dismissDuplicateTool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "dismiss_duplicate_human_input_request",
		Description: "Retire only an unlinked duplicate pending decision without answering it. Both requests must be pending with identical question, context, options, source, run, evidence and approval contract. The retained request is untouched. Linked requests are rejected. Never use this to remove an unanswered unique question.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"workspace_path": map[string]interface{}{"type": "string"}, "input_id": map[string]interface{}{"type": "string"},
			"keep_input_id": map[string]interface{}{"type": "string"}, "reason": map[string]interface{}{"type": "string"},
		}, "required": []string{"workspace_path", "input_id", "keep_input_id", "reason"}}),
	}}
	executors := map[string]interface{}{
		"dismiss_duplicate_human_input_request": func(ctx context.Context, args map[string]interface{}) (string, error) {
			ws, _ := args["workspace_path"].(string)
			id, _ := args["input_id"].(string)
			keep, _ := args["keep_input_id"].(string)
			reason, _ := args["reason"].(string)
			input, err := dismissDuplicateHumanInput(ctx, ws, id, keep, reason, mcpexecutor.SessionIDFromContext(ctx))
			if err != nil {
				return "", err
			}
			return marshalReportHumanInputToolResult("duplicate_dismissed", input)
		},
		"list_approved_fixer_decisions": func(ctx context.Context, args map[string]interface{}) (string, error) {
			workspacePath, _ := args["workspace_path"].(string)
			if strings.TrimSpace(workspacePath) == "" {
				return "", fmt.Errorf("workspace_path is required")
			}
			candidates, err := listApprovedTargetedFixerCandidates(ctx, workspacePath)
			if err != nil {
				return "", err
			}
			payload := map[string]interface{}{
				"candidates": candidates,
				"note":       "Each candidate is an explicit approved targeted-Fixer handoff. It overrides normal generic Fixer queue eligibility; leave it unconsumed if the bounded repair cannot be proved.",
			}
			encoded, err := json.Marshal(payload)
			return string(encoded), err
		},
		"get_human_input_request": func(ctx context.Context, args map[string]interface{}) (string, error) {
			workspacePath, _ := args["workspace_path"].(string)
			inputID, _ := args["input_id"].(string)
			if strings.TrimSpace(workspacePath) == "" || strings.TrimSpace(inputID) == "" {
				return "", fmt.Errorf("workspace_path and input_id are required")
			}
			normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
			if err != nil {
				return "", err
			}
			if db == nil {
				return "", fmt.Errorf("human input %q was not found in %s", strings.TrimSpace(inputID), strings.TrimSpace(workspacePath))
			}
			defer db.Close()
			input, err := getReportHumanInputByID(ctx, db, normalized, inputID)
			if err != nil {
				return "", err
			}
			if input == nil {
				return "", fmt.Errorf("human input %q was not found in %s", strings.TrimSpace(inputID), normalized)
			}
			return marshalReportHumanInputToolResult("read", input)
		},
		"create_human_input_request": func(ctx context.Context, args map[string]interface{}) (string, error) {
			req, err := reportHumanInputCreateRequestFromToolArgs(args)
			if err != nil {
				return "", err
			}
			// Pulse decisions land in the user's Needs you list.
			if isPulseDecisionSource(req.Source) {
				if err := checkPulsePlainText("context", plainTextField{name: "question", text: req.Question, maxLen: 300}); err != nil {
					return "", err
				}
			}
			if req.CreatedBy == "" {
				req.CreatedBy = "agent"
			}
			req.CreatedByKind = "agent"
			req.CreatedVia = "agent_tool"
			req.SessionID = mcpexecutor.SessionIDFromContext(ctx)
			input, err := createReportHumanInput(ctx, req.WorkspacePath, req)
			if err != nil {
				return "", err
			}
			return marshalReportHumanInputToolResult("created", input)
		},
		"answer_human_input_request": func(ctx context.Context, args map[string]interface{}) (string, error) {
			requestedWorkspace, _ := args["workspace_path"].(string)
			workspacePath, claims, err := humanAnswerScope(ctx, requestedWorkspace)
			if err != nil {
				return "", err
			}
			inputID, _ := args["input_id"].(string)
			req := ReportHumanInputAnswerRequest{}
			req.SelectedOptionID, _ = args["selected_option_id"].(string)
			req.Note, _ = args["note"].(string)
			req.AnsweredBy = claims.UserID
			req.AnsweredByKind = "human_via_chat"
			req.AnsweredVia = "agent_chat"
			req.SessionID = mcpexecutor.SessionIDFromContext(ctx)
			input, err := answerReportHumanInput(ctx, workspacePath, inputID, req)
			if err != nil {
				return "", err
			}
			result, err := marshalReportHumanInputToolResult("answered", input)
			if err != nil || input == nil {
				return result, err
			}
			// Decisions are answered in chat (the Needs you card pre-fills the
			// choice), so the same turn applies it rather than waiting for the
			// next run's drain. Same instructions as the UI answer path.
			if apply := decisionApplyChatMessage(*input); apply != "" {
				result += "\n\nAPPLY THIS ANSWER NOW, in this same turn, then report in one or two plain sentences what changed:\n" + apply
			}
			return result, nil
		},
		"mark_human_input_consumed": func(ctx context.Context, args map[string]interface{}) (string, error) {
			workspacePath, _ := args["workspace_path"].(string)
			inputID, _ := args["input_id"].(string)
			req := ReportHumanInputConsumeRequest{}
			req.OutcomeSummary, _ = args["outcome_summary"].(string)
			req.ConsumedBy, _ = args["consumed_by"].(string)
			if req.ConsumedBy == "" {
				req.ConsumedBy = "agent"
			}
			req.ConsumedByKind = "agent"
			req.ConsumedVia = "agent_tool"
			req.SessionID = mcpexecutor.SessionIDFromContext(ctx)
			input, err := consumeReportHumanInput(ctx, workspacePath, inputID, req)
			if err != nil {
				return "", err
			}
			return marshalReportHumanInputToolResult("consumed", input)
		},
	}
	categories := map[string]string{
		"dismiss_duplicate_human_input_request": "human_tools",
		"list_approved_fixer_decisions":         "human_tools",
		"get_human_input_request":               "human_tools",
		"create_human_input_request":            "human_tools",
		"answer_human_input_request":            "human_tools",
		"mark_human_input_consumed":             "human_tools",
	}
	return []llmtypes.Tool{getTool, listApprovedFixerTool, createTool, answerTool, consumeTool, dismissDuplicateTool}, executors, categories
}

func reportHumanInputCreateRequestFromToolArgs(args map[string]interface{}) (ReportHumanInputCreateRequest, error) {
	var req ReportHumanInputCreateRequest
	req.WorkspacePath, _ = args["workspace_path"].(string)
	req.InputID, _ = args["input_id"].(string)
	req.Source, _ = args["source"].(string)
	req.Priority, _ = args["priority"].(string)
	req.Question, _ = args["question"].(string)
	req.Context, _ = args["context"].(string)
	req.RunID, _ = args["run_id"].(string)
	req.Evidence, _ = args["evidence"].(string)
	if raw, ok := args["apply_contract"]; ok {
		contractObject, isObject := raw.(map[string]interface{})
		if !isObject {
			return req, fmt.Errorf("apply_contract must be an object")
		}
		for _, field := range []string{"mode", "issue_id", "approved_scope", "post_run_proof", "failure_policy"} {
			if value, exists := contractObject[field]; exists && value != nil {
				if _, isString := value.(string); !isString {
					return req, fmt.Errorf("apply_contract.%s must be a string", field)
				}
			}
		}
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &req.ApplyContract); err != nil {
			return req, fmt.Errorf("invalid apply_contract: %w", err)
		}
	}
	req.CreatedBy, _ = args["created_by"].(string)
	req.AllowFreeText, _ = args["allow_free_text"].(bool)
	if raw, ok := args["options"]; ok {
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &req.Options); err != nil {
			return req, fmt.Errorf("options must be an array of {id,title,description}")
		}
	}
	return req, nil
}

func marshalReportHumanInputToolResult(status string, input *ReportHumanInput) (string, error) {
	payload := map[string]interface{}{
		"status": status,
		"input":  input,
		"note":   "Stored in the workflow-local db/db.sqlite report_human_inputs table.",
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (api *StreamingAPI) handleListReportHumanInputs(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath := r.URL.Query().Get("workspace_path")
	inputs, err := listReportHumanInputs(r.Context(), workspacePath, r.URL.Query().Get("status"), r.URL.Query().Get("source"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "inputs": inputs})
}

func (api *StreamingAPI) handleListReportHumanInputsAggregate(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePaths := r.URL.Query()["workspace_path"]
	if len(workspacePaths) == 0 {
		workspacePaths = strings.Split(r.URL.Query().Get("workspace_paths"), ",")
	}
	seen := make(map[string]struct{}, len(workspacePaths))
	inputs := make([]ReportHumanInput, 0)
	for _, workspacePath := range workspacePaths {
		workspacePath = strings.TrimSpace(workspacePath)
		if workspacePath == "" {
			continue
		}
		normalized, err := normalizeReportHumanInputWorkspacePath(workspacePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		workspaceInputs, err := listReportHumanInputs(r.Context(), normalized, r.URL.Query().Get("status"), r.URL.Query().Get("source"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inputs = append(inputs, workspaceInputs...)
	}
	sort.SliceStable(inputs, func(i, j int) bool {
		return inputs[i].UpdatedAt > inputs[j].UpdatedAt
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "inputs": inputs})
}

func (api *StreamingAPI) handleCreateReportHumanInput(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req ReportHumanInputCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	// Actor identity is server-derived. A request body must not be able to forge
	// who created or answered an operator-facing decision.
	req.CreatedBy = GetUserIDFromContext(r.Context())
	req.CreatedByKind = "human_ui"
	req.CreatedVia = "report_ui"
	req.SessionID = reportHumanInputRequestSessionID(r)
	input, err := createReportHumanInput(r.Context(), req.WorkspacePath, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "input": input})
}

func (api *StreamingAPI) handleAnswerReportHumanInput(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req ReportHumanInputAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.WorkspacePath == "" {
		req.WorkspacePath = r.URL.Query().Get("workspace_path")
	}
	req.AnsweredBy = GetUserIDFromContext(r.Context())
	req.AnsweredByKind = "human_ui"
	req.AnsweredVia = "report_ui"
	req.SessionID = reportHumanInputRequestSessionID(r)
	input, err := answerReportHumanInput(r.Context(), req.WorkspacePath, mux.Vars(r)["input_id"], req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// apply_message lets the UI send the answer to the Builder chat so it is
	// applied now, where the user can watch (decision_apply_chat.go).
	var applyMessage string
	if input != nil {
		applyMessage = decisionApplyChatMessage(*input)
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "input": input, "apply_message": applyMessage})
}

func (api *StreamingAPI) handleDismissReportHumanInput(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var body struct {
		WorkspacePath string `json:"workspace_path"`
		AnsweredBy    string `json:"answered_by"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.WorkspacePath == "" {
		body.WorkspacePath = r.URL.Query().Get("workspace_path")
	}
	req := ReportHumanInputAnswerRequest{
		WorkspacePath:  body.WorkspacePath,
		AnsweredBy:     GetUserIDFromContext(r.Context()),
		AnsweredByKind: "human_ui",
		AnsweredVia:    "report_ui",
		SessionID:      reportHumanInputRequestSessionID(r),
	}
	input, err := dismissReportHumanInput(r.Context(), req.WorkspacePath, mux.Vars(r)["input_id"], req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "input": input})
}

func (api *StreamingAPI) handleConsumeReportHumanInput(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req ReportHumanInputConsumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.WorkspacePath == "" {
		req.WorkspacePath = r.URL.Query().Get("workspace_path")
	}
	req.ConsumedBy = GetUserIDFromContext(r.Context())
	req.ConsumedByKind = "human_ui"
	req.ConsumedVia = "report_ui"
	req.SessionID = reportHumanInputRequestSessionID(r)
	input, err := consumeReportHumanInput(r.Context(), req.WorkspacePath, mux.Vars(r)["input_id"], req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "input": input})
}

func reportHumanInputRequestSessionID(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, header := range []string{"X-AgentWorks-Session-ID", "X-Session-ID", "X-Request-ID"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return "http-request-" + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("http-request-%d", time.Now().UTC().UnixNano())
}

func formatAnsweredReportHumanInputsForAgent(ctx context.Context, workspacePath string) string {
	inputs, err := listReportHumanInputs(ctx, workspacePath, "answered", "")
	if err != nil || len(inputs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Answered human input requests waiting for this workflow pass:\n")
	for _, input := range inputs {
		answer := reportHumanInputAnswerForAgent(input)
		context := strings.TrimSpace(input.Context)
		if context != "" {
			context = " context=" + strconv.Quote(context)
		}
		b.WriteString(fmt.Sprintf("- input_id=%s source=%s priority=%s question=%q answer=%q answered_by=%q actor_kind=%s via=%s answered_at=%s evidence=%q%s\n",
			input.ID, input.Source, input.Priority, input.Question, answer, input.AnsweredBy, input.AnsweredByKind, input.AnsweredVia, input.AnsweredAt, input.Evidence, context))
	}
	b.WriteString("If an answered Goal Advisor plan proposal is approved, apply it only with normal plan modification/config/measurement/report tools, then call mark_human_input_consumed with the concrete outcome. If it is rejected or deferred, record that outcome and consume it. The Pulse popup derives pending questions from typed state, so do not edit a presentation artifact or the SQLite table directly.\n")
	return strings.TrimSpace(b.String())
}

func reportHumanInputAnswerForAgent(input ReportHumanInput) string {
	answer := input.Note
	if input.SelectedOptionID == "" {
		return answer
	}
	answer = fmt.Sprintf("option=%s", input.SelectedOptionID)
	if title := reportHumanInputOptionTitle(input.Options, input.SelectedOptionID); title != "" {
		answer += fmt.Sprintf(" (%s)", title)
	}
	if input.Note != "" {
		answer += "; note=" + input.Note
	}
	return answer
}

func normalizeReportHumanInputSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "user_suggestion":
		return "user_suggestion"
	case "engineering_review", "engineering-review", "engineering review":
		return "technical_review"
	case "ops_review", "ops-review", "ops review", "operations_review", "operations-review", "operations review":
		return "technical_review"
	case "architecture_review", "architecture-review", "architecture review":
		return "architecture_review"
	case "technical_review", "technical-review", "technical review":
		return "technical_review"
	case "strategic_review", "strategic-review", "strategic review",
		"strategy_auditor", "strategy-auditor", "strategy auditor",
		"goal_advisor", "goal-advisor", "goal advisor":
		return "strategic_review"
	default:
		return "pulse"
	}
}

func normalizeReportHumanInputPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low", "high":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "medium"
	}
}

func normalizeReportHumanInputOptions(options []ReportHumanInputOption) ([]ReportHumanInputOption, error) {
	if len(options) == 0 {
		return []ReportHumanInputOption{}, nil
	}
	out := make([]ReportHumanInputOption, 0, len(options))
	seen := map[string]bool{}
	for _, option := range options {
		id := normalizeReportHumanInputID(option.ID)
		title := strings.TrimSpace(option.Title)
		if id == "" || title == "" {
			return nil, fmt.Errorf("each option requires id and title")
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate option id %q", id)
		}
		seen[id] = true
		out = append(out, ReportHumanInputOption{
			ID:          id,
			Title:       title,
			Description: strings.TrimSpace(option.Description),
		})
	}
	return out, nil
}

var reportHumanInputIDRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func normalizeReportHumanInputID(id string) string {
	cleaned := strings.Trim(reportHumanInputIDRe.ReplaceAllString(strings.TrimSpace(id), "-"), "-_")
	if len(cleaned) > 96 {
		cleaned = cleaned[:96]
	}
	return cleaned
}

func newReportHumanInputID(question string) string {
	slug := strings.ToLower(normalizeReportHumanInputID(question))
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-_")
	}
	if slug == "" {
		slug = "question"
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("input-%s-%s", time.Now().UTC().Format("20060102-150405"), slug)
	}
	return fmt.Sprintf("input-%s-%s-%s", time.Now().UTC().Format("20060102-150405"), slug, hex.EncodeToString(buf))
}

func reportHumanInputOptionExists(options []ReportHumanInputOption, id string) bool {
	for _, option := range options {
		if option.ID == id {
			return true
		}
	}
	return false
}

func reportHumanInputOptionTitle(options []ReportHumanInputOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return option.Title
		}
	}
	return ""
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isReportHumanInputsMissingTable(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table: report_human_inputs")
}
