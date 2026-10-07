package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Goal Work is Pulse's primary loop (docs/design/pulse_goal_work.md): work
// that moves the user's goals, done by Pulse, plus evidence-backed challenges
// to soul.md constraints. It is deliberately separate from the bug/finding
// lifecycle: an item is an idea, work in progress, waiting on the user, or
// done — and later we learn whether it worked.
const pulseGoalWorkSchema = `CREATE TABLE IF NOT EXISTS pulse_goal_work (
	id TEXT PRIMARY KEY COLLATE NOCASE,
	kind TEXT NOT NULL CHECK(kind IN ('goal_work','constraint_challenge')),
	title TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL CHECK(status IN ('idea','in_progress','needs_user','done','dropped')),
	action_taken TEXT NOT NULL DEFAULT '',
	links_json TEXT NOT NULL DEFAULT '[]',
	metric TEXT NOT NULL DEFAULT '',
	expected_direction TEXT NOT NULL DEFAULT '',
	check_at TEXT NOT NULL DEFAULT '',
	effect TEXT NOT NULL DEFAULT '' CHECK(effect IN ('','worked','no_effect','unclear')),
	effect_note TEXT NOT NULL DEFAULT '',
	decision_id TEXT NOT NULL DEFAULT '',
	constraint_text TEXT NOT NULL DEFAULT '',
	constraint_class TEXT NOT NULL DEFAULT '' CHECK(constraint_class IN ('','boundary','choice','unconfirmed')),
	created_by_pulse_run_id TEXT NOT NULL DEFAULT '',
	updated_by_pulse_run_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	CHECK(status <> 'done' OR trim(action_taken) <> ''),
	CHECK(status <> 'needs_user' OR trim(decision_id) <> ''),
	CHECK(kind <> 'constraint_challenge' OR (trim(constraint_text) <> '' AND constraint_class <> ''))
)`

type PulseGoalWorkItem struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	Title             string   `json:"title"`
	Detail            string   `json:"detail,omitempty"`
	Status            string   `json:"status"`
	ActionTaken       string   `json:"action_taken,omitempty"`
	Links             []string `json:"links"`
	Metric            string   `json:"metric,omitempty"`
	ExpectedDirection string   `json:"expected_direction,omitempty"`
	CheckAt           string   `json:"check_at,omitempty"`
	Effect            string   `json:"effect,omitempty"`
	EffectNote        string   `json:"effect_note,omitempty"`
	DecisionID        string   `json:"decision_id,omitempty"`
	ConstraintText    string   `json:"constraint_text,omitempty"`
	ConstraintClass   string   `json:"constraint_class,omitempty"`
	CreatedByPulseRun string   `json:"created_by_pulse_run_id,omitempty"`
	UpdatedByPulseRun string   `json:"updated_by_pulse_run_id,omitempty"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
}

func ensurePulseGoalWorkSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, pulseGoalWorkSchema); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_pulse_goal_work_status ON pulse_goal_work(status, updated_at DESC)`)
	return err
}

func newPulseGoalWorkID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return "GW-" + strings.ToUpper(hex.EncodeToString(buf))
}

var pulseGoalWorkStatuses = map[string]bool{"idea": true, "in_progress": true, "needs_user": true, "done": true, "dropped": true}

// recordPulseGoalWork creates or updates one item. Fields left empty on an
// update keep their stored value, so a later pass can add only the effect.
func recordPulseGoalWork(ctx context.Context, workspacePath string, in PulseGoalWorkItem, pulseRunID string) (*PulseGoalWorkItem, error) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensurePulseGoalWorkSchema(ctx, db); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	item := PulseGoalWorkItem{Links: []string{}}
	id := strings.TrimSpace(in.ID)
	if id != "" {
		existing, err := readPulseGoalWorkItem(ctx, db, id)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, fmt.Errorf("goal work item %s not found; omit item_id to create a new one", id)
		}
		item = *existing
	} else {
		item.ID = newPulseGoalWorkID()
		item.CreatedAt = now
		item.CreatedByPulseRun = strings.TrimSpace(pulseRunID)
		item.Kind = "goal_work"
	}
	set := func(dst *string, value string) {
		if v := strings.TrimSpace(value); v != "" {
			*dst = v
		}
	}
	set(&item.Kind, in.Kind)
	set(&item.Title, in.Title)
	set(&item.Detail, in.Detail)
	set(&item.Status, in.Status)
	set(&item.ActionTaken, in.ActionTaken)
	set(&item.Metric, in.Metric)
	set(&item.ExpectedDirection, in.ExpectedDirection)
	set(&item.CheckAt, in.CheckAt)
	set(&item.Effect, in.Effect)
	set(&item.EffectNote, in.EffectNote)
	set(&item.DecisionID, in.DecisionID)
	set(&item.ConstraintText, in.ConstraintText)
	set(&item.ConstraintClass, in.ConstraintClass)
	if len(in.Links) > 0 {
		item.Links = in.Links
	}
	item.UpdatedAt = now
	item.UpdatedByPulseRun = strings.TrimSpace(pulseRunID)

	if item.Kind != "goal_work" && item.Kind != "constraint_challenge" {
		return nil, fmt.Errorf("kind must be goal_work or constraint_challenge")
	}
	if item.Title == "" {
		return nil, fmt.Errorf("title is required: the gap in plain words, or the constraint being challenged")
	}
	if !pulseGoalWorkStatuses[item.Status] {
		return nil, fmt.Errorf("status must be idea, in_progress, needs_user, done or dropped")
	}
	if item.Status == "done" && item.ActionTaken == "" {
		return nil, fmt.Errorf("status done requires action_taken: what Pulse actually did")
	}
	if item.Status == "needs_user" && item.DecisionID == "" {
		return nil, fmt.Errorf("status needs_user requires decision_id from create_human_input_request")
	}
	if item.Kind == "constraint_challenge" && (item.ConstraintText == "" || item.ConstraintClass == "") {
		return nil, fmt.Errorf("constraint_challenge requires constraint_text and constraint_class (boundary, choice or unconfirmed)")
	}
	switch item.Effect {
	case "", "worked", "no_effect", "unclear":
	default:
		return nil, fmt.Errorf("effect must be worked, no_effect or unclear")
	}
	linksJSON, _ := json.Marshal(item.Links)
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_goal_work
		(id,kind,title,detail,status,action_taken,links_json,metric,expected_direction,check_at,effect,effect_note,decision_id,constraint_text,constraint_class,created_by_pulse_run_id,updated_by_pulse_run_id,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,title=excluded.title,detail=excluded.detail,status=excluded.status,
		action_taken=excluded.action_taken,links_json=excluded.links_json,metric=excluded.metric,expected_direction=excluded.expected_direction,
		check_at=excluded.check_at,effect=excluded.effect,effect_note=excluded.effect_note,decision_id=excluded.decision_id,
		constraint_text=excluded.constraint_text,constraint_class=excluded.constraint_class,
		updated_by_pulse_run_id=excluded.updated_by_pulse_run_id,updated_at=excluded.updated_at`,
		item.ID, item.Kind, item.Title, item.Detail, item.Status, item.ActionTaken, string(linksJSON), item.Metric, item.ExpectedDirection,
		item.CheckAt, item.Effect, item.EffectNote, item.DecisionID, item.ConstraintText, item.ConstraintClass,
		item.CreatedByPulseRun, item.UpdatedByPulseRun, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

const pulseGoalWorkColumns = `id,kind,title,detail,status,action_taken,links_json,metric,expected_direction,check_at,effect,effect_note,decision_id,constraint_text,constraint_class,created_by_pulse_run_id,updated_by_pulse_run_id,created_at,updated_at`

func scanPulseGoalWorkItem(scan func(dest ...interface{}) error) (PulseGoalWorkItem, error) {
	var item PulseGoalWorkItem
	var links string
	err := scan(&item.ID, &item.Kind, &item.Title, &item.Detail, &item.Status, &item.ActionTaken, &links, &item.Metric, &item.ExpectedDirection,
		&item.CheckAt, &item.Effect, &item.EffectNote, &item.DecisionID, &item.ConstraintText, &item.ConstraintClass,
		&item.CreatedByPulseRun, &item.UpdatedByPulseRun, &item.CreatedAt, &item.UpdatedAt)
	item.Links = []string{}
	_ = json.Unmarshal([]byte(links), &item.Links)
	return item, err
}

func readPulseGoalWorkItem(ctx context.Context, db *sql.DB, id string) (*PulseGoalWorkItem, error) {
	row := db.QueryRowContext(ctx, `SELECT `+pulseGoalWorkColumns+` FROM pulse_goal_work WHERE id=?`, strings.TrimSpace(id))
	item, err := scanPulseGoalWorkItem(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// listPulseGoalWork returns items newest first. limit <= 0 means 200.
func listPulseGoalWork(ctx context.Context, workspacePath string, limit int) ([]PulseGoalWorkItem, error) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulseGoalWorkItem{}, err
	}
	defer db.Close()
	if err := ensurePulseGoalWorkSchema(ctx, db); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.QueryContext(ctx, `SELECT `+pulseGoalWorkColumns+` FROM pulse_goal_work ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PulseGoalWorkItem{}
	for rows.Next() {
		item, err := scanPulseGoalWorkItem(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func recordPulseGoalWorkFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := stringToolArg(args, "workspace_path")
	if workspacePath == "" {
		return "", fmt.Errorf("record_pulse_goal_work requires workspace_path")
	}
	if err := checkPulsePlainText("links and the files under pulse/work/",
		plainTextField{name: "title", text: stringToolArg(args, "title"), maxLen: 140},
		plainTextField{name: "action_taken", text: stringToolArg(args, "action_taken"), maxLen: 500},
		plainTextField{name: "detail", text: stringToolArg(args, "detail"), maxLen: 1500, idsOnly: true},
		plainTextField{name: "effect_note", text: stringToolArg(args, "effect_note"), maxLen: 300}); err != nil {
		return "", err
	}
	item, err := recordPulseGoalWork(ctx, workspacePath, PulseGoalWorkItem{
		ID: stringToolArg(args, "item_id"), Kind: stringToolArg(args, "kind"), Title: stringToolArg(args, "title"),
		Detail: stringToolArg(args, "detail"), Status: stringToolArg(args, "status"), ActionTaken: stringToolArg(args, "action_taken"),
		Links: stringSliceFromToolArg(args["links"]), Metric: stringToolArg(args, "metric"), ExpectedDirection: stringToolArg(args, "expected_direction"),
		CheckAt: stringToolArg(args, "check_at"), Effect: stringToolArg(args, "effect"), EffectNote: stringToolArg(args, "effect_note"),
		DecisionID: stringToolArg(args, "decision_id"), ConstraintText: stringToolArg(args, "constraint_text"), ConstraintClass: stringToolArg(args, "constraint_class"),
	}, stringToolArg(args, "pulse_run_id"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Goal Work %s recorded: %s (%s).", item.ID, item.Title, item.Status), nil
}

func readPulseGoalWorkView(ctx context.Context, workspacePath string, limit int) (string, error) {
	items, err := listPulseGoalWork(ctx, workspacePath, limit)
	if err != nil {
		return "", err
	}
	// The goal check comes first (PLAT-697): the code-computed goal facts ride
	// along so a pass that reads only this view still sees the silence alarm.
	var goalStatus interface{}
	if view, statusErr := computeGoalStatus(ctx, workspacePath, time.Now().UTC()); statusErr == nil {
		goalStatus = view
	}
	out, err := json.MarshalIndent(map[string]interface{}{
		"goal_status":             goalStatus,
		"goal_lead":               goalLeadAgentContext(ctx, workspacePath),
		"autonomy":                pulseAutonomyForView(ctx, workspacePath),
		"autonomy_note":           "The user's Pulse permissions: auto means do it yourself and record it; ask means prepare it and create a decision. run covers existing steps, outward covers new posts/messages/contacts, change covers plan, step and schedule edits.",
		"focus_areas":             pulseFocusAreasForView(ctx, workspacePath),
		"focus_areas_note":        "The user's current priorities for Goal Work. Start each pass here; they direct attention, they do not limit what you may consider or override soul.md goals and constraints.",
		"focus_area_details":      focusAreasForView(ctx, workspacePath),
		"focus_area_details_note": "Each focus area's lifecycle: end date, its own check, latest tracking, proposals waiting for the owner and recent closes with their lesson. Propose, track and close with record_pulse_focus_area; only the owner confirms.",
		"goal_work":               items,
	}, "", "  ")
	return string(out), err
}

// pulseFocusAreasForView returns the workflow's Goal Work focus areas.
func pulseFocusAreasForView(ctx context.Context, workspacePath string) []string {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest.Pulse == nil {
		return []string{}
	}
	areas, err := normalizePulseFocusAreas(manifest.Pulse.FocusAreas)
	if err != nil {
		return []string{}
	}
	return areas
}

// pulseAutonomyForView is the workflow's Goal Work permissions with defaults
// applied.
func pulseAutonomyForView(ctx context.Context, workspacePath string) PulseAutonomyLevels {
	defaults, _ := resolvePulseAutonomy(nil)
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest.Pulse == nil {
		return defaults
	}
	levels, err := resolvePulseAutonomy(manifest.Pulse.Autonomy)
	if err != nil {
		return defaults
	}
	return levels
}

// pulseAutonomyRunForView is the workflow's Goal Work Run permission for the
// Pulse view: "auto" (default) or "ask".
func pulseAutonomyRunForView(ctx context.Context, workspacePath string) string {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest.Pulse == nil || manifest.Pulse.Autonomy == nil {
		return "auto"
	}
	run, err := normalizePulseAutonomyRun(manifest.Pulse.Autonomy.Run)
	if err != nil {
		return "auto"
	}
	return run
}
