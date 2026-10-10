package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const pulseInterventionsSchema = `CREATE TABLE IF NOT EXISTS pulse_interventions (
	intervention_id TEXT PRIMARY KEY,
	pulse_run_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	criterion_id TEXT NOT NULL,
	impact_type TEXT NOT NULL,
	metric TEXT NOT NULL,
	expected_direction TEXT NOT NULL,
	scope_json TEXT NOT NULL DEFAULT '[]',
	provenance TEXT NOT NULL DEFAULT '',
	baseline_window TEXT NOT NULL DEFAULT '',
	checkpoint TEXT NOT NULL DEFAULT '',
	minimum_evidence_runs INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'awaiting_evidence',
	kind TEXT NOT NULL DEFAULT 'fix_bundle',
	guardrails_json TEXT NOT NULL DEFAULT '[]',
	rollback_condition TEXT NOT NULL DEFAULT '',
	interference_domains_json TEXT NOT NULL DEFAULT '[]',
	human_input_id TEXT NOT NULL DEFAULT '',
	terminal_outcome TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`

const pulseInterventionSourcesSchema = `CREATE TABLE IF NOT EXISTS pulse_intervention_sources (
	intervention_id TEXT NOT NULL,
	source_type TEXT NOT NULL,
	source_id TEXT NOT NULL,
	PRIMARY KEY (intervention_id, source_type, source_id)
)`

const pulseGoalObservationsSchema = `CREATE TABLE IF NOT EXISTS pulse_goal_observations (
	observation_id TEXT PRIMARY KEY,
	criterion_id TEXT NOT NULL,
	metric TEXT NOT NULL,
	run_id TEXT NOT NULL,
	route TEXT NOT NULL DEFAULT '',
	environment TEXT NOT NULL DEFAULT '',
	value REAL,
	status TEXT NOT NULL DEFAULT '',
	unit TEXT NOT NULL DEFAULT '',
	observed_at TEXT NOT NULL,
	window_start TEXT NOT NULL DEFAULT '',
	window_end TEXT NOT NULL DEFAULT '',
	evidence_json TEXT NOT NULL DEFAULT '[]',
	recorded_at TEXT NOT NULL,
	UNIQUE (criterion_id, metric, run_id, route, environment)
)`

const pulseImpactAssessmentsSchema = `CREATE TABLE IF NOT EXISTS pulse_impact_assessments (
	assessment_id TEXT PRIMARY KEY,
	intervention_id TEXT NOT NULL,
	verdict TEXT NOT NULL,
	before_window TEXT NOT NULL,
	after_window TEXT NOT NULL,
	before_value REAL,
	after_value REAL,
	absolute_change REAL,
	relative_change REAL,
	confidence TEXT NOT NULL,
	confounders_json TEXT NOT NULL DEFAULT '[]',
	evidence_json TEXT NOT NULL DEFAULT '[]',
	next_checkpoint TEXT NOT NULL DEFAULT '',
	assessed_at TEXT NOT NULL
)`

type PulseInterventionSource struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
}

// Effects lists additional configured metrics affected by the same change.
// The existing metric/expected_direction fields remain the lead effect for
// compatibility with old callers, decisions and historical assessments.
type PulseMetricEffect struct {
	Metric            string `json:"metric"`
	ExpectedDirection string `json:"expected_direction"`
}

type PulseIntervention struct {
	Effects             []PulseMetricEffect `json:"effects,omitempty"`
	InterventionID      string              `json:"intervention_id"`
	PulseRunID          string              `json:"pulse_run_id,omitempty"`
	Title               string              `json:"title"`
	CriterionID         string              `json:"criterion_id"`
	ImpactType          string              `json:"impact_type"`
	Metric              string              `json:"metric"`
	ExpectedDirection   string              `json:"expected_direction"`
	Scope               []string            `json:"scope,omitempty"`
	Provenance          string              `json:"provenance,omitempty"`
	BaselineWindow      string              `json:"baseline_window,omitempty"`
	Checkpoint          string              `json:"checkpoint,omitempty"`
	MinimumEvidenceRuns int                 `json:"minimum_evidence_runs"`
	Status              string              `json:"status"`
	Kind                string              `json:"kind,omitempty"`
	Guardrails          []string            `json:"guardrails,omitempty"`
	RollbackCondition   string              `json:"rollback_condition,omitempty"`
	// InterferenceDomains names the goal criterion, control surface,
	// channel/cohort, metric stream, shared resource, or contamination boundary
	// an experiment can affect. Use stable values such as "control:reply-copy"
	// or "metric:qualified-replies". Running experiments may coexist only when
	// these sets do not overlap.
	InterferenceDomains []string                  `json:"interference_domains,omitempty"`
	HumanInputID        string                    `json:"human_input_id,omitempty"`
	TerminalOutcome     string                    `json:"terminal_outcome,omitempty"`
	CreatedAt           string                    `json:"created_at,omitempty"`
	UpdatedAt           string                    `json:"updated_at,omitempty"`
	Sources             []PulseInterventionSource `json:"sources,omitempty"`
}

type PulseGoalObservation struct {
	ObservationID string   `json:"observation_id"`
	CriterionID   string   `json:"criterion_id"`
	Metric        string   `json:"metric"`
	RunID         string   `json:"run_id"`
	Route         string   `json:"route,omitempty"`
	Environment   string   `json:"environment,omitempty"`
	Value         *float64 `json:"value,omitempty"`
	Status        string   `json:"status,omitempty"`
	Unit          string   `json:"unit,omitempty"`
	ObservedAt    string   `json:"observed_at"`
	WindowStart   string   `json:"window_start,omitempty"`
	WindowEnd     string   `json:"window_end,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
	RecordedAt    string   `json:"recorded_at,omitempty"`
}

type PulseImpactAssessment struct {
	Metric         string   `json:"metric,omitempty"`
	AssessmentID   string   `json:"assessment_id"`
	InterventionID string   `json:"intervention_id"`
	Verdict        string   `json:"verdict"`
	BeforeWindow   string   `json:"before_window"`
	AfterWindow    string   `json:"after_window"`
	BeforeValue    *float64 `json:"before_value,omitempty"`
	AfterValue     *float64 `json:"after_value,omitempty"`
	AbsoluteChange *float64 `json:"absolute_change,omitempty"`
	RelativeChange *float64 `json:"relative_change,omitempty"`
	Confidence     string   `json:"confidence"`
	Confounders    []string `json:"confounders,omitempty"`
	Evidence       []string `json:"evidence,omitempty"`
	NextCheckpoint string   `json:"next_checkpoint,omitempty"`
	AssessedAt     string   `json:"assessed_at"`
}

type PulseImpactUpdate struct {
	Interventions []PulseIntervention     `json:"interventions,omitempty"`
	Observations  []PulseGoalObservation  `json:"observations,omitempty"`
	Assessments   []PulseImpactAssessment `json:"assessments,omitempty"`
}

type PulseImpactLedger struct {
	Metrics       []GoalMetric            `json:"metrics"`
	Interventions []PulseIntervention     `json:"interventions"`
	Observations  []PulseGoalObservation  `json:"observations"`
	Assessments   []PulseImpactAssessment `json:"assessments"`
}

func ensurePulseImpactSchema(ctx context.Context, db pulseFindingLifecycleDB) error {
	for _, ddl := range []string{
		pulseInterventionsSchema,
		pulseInterventionSourcesSchema,
		pulseGoalObservationsSchema,
		pulseImpactAssessmentsSchema,
		`CREATE INDEX IF NOT EXISTS idx_pulse_interventions_criterion ON pulse_interventions(criterion_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_pulse_goal_observations_criterion ON pulse_goal_observations(criterion_id, metric, observed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_pulse_impact_assessments_intervention ON pulse_impact_assessments(intervention_id, assessed_at DESC)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	if err := ensurePulseTableColumns(ctx, db, "pulse_goal_observations", map[string]string{"window_start": "TEXT NOT NULL DEFAULT ''", "window_end": "TEXT NOT NULL DEFAULT ''"}); err != nil {
		return err
	}
	if err := ensurePulseInterventionColumns(ctx, db); err != nil {
		return err
	}
	if err := ensurePulseTableColumns(ctx, db, "pulse_impact_assessments", map[string]string{"metric": "TEXT NOT NULL DEFAULT ''"}); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pulse_intervention_effects (
 intervention_id TEXT NOT NULL, metric TEXT NOT NULL, expected_direction TEXT NOT NULL,
 PRIMARY KEY(intervention_id, metric))`)
	return err
}

func ensurePulseInterventionColumns(ctx context.Context, db pulseFindingLifecycleDB) error {
	return ensurePulseTableColumns(ctx, db, "pulse_interventions", map[string]string{
		"kind":                      "TEXT NOT NULL DEFAULT 'fix_bundle'",
		"guardrails_json":           "TEXT NOT NULL DEFAULT '[]'",
		"rollback_condition":        "TEXT NOT NULL DEFAULT ''",
		"interference_domains_json": "TEXT NOT NULL DEFAULT '[]'",
		"human_input_id":            "TEXT NOT NULL DEFAULT ''",
		"terminal_outcome":          "TEXT NOT NULL DEFAULT ''",
	})
}

func ensurePulseTableColumns(ctx context.Context, db pulseFindingLifecycleDB, table string, columns map[string]string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for name, definition := range columns {
		if existing[name] {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+definition); err != nil {
			return err
		}
	}
	return nil
}

func pulseImpactID(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized = append(normalized, strings.TrimSpace(part))
	}
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	return hex.EncodeToString(sum[:])[:20]
}

func pulseImpactJSON(values []string) string {
	encoded, _ := json.Marshal(normalizedLifecycleStrings(values))
	return string(encoded)
}

// Each closed set is declared once and used by both the accept check and the
// rejection message, so a value can never be dropped from the list an agent is
// shown while remaining acceptable, or the reverse.
var (
	pulseImpactTypeValues           = []string{"direct_goal", "reliability", "measurement", "presentation_maintenance"}
	pulseImpactVerdictValues        = []string{"improved", "unchanged", "regressed", "inconclusive", "confounded"}
	pulseFixBundleStatusValues      = []string{"awaiting_evidence", "measuring", "assessed", "retired"}
	pulseStrategyExperimentStatuses = []string{"proposed", "deferred", "approved", "running", "measuring", "blocked", "adopted", "rejected", "retired"}
	pulseInterventionKindValues     = []string{"fix_bundle", "strategy_experiment", "architecture_improvement"}
	pulseImpactSourceTypeValues     = []string{"attempt", "experiment", "finding", "review"}
	pulseExpectedDirectionValues    = []string{"increase", "decrease", "maintain"}
	pulseAssessmentConfidenceValues = []string{"low", "medium", "high"}
)

// SyncPulseImprovementDecisionTx advances linked proposals in the same transaction
// as the real user decision/application receipt. Approval alone never means applied.
func SyncPulseImprovementDecisionTx(ctx context.Context, tx *sql.Tx, inputID, selected string, consumed bool, outcome, now string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='pulse_interventions'").Scan(&exists); err != nil || exists == 0 {
		return err
	}
	status := ""
	switch selected {
	case "approve":
		status = "approved"
	case "reject":
		status = "rejected"
	case "defer":
		status = "deferred"
	}
	if status == "" {
		return nil
	}
	if consumed && selected == "approve" {
		status = "running"
	}
	_, err := tx.ExecContext(ctx, `UPDATE pulse_interventions SET status=?,
		provenance=CASE WHEN ? THEN CASE WHEN provenance='' THEN ? ELSE provenance || char(10) || ? END ELSE provenance END,
		terminal_outcome=CASE WHEN ?='rejected' THEN 'User rejected proposal' ELSE terminal_outcome END,
		updated_at=? WHERE human_input_id=? AND kind IN ('architecture_improvement','strategy_experiment')
		AND status IN ('proposed','deferred','approved','blocked')`, status, consumed && selected == "approve", outcome, outcome, status, now, inputID)
	return err
}

func validPulseImpactType(value string) bool {
	return pulseValueAllowed(value, pulseImpactTypeValues)
}

func validPulseImpactVerdict(value string) bool {
	return pulseValueAllowed(value, pulseImpactVerdictValues)
}

func isPlannedPulseImprovement(kind string) bool {
	return kind == "strategy_experiment" || kind == "architecture_improvement"
}

func validPulseInterventionStatus(kind, value string) bool {
	if isPlannedPulseImprovement(kind) {
		return pulseValueAllowed(value, pulseStrategyExperimentStatuses)
	}
	return pulseValueAllowed(value, pulseFixBundleStatusValues)
}

func validPulseImpactSourceType(value string) bool {
	return pulseValueAllowed(value, pulseImpactSourceTypeValues)
}

// RecordPulseImpactUpdate stores the durable second half of the Pulse loop.
// Observations and assessments are append-only. Intervention definitions may
// advance their lifecycle status while retaining their stable identity.
func RecordPulseImpactUpdate(ctx context.Context, workspacePath string, update PulseImpactUpdate) (*PulseImpactLedger, error) {
	if len(update.Interventions) == 0 && len(update.Observations) == 0 && len(update.Assessments) == 0 {
		return nil, fmt.Errorf("record_pulse_impact requires at least one of interventions, observations, or assessments; each is an array of objects, e.g. \"observations\": [{\"criterion_id\": \"sc-1\", \"metric\": \"posts_published\", \"run_id\": \"run-2026-08-01\", \"observed_at\": \"2026-08-01T09:00:00Z\", \"value\": 4}]")
	}
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensurePulseImpactSchema(ctx, db); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for index, intervention := range update.Interventions {
		intervention.InterventionID = strings.TrimSpace(intervention.InterventionID)
		intervention.Title = strings.TrimSpace(intervention.Title)
		intervention.CriterionID = strings.TrimSpace(intervention.CriterionID)
		intervention.ImpactType = strings.TrimSpace(intervention.ImpactType)
		intervention.Metric = strings.TrimSpace(intervention.Metric)
		intervention.ExpectedDirection = strings.TrimSpace(intervention.ExpectedDirection)
		intervention.Kind = strings.TrimSpace(intervention.Kind)
		intervention.RollbackCondition = strings.TrimSpace(intervention.RollbackCondition)
		intervention.InterferenceDomains = normalizedLifecycleStrings(intervention.InterferenceDomains)
		intervention.HumanInputID = strings.TrimSpace(intervention.HumanInputID)
		intervention.TerminalOutcome = strings.TrimSpace(intervention.TerminalOutcome)
		if intervention.Title == "" || intervention.CriterionID == "" || intervention.Metric == "" {
			return nil, fmt.Errorf("interventions[%d] requires all of title, criterion_id, and metric (got %s); criterion_id is a stable success-criterion id from the workflow's goal contract and metric is the measurement that criterion is judged by",
				index, pulseArrivalReport(
					pulseStringArrival("title", intervention.Title),
					pulseStringArrival("criterion_id", intervention.CriterionID),
					pulseStringArrival("metric", intervention.Metric)))
		}
		if !validPulseImpactType(intervention.ImpactType) {
			return nil, fmt.Errorf("interventions[%d] has invalid impact_type %q. Must be one of: %s. Only work that moves the goal itself is direct_goal; fixes that keep the workflow running are reliability, and measurement/presentation_maintenance cover instrumentation and reporting",
				index, intervention.ImpactType, pulseAllowed(pulseImpactTypeValues))
		}
		if !pulseValueAllowed(intervention.ExpectedDirection, pulseExpectedDirectionValues) {
			return nil, fmt.Errorf("interventions[%d] has invalid expected_direction %q. Must be one of: %s",
				index, intervention.ExpectedDirection, pulseAllowed(pulseExpectedDirectionValues))
		}
		if intervention.InterventionID == "" {
			intervention.InterventionID = "int-" + pulseImpactID(intervention.CriterionID, intervention.Metric, intervention.Title)
		}
		if intervention.Kind == "" {
			intervention.Kind = "fix_bundle"
		}
		if !pulseValueAllowed(intervention.Kind, pulseInterventionKindValues) {
			return nil, fmt.Errorf("interventions[%d] has invalid kind %q. Must be one of: %s", index, intervention.Kind, pulseAllowed(pulseInterventionKindValues))
		}
		if intervention.MinimumEvidenceRuns < 1 {
			intervention.MinimumEvidenceRuns = 1
		}
		if strings.TrimSpace(intervention.Status) == "" {
			if isPlannedPulseImprovement(intervention.Kind) {
				intervention.Status = "proposed"
			} else {
				intervention.Status = "awaiting_evidence"
			}
		}
		if !validPulseInterventionStatus(intervention.Kind, intervention.Status) {
			allowed := pulseFixBundleStatusValues
			if isPlannedPulseImprovement(intervention.Kind) {
				allowed = pulseStrategyExperimentStatuses
			}
			return nil, fmt.Errorf("interventions[%d] has invalid status %q for %s. Must be one of: %s", index, intervention.Status, intervention.Kind, pulseAllowed(allowed))
		}
		if isPlannedPulseImprovement(intervention.Kind) {
			intervention.InterferenceDomains = normalizedLifecycleStrings(intervention.InterferenceDomains)
			if intervention.BaselineWindow == "" || intervention.Checkpoint == "" || len(normalizedLifecycleStrings(intervention.Guardrails)) == 0 || intervention.RollbackCondition == "" {
				return nil, fmt.Errorf("strategy experiment interventions[%d] require baseline_window, checkpoint, at least one guardrail, and rollback_condition", index)
			}
			if isActiveStrategyExperimentStatus(intervention.Status) {
				if len(intervention.InterferenceDomains) == 0 {
					return nil, fmt.Errorf("running strategy experiment interventions[%d] requires interference_domains so concurrent experiments can be checked for contamination", index)
				}
				rows, err := tx.QueryContext(ctx, `SELECT intervention_id, interference_domains_json FROM pulse_interventions
					WHERE kind IN ('strategy_experiment', 'architecture_improvement') AND intervention_id<>? AND status IN ('running', 'measuring')`, intervention.InterventionID)
				if err != nil {
					return nil, err
				}
				for rows.Next() {
					var otherID, rawDomains string
					if err := rows.Scan(&otherID, &rawDomains); err != nil {
						rows.Close()
						return nil, err
					}
					if conflicts := overlappingPulseDomains(intervention.InterferenceDomains, decodePulseImpactStrings(rawDomains)); len(conflicts) > 0 {
						rows.Close()
						return nil, fmt.Errorf("strategy experiment interventions[%d] conflicts with running experiment %q on interference domains: %s", index, otherID, strings.Join(conflicts, ", "))
					}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return nil, err
				}
				if err := rows.Close(); err != nil {
					return nil, err
				}
			}
			if (intervention.Status == "adopted" || intervention.Status == "rejected" || intervention.Status == "retired") && intervention.TerminalOutcome == "" {
				return nil, fmt.Errorf("strategy experiment interventions[%d] in terminal status %q require terminal_outcome", index, intervention.Status)
			}
		}
		if isPlannedPulseImprovement(intervention.Kind) {
			var previousStatus string
			readErr := tx.QueryRowContext(ctx, "SELECT status FROM pulse_interventions WHERE intervention_id=?", intervention.InterventionID).Scan(&previousStatus)
			if readErr != nil && readErr != sql.ErrNoRows {
				return nil, readErr
			}
			if (previousStatus == "running" || previousStatus == "measuring" || previousStatus == "adopted") && (intervention.Status == "proposed" || intervention.Status == "approved" || intervention.Status == "deferred") {
				return nil, fmt.Errorf("an applied improvement cannot be reset to %s; assess, block or retire it with evidence", intervention.Status)
			}
		}
		if err := recordPulseMetricEffects(ctx, tx, intervention); err != nil {
			return nil, err
		}
		if isPlannedPulseImprovement(intervention.Kind) && intervention.Status == "adopted" {
			if err := requirePulseMetricAssessments(ctx, tx, intervention); err != nil {
				return nil, err
			}
		}
		if intervention.Kind == "architecture_improvement" {
			if intervention.Status == "running" || intervention.Status == "measuring" || intervention.Status == "adopted" {
				var status, selected string
				if err := tx.QueryRowContext(ctx, "SELECT status, selected_option_id FROM report_human_inputs WHERE id=?", intervention.HumanInputID).Scan(&status, &selected); err != nil || status != "consumed" || selected != "approve" {
					return nil, fmt.Errorf("architecture improvement requires a consumed approval/application receipt before %s", intervention.Status)
				}
			}
			if intervention.Status == "adopted" {
				var verdict string
				if err := tx.QueryRowContext(ctx, "SELECT verdict FROM pulse_impact_assessments WHERE intervention_id=? ORDER BY assessed_at DESC, rowid DESC LIMIT 1", intervention.InterventionID).Scan(&verdict); err != nil || (verdict != "improved" && verdict != "unchanged") {
					return nil, fmt.Errorf("architecture improvement adoption requires an outcome assessment")
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pulse_interventions
			(intervention_id, pulse_run_id, title, criterion_id, impact_type, metric, expected_direction,
			 scope_json, provenance, baseline_window, checkpoint, minimum_evidence_runs, status, kind,
			 guardrails_json, rollback_condition, interference_domains_json, human_input_id, terminal_outcome, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(intervention_id) DO UPDATE SET
			 pulse_run_id=excluded.pulse_run_id, title=excluded.title, criterion_id=excluded.criterion_id,
			 impact_type=excluded.impact_type, metric=excluded.metric, expected_direction=excluded.expected_direction,
			 scope_json=excluded.scope_json, provenance=excluded.provenance,
			 baseline_window=excluded.baseline_window, checkpoint=excluded.checkpoint,
			 minimum_evidence_runs=excluded.minimum_evidence_runs, status=excluded.status, kind=excluded.kind,
			 guardrails_json=excluded.guardrails_json, rollback_condition=excluded.rollback_condition,
			 interference_domains_json=excluded.interference_domains_json,
			 human_input_id=excluded.human_input_id, terminal_outcome=excluded.terminal_outcome, updated_at=excluded.updated_at`,
			intervention.InterventionID, strings.TrimSpace(intervention.PulseRunID), intervention.Title,
			intervention.CriterionID, intervention.ImpactType, intervention.Metric,
			intervention.ExpectedDirection, pulseImpactJSON(intervention.Scope), strings.TrimSpace(intervention.Provenance),
			strings.TrimSpace(intervention.BaselineWindow), strings.TrimSpace(intervention.Checkpoint),
			intervention.MinimumEvidenceRuns, strings.TrimSpace(intervention.Status), intervention.Kind,
			pulseImpactJSON(intervention.Guardrails), intervention.RollbackCondition, pulseImpactJSON(intervention.InterferenceDomains), intervention.HumanInputID,
			intervention.TerminalOutcome, now, now); err != nil {
			return nil, err
		}
		for sourceIndex, source := range intervention.Sources {
			source.SourceType = strings.TrimSpace(source.SourceType)
			source.SourceID = strings.TrimSpace(source.SourceID)
			if source.SourceType == "" || source.SourceID == "" {
				return nil, fmt.Errorf("interventions[%d].sources[%d] requires both source_type and source_id (got %s); source_type must be one of: %s",
					index, sourceIndex, pulseArrivalReport(
						pulseStringArrival("source_type", source.SourceType),
						pulseStringArrival("source_id", source.SourceID)),
					pulseAllowed(pulseImpactSourceTypeValues))
			}
			if !validPulseImpactSourceType(source.SourceType) {
				return nil, fmt.Errorf("interventions[%d].sources[%d] has invalid source_type %q. Must be one of: %s",
					index, sourceIndex, source.SourceType, pulseAllowed(pulseImpactSourceTypeValues))
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pulse_intervention_sources
				(intervention_id, source_type, source_id) VALUES (?, ?, ?)`,
				intervention.InterventionID, strings.TrimSpace(source.SourceType), strings.TrimSpace(source.SourceID)); err != nil {
				return nil, err
			}
		}
	}

	for index, observation := range update.Observations {
		observation.CriterionID = strings.TrimSpace(observation.CriterionID)
		observation.Metric = strings.TrimSpace(observation.Metric)
		observation.RunID = strings.TrimSpace(observation.RunID)
		observation.ObservedAt = strings.TrimSpace(observation.ObservedAt)
		if observation.CriterionID == "" || observation.Metric == "" || observation.RunID == "" || observation.ObservedAt == "" {
			return nil, fmt.Errorf("observations[%d] requires all of criterion_id, metric, run_id, and observed_at (got %s); run_id is the workflow run the measurement came from and observed_at is its RFC3339 timestamp",
				index, pulseArrivalReport(
					pulseStringArrival("criterion_id", observation.CriterionID),
					pulseStringArrival("metric", observation.Metric),
					pulseStringArrival("run_id", observation.RunID),
					pulseStringArrival("observed_at", observation.ObservedAt)))
		}
		if observation.Value == nil && strings.TrimSpace(observation.Status) == "" {
			return nil, fmt.Errorf("observations[%d] (%s/%s) requires either value or status; use the numeric value when one was measured, otherwise a short qualitative status such as not_measured or unavailable",
				index, observation.CriterionID, observation.Metric)
		}
		if err := validateObservationWindow(observation); err != nil {
			return nil, fmt.Errorf("observations[%d]: %w", index, err)
		}
		observation.ObservationID = strings.TrimSpace(observation.ObservationID)
		if observation.ObservationID == "" {
			observation.ObservationID = "obs-" + pulseImpactID(observation.CriterionID, observation.Metric, observation.RunID, observation.Route, observation.Environment)
		}
		var value interface{}
		if observation.Value != nil {
			value = *observation.Value
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pulse_goal_observations
			(observation_id, criterion_id, metric, run_id, route, environment, value, status, unit, observed_at, window_start, window_end, evidence_json, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			observation.ObservationID, observation.CriterionID, observation.Metric, observation.RunID,
			strings.TrimSpace(observation.Route), strings.TrimSpace(observation.Environment), value,
			strings.TrimSpace(observation.Status), strings.TrimSpace(observation.Unit), observation.ObservedAt,
			observation.WindowStart, observation.WindowEnd, pulseImpactJSON(observation.Evidence), now); err != nil {
			return nil, err
		}
	}

	for index, assessment := range update.Assessments {
		assessment.InterventionID = strings.TrimSpace(assessment.InterventionID)
		assessment.Verdict = strings.TrimSpace(assessment.Verdict)
		assessment.BeforeWindow = strings.TrimSpace(assessment.BeforeWindow)
		assessment.AfterWindow = strings.TrimSpace(assessment.AfterWindow)
		assessment.Confidence = strings.TrimSpace(assessment.Confidence)
		assessment.AssessedAt = strings.TrimSpace(assessment.AssessedAt)
		if assessment.InterventionID == "" || assessment.BeforeWindow == "" || assessment.AfterWindow == "" || assessment.Confidence == "" || assessment.AssessedAt == "" {
			return nil, fmt.Errorf("assessments[%d] requires all of intervention_id, before_window, after_window, confidence, and assessed_at (got %s); confidence must be one of: %s",
				index, pulseArrivalReport(
					pulseStringArrival("intervention_id", assessment.InterventionID),
					pulseStringArrival("before_window", assessment.BeforeWindow),
					pulseStringArrival("after_window", assessment.AfterWindow),
					pulseStringArrival("confidence", assessment.Confidence),
					pulseStringArrival("assessed_at", assessment.AssessedAt)),
				pulseAllowed(pulseAssessmentConfidenceValues))
		}
		if !validPulseImpactVerdict(assessment.Verdict) {
			return nil, fmt.Errorf("assessments[%d] has invalid verdict %q. Must be one of: %s. Use inconclusive when the evidence window has not matured and confounded when overlapping changes make attribution impossible",
				index, assessment.Verdict, pulseAllowed(pulseImpactVerdictValues))
		}
		if !pulseValueAllowed(assessment.Confidence, pulseAssessmentConfidenceValues) {
			return nil, fmt.Errorf("assessments[%d] has invalid confidence %q. Must be one of: %s",
				index, assessment.Confidence, pulseAllowed(pulseAssessmentConfidenceValues))
		}
		var interventionExists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_interventions WHERE intervention_id=?`, assessment.InterventionID).Scan(&interventionExists); err != nil {
			return nil, err
		}
		if interventionExists != 1 {
			return nil, fmt.Errorf("assessments[%d] references unknown intervention_id %q; an assessment can only be appended to an intervention that already exists, so include it in this call's interventions array or use the intervention_id shown in get_pulse_state(view=\"module\").impact_ledger",
				index, assessment.InterventionID)
		}
		var leadMetric string
		if err := tx.QueryRowContext(ctx, "SELECT metric FROM pulse_interventions WHERE intervention_id=?", assessment.InterventionID).Scan(&leadMetric); err != nil {
			return nil, err
		}
		if assessment.Metric == "" {
			assessment.Metric = leadMetric
		}
		if assessment.Metric != leadMetric {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pulse_intervention_effects WHERE intervention_id=? AND metric=?", assessment.InterventionID, assessment.Metric).Scan(&count); err != nil {
				return nil, err
			}
			if count != 1 {
				return nil, fmt.Errorf("assessment metric %q is not an effect of intervention %q", assessment.Metric, assessment.InterventionID)
			}
		}
		assessment.AssessmentID = strings.TrimSpace(assessment.AssessmentID)
		if assessment.AssessmentID == "" {
			assessment.AssessmentID = "impact-" + pulseImpactID(assessment.InterventionID, assessment.BeforeWindow, assessment.AfterWindow, assessment.AssessedAt)
			if assessment.Metric != leadMetric {
				assessment.AssessmentID = "impact-" + pulseImpactID(assessment.InterventionID, assessment.Metric, assessment.BeforeWindow, assessment.AfterWindow, assessment.AssessedAt)
			}
		}
		var existingIntervention, existingMetric string
		readErr := tx.QueryRowContext(ctx, "SELECT intervention_id, metric FROM pulse_impact_assessments WHERE assessment_id=?", assessment.AssessmentID).Scan(&existingIntervention, &existingMetric)
		if readErr != nil && readErr != sql.ErrNoRows {
			return nil, readErr
		}
		if readErr == nil {
			if existingMetric == "" {
				existingMetric = leadMetric
			}
			if existingIntervention != assessment.InterventionID || existingMetric != assessment.Metric {
				return nil, fmt.Errorf("assessment_id %q already belongs to another intervention or metric", assessment.AssessmentID)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pulse_impact_assessments
			(assessment_id, intervention_id, verdict, before_window, after_window, before_value, after_value,
			 absolute_change, relative_change, confidence, confounders_json, evidence_json, next_checkpoint, assessed_at, metric)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			assessment.AssessmentID, assessment.InterventionID, assessment.Verdict,
			assessment.BeforeWindow, assessment.AfterWindow, nullableFloat(assessment.BeforeValue),
			nullableFloat(assessment.AfterValue), nullableFloat(assessment.AbsoluteChange), nullableFloat(assessment.RelativeChange),
			assessment.Confidence, pulseImpactJSON(assessment.Confounders), pulseImpactJSON(assessment.Evidence),
			strings.TrimSpace(assessment.NextCheckpoint), assessment.AssessedAt, assessment.Metric); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pulse_interventions
			SET status=CASE
				WHEN kind IN ('strategy_experiment', 'architecture_improvement') AND status IN ('running', 'measuring') THEN 'measuring'
				WHEN kind='fix_bundle' AND status!='retired' THEN CASE
     WHEN EXISTS (SELECT 1 FROM pulse_intervention_effects e WHERE e.intervention_id=pulse_interventions.intervention_id
       AND NOT EXISTS (SELECT 1 FROM pulse_impact_assessments a WHERE a.intervention_id=e.intervention_id AND a.metric=e.metric))
       OR NOT EXISTS (SELECT 1 FROM pulse_impact_assessments a WHERE a.intervention_id=pulse_interventions.intervention_id AND (a.metric=pulse_interventions.metric OR a.metric=''))
     THEN 'measuring' ELSE 'assessed' END
				ELSE status
			END, updated_at=?
			WHERE intervention_id=?`,
			now, assessment.InterventionID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return LoadPulseImpactLedger(ctx, workspacePath, 200)
}

func isActiveStrategyExperimentStatus(status string) bool {
	switch status {
	case "running", "measuring":
		return true
	default:
		return false
	}
}

func overlappingPulseDomains(left, right []string) []string {
	seen := make(map[string]bool, len(left))
	for _, domain := range normalizedLifecycleStrings(left) {
		seen[domain] = true
	}
	var overlap []string
	for _, domain := range normalizedLifecycleStrings(right) {
		if seen[domain] {
			overlap = append(overlap, domain)
		}
	}
	return overlap
}

func nullableFloat(value *float64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func decodePulseImpactStrings(raw string) []string {
	var values []string
	_ = json.Unmarshal([]byte(raw), &values)
	if values == nil {
		return []string{}
	}
	return values
}

// LoadPulseImpactLedger returns current intervention definitions and immutable
// observation/assessment history, newest first. It is the source for Pulse UI
// and email projections; HTML is never the source of truth.
func LoadPulseImpactLedger(ctx context.Context, workspacePath string, limit int) (*PulseImpactLedger, error) {
	if limit <= 0 {
		limit = 200
	}
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return &PulseImpactLedger{Interventions: []PulseIntervention{}, Observations: []PulseGoalObservation{}, Assessments: []PulseImpactAssessment{}}, err
	}
	defer db.Close()
	if err := ensurePulseImpactSchema(ctx, db); err != nil {
		return nil, err
	}
	ledger := &PulseImpactLedger{Interventions: []PulseIntervention{}, Observations: []PulseGoalObservation{}, Assessments: []PulseImpactAssessment{}}
	ledger.Metrics, err = loadGoalMetrics(ctx, db)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `SELECT intervention_id, pulse_run_id, title, criterion_id, impact_type, metric,
		expected_direction, scope_json, provenance, baseline_window, checkpoint, minimum_evidence_runs,
		status, kind, guardrails_json, rollback_condition, interference_domains_json, human_input_id, terminal_outcome, created_at, updated_at
		FROM pulse_interventions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item PulseIntervention
		var scopeJSON, guardrailsJSON, interferenceDomainsJSON string
		if err := rows.Scan(&item.InterventionID, &item.PulseRunID, &item.Title, &item.CriterionID,
			&item.ImpactType, &item.Metric, &item.ExpectedDirection, &scopeJSON, &item.Provenance,
			&item.BaselineWindow, &item.Checkpoint, &item.MinimumEvidenceRuns, &item.Status, &item.Kind,
			&guardrailsJSON, &item.RollbackCondition, &interferenceDomainsJSON, &item.HumanInputID, &item.TerminalOutcome,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		item.Scope = decodePulseImpactStrings(scopeJSON)
		item.Guardrails = decodePulseImpactStrings(guardrailsJSON)
		item.InterferenceDomains = decodePulseImpactStrings(interferenceDomainsJSON)
		sourceRows, sourceErr := db.QueryContext(ctx, `SELECT source_type, source_id FROM pulse_intervention_sources WHERE intervention_id=? ORDER BY source_type, source_id`, item.InterventionID)
		if sourceErr != nil {
			rows.Close()
			return nil, sourceErr
		}
		item.Sources = []PulseInterventionSource{}
		for sourceRows.Next() {
			var source PulseInterventionSource
			if err := sourceRows.Scan(&source.SourceType, &source.SourceID); err != nil {
				sourceRows.Close()
				rows.Close()
				return nil, err
			}
			item.Sources = append(item.Sources, source)
		}
		sourceRows.Close()
		effectRows, err := db.QueryContext(ctx, "SELECT metric, expected_direction FROM pulse_intervention_effects WHERE intervention_id=? ORDER BY metric", item.InterventionID)
		if err != nil {
			rows.Close()
			return nil, err
		}
		for effectRows.Next() {
			var effect PulseMetricEffect
			if err := effectRows.Scan(&effect.Metric, &effect.ExpectedDirection); err != nil {
				effectRows.Close()
				rows.Close()
				return nil, err
			}
			item.Effects = append(item.Effects, effect)
		}
		err = effectRows.Err()
		effectRows.Close()
		if err != nil {
			rows.Close()
			return nil, err
		}
		ledger.Interventions = append(ledger.Interventions, item)
	}
	rows.Close()

	rows, err = db.QueryContext(ctx, `SELECT observation_id, criterion_id, metric, run_id, route, environment,
		value, status, unit, observed_at, window_start, window_end, evidence_json, recorded_at
		FROM pulse_goal_observations WHERE observation_id IN (
 SELECT observation_id FROM pulse_goal_observations ORDER BY julianday(observed_at) DESC, recorded_at DESC LIMIT ?
 ) OR observation_id IN (
 SELECT observation_id FROM (
 SELECT o.observation_id, ROW_NUMBER() OVER (PARTITION BY o.metric,o.criterion_id,o.unit,o.route,o.environment ORDER BY julianday(o.observed_at) DESC, o.recorded_at DESC) AS position
 FROM pulse_goal_observations o JOIN workflow_goal_metrics m ON m.metric_id=o.metric AND m.active=1
 WHERE o.criterion_id=json_extract(m.definition_json,'$.criterion_id') AND o.unit=json_extract(m.definition_json,'$.unit')
 AND o.route=json_extract(m.definition_json,'$.route') AND o.environment=json_extract(m.definition_json,'$.environment')
 ) WHERE position<=120
 ) ORDER BY julianday(observed_at) DESC, recorded_at DESC`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item PulseGoalObservation
		var value sql.NullFloat64
		var evidenceJSON string
		if err := rows.Scan(&item.ObservationID, &item.CriterionID, &item.Metric, &item.RunID, &item.Route,
			&item.Environment, &value, &item.Status, &item.Unit, &item.ObservedAt, &item.WindowStart, &item.WindowEnd, &evidenceJSON, &item.RecordedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if value.Valid {
			v := value.Float64
			item.Value = &v
		}
		item.Evidence = decodePulseImpactStrings(evidenceJSON)
		ledger.Observations = append(ledger.Observations, item)
	}
	rows.Close()

	rows, err = db.QueryContext(ctx, `SELECT assessment_id, intervention_id, verdict, before_window, after_window,
		before_value, after_value, absolute_change, relative_change, confidence, confounders_json,
		evidence_json, next_checkpoint, assessed_at, CASE WHEN metric='' THEN (SELECT metric FROM pulse_interventions i WHERE i.intervention_id=pulse_impact_assessments.intervention_id) ELSE metric END FROM pulse_impact_assessments ORDER BY assessed_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item PulseImpactAssessment
		var before, after, absolute, relative sql.NullFloat64
		var confoundersJSON, evidenceJSON string
		if err := rows.Scan(&item.AssessmentID, &item.InterventionID, &item.Verdict, &item.BeforeWindow,
			&item.AfterWindow, &before, &after, &absolute, &relative, &item.Confidence,
			&confoundersJSON, &evidenceJSON, &item.NextCheckpoint, &item.AssessedAt, &item.Metric); err != nil {
			rows.Close()
			return nil, err
		}
		if before.Valid {
			v := before.Float64
			item.BeforeValue = &v
		}
		if after.Valid {
			v := after.Float64
			item.AfterValue = &v
		}
		if absolute.Valid {
			v := absolute.Float64
			item.AbsoluteChange = &v
		}
		if relative.Valid {
			v := relative.Float64
			item.RelativeChange = &v
		}
		item.Confounders = decodePulseImpactStrings(confoundersJSON)
		item.Evidence = decodePulseImpactStrings(evidenceJSON)
		ledger.Assessments = append(ledger.Assessments, item)
	}
	rows.Close()
	return ledger, nil
}
