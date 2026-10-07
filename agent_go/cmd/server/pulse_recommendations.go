package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	mcpexecutor "github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The Goal Lead answers decision requests as recommendations first (PLAT-697
// phase 3, owner 2026-10-07). On a workflow with a goal, the Pulse and daily
// goal-check turns attach a structured recommendation to each pending
// decision: the option, why, the evidence, a confidence, what it blocks and,
// only when the default is safe and within the autonomy levels, a "safe
// default by" time. The owner confirms it with one click (Accept) or changes
// it. Pulse never answers: humanAnswerScope still refuses Pulse, scheduled and
// background sessions, and the recommendation is stored apart from the
// owner's answer (pulse_recommendations, beside report_human_inputs in the
// workflow's db/db.sqlite).
//
// The same rows are the decision log: what Pulse recommended, why, what the
// owner did, and what happened after (the outcome a later goal check fills in
// with record_pulse_decision_outcome).
//
// pulse.autonomy.answer ("recommend", the default, or "act") is kept for a
// later phase; only "recommend" is implemented, so "act" behaves the same.

const pulseRecommendationsSchema = `CREATE TABLE IF NOT EXISTS pulse_recommendations (
	input_id TEXT NOT NULL,
	workspace_path TEXT NOT NULL,
	option_id TEXT NOT NULL DEFAULT '',
	answer TEXT NOT NULL DEFAULT '',
	why TEXT NOT NULL DEFAULT '',
	evidence TEXT NOT NULL DEFAULT '',
	confidence TEXT NOT NULL DEFAULT 'medium',
	blocks TEXT NOT NULL DEFAULT '',
	safe_default_by TEXT NOT NULL DEFAULT '',
	recommended_by TEXT NOT NULL DEFAULT 'pulse',
	session_id TEXT NOT NULL DEFAULT '',
	recommended_at TEXT NOT NULL,
	owner_response TEXT NOT NULL DEFAULT '',
	owner_answer TEXT NOT NULL DEFAULT '',
	responded_at TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL DEFAULT '',
	outcome_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (workspace_path, input_id)
)`

// PulseRecommendation is the Goal Lead's recommended answer to one decision
// request, and that decision's log entry.
type PulseRecommendation struct {
	InputID       string `json:"input_id"`
	OptionID      string `json:"option_id,omitempty"`
	OptionTitle   string `json:"option_title,omitempty"`
	Answer        string `json:"answer,omitempty"`
	Why           string `json:"why"`
	Evidence      string `json:"evidence,omitempty"`
	Confidence    string `json:"confidence"`
	Blocks        string `json:"blocks,omitempty"`
	SafeDefaultBy string `json:"safe_default_by,omitempty"`
	RecommendedBy string `json:"recommended_by"`
	SessionID     string `json:"session_id,omitempty"`
	RecommendedAt string `json:"recommended_at"`
	// OwnerResponse: "" (waiting), accepted, changed or dismissed.
	OwnerResponse string `json:"owner_response,omitempty"`
	OwnerAnswer   string `json:"owner_answer,omitempty"`
	RespondedAt   string `json:"responded_at,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	OutcomeAt     string `json:"outcome_at,omitempty"`
}

func (rec *PulseRecommendation) recommendedLabel(options []ReportHumanInputOption) string {
	if rec == nil {
		return ""
	}
	if title := reportHumanInputOptionTitle(options, rec.OptionID); title != "" {
		return title
	}
	if rec.OptionID != "" {
		return rec.OptionID
	}
	return rec.Answer
}

// PulseDecisionLogEntry is one decision-log row for the Pulse tab.
type PulseDecisionLogEntry struct {
	PulseRecommendation
	Question       string `json:"question"`
	DecisionStatus string `json:"decision_status"`
	Recommended    string `json:"recommended"`
}

const pulseRecommendationColumns = `input_id, option_id, answer, why, evidence, confidence, blocks, safe_default_by, recommended_by, session_id,
	recommended_at, owner_response, owner_answer, responded_at, outcome, outcome_at`

func scanPulseRecommendation(row reportHumanInputScanner, extra ...interface{}) (*PulseRecommendation, error) {
	var rec PulseRecommendation
	dest := []interface{}{&rec.InputID, &rec.OptionID, &rec.Answer, &rec.Why, &rec.Evidence, &rec.Confidence, &rec.Blocks,
		&rec.SafeDefaultBy, &rec.RecommendedBy, &rec.SessionID, &rec.RecommendedAt, &rec.OwnerResponse, &rec.OwnerAnswer,
		&rec.RespondedAt, &rec.Outcome, &rec.OutcomeAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	return &rec, nil
}

// pulseRecommendationsByInput reads the recommendations for a workflow's
// decisions; list responses attach them so Needs you can show them.
func pulseRecommendationsByInput(ctx context.Context, db *sql.DB, workspacePath string) (map[string]*PulseRecommendation, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+pulseRecommendationColumns+` FROM pulse_recommendations WHERE workspace_path=?`, workspacePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*PulseRecommendation{}
	for rows.Next() {
		rec, err := scanPulseRecommendation(rows)
		if err != nil {
			return nil, err
		}
		out[rec.InputID] = rec
	}
	return out, rows.Err()
}

func getPulseRecommendation(ctx context.Context, db *sql.DB, workspacePath, inputID string) (*PulseRecommendation, error) {
	rec, err := scanPulseRecommendation(db.QueryRowContext(ctx, `SELECT `+pulseRecommendationColumns+`
		FROM pulse_recommendations WHERE workspace_path=? AND input_id=?`, workspacePath, inputID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

// recordPulseOwnerResponseTx marks the recommendation on inputID as accepted
// or changed by the owner's answer, inside the answer's transaction.
func recordPulseOwnerResponseTx(ctx context.Context, tx *sql.Tx, workspacePath, inputID, response, ownerAnswer, now string) error {
	if response == "" {
		_, err := tx.ExecContext(ctx, `UPDATE pulse_recommendations
			SET owner_response=CASE WHEN (option_id<>'' AND option_id=?) OR (option_id='' AND answer<>'' AND answer=?) THEN 'accepted' ELSE 'changed' END,
			    owner_answer=?, responded_at=?
			WHERE workspace_path=? AND input_id=?`, ownerAnswer, ownerAnswer, ownerAnswer, now, workspacePath, inputID)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE pulse_recommendations SET owner_response=?, owner_answer=?, responded_at=?
		WHERE workspace_path=? AND input_id=?`, response, ownerAnswer, now, workspacePath, inputID)
	return err
}

// afterOwnerAnswer copies a saved owner answer into goal memory. It never
// fails the answer.
func afterOwnerAnswer(ctx context.Context, db *sql.DB, input *ReportHumanInput) {
	if input == nil {
		return
	}
	rec, err := getPulseRecommendation(ctx, db, input.WorkspacePath, input.ID)
	if err != nil {
		log.Printf("[PULSE] read recommendation for %s/%s: %v", input.WorkspacePath, input.ID, err)
	}
	if err := rememberOwnerAnswer(input.WorkspacePath, input, rec); err != nil {
		log.Printf("[PULSE] goal memory not updated for %s/%s: %v", input.WorkspacePath, input.ID, err)
	}
}

func normalizePulseAnswerMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "recommend":
		return "recommend", nil
	case "act":
		return "act", nil
	}
	return "", fmt.Errorf("pulse.autonomy.answer must be recommend or act")
}

// pulseAnswerMode is pulse.autonomy.answer. Only "recommend" is implemented:
// "act" is accepted in workflow.json for a later phase and behaves the same.
func pulseAnswerMode(ctx context.Context, workspacePath string) string {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest.Pulse == nil || manifest.Pulse.Autonomy == nil {
		return "recommend"
	}
	mode, err := normalizePulseAnswerMode(manifest.Pulse.Autonomy.Answer)
	if err != nil {
		return "recommend"
	}
	return mode
}

// recordPulseRecommendationFromToolArgs backs record_pulse_recommendation.
func recordPulseRecommendationFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := stringToolArg(args, "workspace_path")
	inputID := stringToolArg(args, "input_id")
	if workspacePath == "" || inputID == "" {
		return "", fmt.Errorf("record_pulse_recommendation requires workspace_path and input_id")
	}
	why := stringToolArg(args, "why")
	if why == "" {
		return "", fmt.Errorf("why is required: one or two plain sentences on why this option serves the goal")
	}
	confidence := strings.ToLower(stringToolArg(args, "confidence"))
	if confidence == "" {
		confidence = "medium"
	}
	if confidence != "low" && confidence != "medium" && confidence != "high" {
		return "", fmt.Errorf("confidence must be low, medium or high")
	}
	if err := checkPulsePlainText("evidence",
		plainTextField{name: "why", text: why, maxLen: 400},
		plainTextField{name: "blocks", text: stringToolArg(args, "blocks"), maxLen: 200},
		plainTextField{name: "answer", text: stringToolArg(args, "answer"), maxLen: 400}); err != nil {
		return "", err
	}
	defer publishHumanInputsChanged(workspacePath)
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()
	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		return "", err
	}
	if db == nil {
		return "", fmt.Errorf("decision %q was not found in %s", inputID, workspacePath)
	}
	defer db.Close()
	input, err := getReportHumanInputByID(ctx, db, normalized, inputID)
	if err != nil {
		return "", err
	}
	if input == nil {
		return "", fmt.Errorf("decision %q was not found in %s", inputID, normalized)
	}
	if input.Source == "user_suggestion" {
		return "", fmt.Errorf("a user suggestion is not a decision for the Goal Lead to recommend on")
	}
	if input.Status != "pending" {
		return "", fmt.Errorf("decision %q is %s; recommend only on pending decisions", inputID, input.Status)
	}
	optionID := normalizeReportHumanInputID(stringToolArg(args, "option_id"))
	answer := stringToolArg(args, "answer")
	if len(input.Options) > 0 {
		if !reportHumanInputOptionExists(input.Options, optionID) {
			ids := make([]string, 0, len(input.Options))
			for _, option := range input.Options {
				ids = append(ids, option.ID)
			}
			return "", fmt.Errorf("option_id must be one of this decision's options: %s", strings.Join(ids, ", "))
		}
		answer = ""
	} else {
		optionID = ""
		if answer == "" {
			return "", fmt.Errorf("this decision has no options: give the recommended answer in answer")
		}
	}
	safeDefaultBy := ""
	if raw := stringToolArg(args, "safe_default_by"); raw != "" {
		when, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			if day, dayErr := time.Parse("2006-01-02", raw); dayErr == nil {
				when, err = day, nil
			}
		}
		if err != nil {
			return "", fmt.Errorf("safe_default_by must be RFC3339 or YYYY-MM-DD")
		}
		if !when.After(time.Now()) {
			return "", fmt.Errorf("safe_default_by must be in the future")
		}
		// A default the owner did not answer is only offered when it is safe:
		// high confidence, and applying it needs no permission the workflow
		// keeps at ask (a workflow change needs change=auto).
		if confidence != "high" {
			return "", fmt.Errorf("a safe default needs high confidence; leave safe_default_by empty")
		}
		switch input.ApplyContract.Mode {
		case "", "no_change", "external_wait":
		default:
			if pulseAutonomyForView(ctx, normalized).Change != "auto" {
				return "", fmt.Errorf("this option changes the workflow and pulse.autonomy.change is ask: a safe default is not within the autonomy levels; leave safe_default_by empty")
			}
		}
		safeDefaultBy = when.UTC().Format(time.RFC3339)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sessionID := mcpexecutor.SessionIDFromContext(ctx)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO pulse_recommendations
		(input_id, workspace_path, option_id, answer, why, evidence, confidence, blocks, safe_default_by, recommended_by, session_id, recommended_at)
		VALUES (?,?,?,?,?,?,?,?,?,'pulse',?,?)
		ON CONFLICT(workspace_path, input_id) DO UPDATE SET option_id=excluded.option_id, answer=excluded.answer, why=excluded.why,
			evidence=excluded.evidence, confidence=excluded.confidence, blocks=excluded.blocks, safe_default_by=excluded.safe_default_by,
			session_id=excluded.session_id, recommended_at=excluded.recommended_at`,
		input.ID, normalized, optionID, answer, why, stringToolArg(args, "evidence"), confidence, stringToolArg(args, "blocks"),
		safeDefaultBy, sessionID, now); err != nil {
		return "", err
	}
	details, _ := json.Marshal(map[string]string{"option_id": optionID, "confidence": confidence})
	if err := writeReportHumanInputEvent(ctx, tx, normalized, reportHumanInputEvent{
		InputID: input.ID, EventType: "recommended", Status: "pending", ActorID: "pulse",
		ActorKind: "agent", Channel: "pulse_recommendation", SessionID: sessionID, Details: string(details), CreatedAt: now,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	msg := "Recommendation recorded. The owner confirms it with Accept or changes it; it is not an answer and nothing is applied until the owner answers."
	if pulseAnswerMode(ctx, normalized) == "act" {
		msg += " (pulse.autonomy.answer=act is not available yet; the Goal Lead recommends.)"
	}
	return msg, nil
}

// recordPulseDecisionOutcomeFromToolArgs backs record_pulse_decision_outcome:
// what happened after a decision the Goal Lead recommended on.
func recordPulseDecisionOutcomeFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := stringToolArg(args, "workspace_path")
	inputID := stringToolArg(args, "input_id")
	outcome := stringToolArg(args, "outcome")
	if workspacePath == "" || inputID == "" || outcome == "" {
		return "", fmt.Errorf("record_pulse_decision_outcome requires workspace_path, input_id and outcome")
	}
	if err := checkPulsePlainText("goal memory", plainTextField{name: "outcome", text: outcome, maxLen: 300}); err != nil {
		return "", err
	}
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()
	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		return "", err
	}
	if db == nil {
		return "", fmt.Errorf("no decision log in %s", workspacePath)
	}
	defer db.Close()
	result, err := db.ExecContext(ctx, `UPDATE pulse_recommendations SET outcome=?, outcome_at=? WHERE workspace_path=? AND input_id=?`,
		outcome, time.Now().UTC().Format(time.RFC3339), normalized, inputID)
	if err != nil {
		return "", err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return "", fmt.Errorf("decision %q has no Goal Lead recommendation in %s", inputID, normalized)
	}
	return "Outcome recorded in the decision log. Add a lesson to goal memory when there is one.", nil
}

// listPulseDecisionLog returns the decision log, newest first.
func listPulseDecisionLog(ctx context.Context, workspacePath string, limit int) ([]PulseDecisionLogEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()
	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulseDecisionLogEntry{}, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT r.input_id, r.option_id, r.answer, r.why, r.evidence, r.confidence, r.blocks, r.safe_default_by,
		r.recommended_by, r.session_id, r.recommended_at, r.owner_response, r.owner_answer, r.responded_at, r.outcome, r.outcome_at,
		COALESCE(h.question,''), COALESCE(h.status,''), COALESCE(h.options_json,'[]')
		FROM pulse_recommendations r LEFT JOIN report_human_inputs h ON h.id=r.input_id AND h.workspace_path=r.workspace_path
		WHERE r.workspace_path=? ORDER BY r.recommended_at DESC LIMIT ?`, normalized, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PulseDecisionLogEntry{}
	for rows.Next() {
		var entry PulseDecisionLogEntry
		var optionsJSON string
		rec, err := scanPulseRecommendation(rows, &entry.Question, &entry.DecisionStatus, &optionsJSON)
		if err != nil {
			return nil, err
		}
		var options []ReportHumanInputOption
		_ = json.Unmarshal([]byte(optionsJSON), &options)
		rec.OptionTitle = reportHumanInputOptionTitle(options, rec.OptionID)
		if title := reportHumanInputOptionTitle(options, rec.OwnerAnswer); title != "" {
			rec.OwnerAnswer = title
		}
		entry.PulseRecommendation = *rec
		entry.Recommended = rec.recommendedLabel(options)
		out = append(out, entry)
	}
	return out, rows.Err()
}

// goalLeadAgentContext is what the Pulse and goal-check turns read first:
// goal memory, the pending decisions to recommend on, and decisions whose
// outcome is still to record.
func goalLeadAgentContext(ctx context.Context, workspacePath string) map[string]interface{} {
	memory, err := readGoalMemory(workspacePath)
	if err != nil {
		memory = ""
	}
	toRecommend := []map[string]interface{}{}
	if inputs, err := listReportHumanInputs(ctx, workspacePath, "pending", ""); err == nil {
		for _, input := range inputs {
			if input.Source == "user_suggestion" {
				continue
			}
			item := map[string]interface{}{"input_id": input.ID, "question": input.Question, "options": input.Options, "created_at": input.CreatedAt}
			if input.Recommendation != nil {
				item["current_recommendation"] = input.Recommendation.recommendedLabel(input.Options)
			}
			toRecommend = append(toRecommend, item)
		}
	}
	outcomesDue := []map[string]string{}
	if entries, err := listPulseDecisionLog(ctx, workspacePath, 30); err == nil {
		cutoff := time.Now().Add(-24 * time.Hour)
		for _, entry := range entries {
			responded := parseStoredTime(entry.RespondedAt)
			if entry.Outcome == "" && entry.OwnerResponse != "" && entry.OwnerResponse != "dismissed" && !responded.IsZero() && responded.Before(cutoff) {
				outcomesDue = append(outcomesDue, map[string]string{"input_id": entry.InputID, "question": entry.Question, "owner_answer": entry.OwnerAnswer, "owner_response": entry.OwnerResponse, "responded_at": entry.RespondedAt})
			}
		}
	}
	entries := parseGoalMemory(memory).entryCount()
	note := "Goal memory (memory/goal.md): read it first. soul.md wins on any conflict. Never re-ask what the owner already answered here. Add results, lessons, open bets and what waits on the owner with record_pulse_goal_memory (one line each, source marked); owner answers are copied in automatically."
	if entries > goalMemoryMaxEntries*3/4 {
		note += fmt.Sprintf(" It has %d entries: consolidate it now (record_pulse_goal_memory action=consolidate) to one line per preference or lesson.", entries)
	}
	return map[string]interface{}{
		"goal_memory":            memory,
		"goal_memory_note":       note,
		"answer_mode":            pulseAnswerMode(ctx, workspacePath),
		"decisions_to_recommend": toRecommend,
		"decisions_note":         "Pending decision requests. For each, call record_pulse_recommendation once (refresh it only on new evidence): the option, why, evidence, confidence, what it blocks, and safe_default_by only when that default is safe and within the autonomy levels. You never answer a decision; the owner accepts or changes your recommendation. Say you do not know the owner's preference instead of guessing it.",
		"outcomes_due":           outcomesDue,
		"outcomes_note":          "Decisions the owner answered over a day ago with no outcome yet: record what happened after with record_pulse_decision_outcome (from runs and goal readings), and a lesson in goal memory when there is one.",
	}
}

// handleGetGoalLead serves the Pulse tab's goal memory and decision log.
func (api *StreamingAPI) handleGetGoalLead(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	memory, memoryErr := readGoalMemory(workspacePath)
	entries, logErr := listPulseDecisionLog(r.Context(), workspacePath, 100)
	resp := map[string]interface{}{
		"success":      true,
		"memory":       memory,
		"memory_path":  workspacePath + "/" + goalMemoryRelPath,
		"decision_log": entries,
		"answer_mode":  pulseAnswerMode(r.Context(), workspacePath),
	}
	if memoryErr != nil {
		resp["memory_error"] = memoryErr.Error()
	}
	if logErr != nil {
		resp["decision_log"] = []PulseDecisionLogEntry{}
		resp["decision_log_error"] = logErr.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handlePutGoalMemory saves the owner's edit of the goal memory.
func (api *StreamingAPI) handlePutGoalMemory(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var body struct {
		WorkspacePath string `json:"workspace_path"`
		Content       string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	// The access guard reads the query first (requestWorkflowWorkspacePath);
	// the write goes to the same workflow.
	if query := strings.TrimSpace(r.URL.Query().Get("workspace_path")); query != "" {
		body.WorkspacePath = query
	}
	if err := saveGoalMemoryContent(body.WorkspacePath, body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// createGoalLeadTools are the Goal Lead's phase 3 writes: a recommendation on
// a decision (never an answer), a decision's outcome, and goal memory.
func createGoalLeadTools() []llmtypes.Tool {
	entry := map[string]interface{}{
		"section": map[string]interface{}{"type": "string", "enum": goalMemorySections},
		"source":  map[string]interface{}{"type": "string", "enum": []string{"owner_answer", "result", "pulse_inference"}, "description": "owner_answer only for what the owner said; result for a dated result; pulse_inference for your own reading (marked as such)."},
		"text":    map[string]interface{}{"type": "string", "description": "One plain line, at most 300 characters."},
		"date":    map[string]interface{}{"type": "string", "description": "YYYY-MM-DD the entry is from; defaults to today."},
	}
	return []llmtypes.Tool{
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_recommendation",
			Description: "The Goal Lead's recommended answer to one pending decision request on this workflow (PLAT-697): the option, why, the evidence, confidence, what it blocks, and safe_default_by only when that default is safe (high confidence) and within the autonomy levels. It is shown on the decision in Needs you; the owner accepts it with one click or changes it. It is never an answer: you cannot answer decisions, and nothing is applied until the owner answers. Re-recording replaces your earlier recommendation. Say you do not know the owner's preference instead of guessing it; goal memory holds what the owner already said.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path":  map[string]interface{}{"type": "string", "description": "Workflow-relative path, e.g. Workflow/substack."},
					"input_id":        map[string]interface{}{"type": "string", "description": "The pending decision's id."},
					"option_id":       map[string]interface{}{"type": "string", "description": "The recommended option's id (required when the decision has options)."},
					"answer":          map[string]interface{}{"type": "string", "description": "The recommended free-text answer, only for a decision without options."},
					"why":             map[string]interface{}{"type": "string", "minLength": 1, "description": "One or two plain sentences: why this option serves the goal."},
					"evidence":        map[string]interface{}{"type": "string", "description": "Evidence: dated results, readings, run ids, files, goal memory lines."},
					"confidence":      map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high"}},
					"blocks":          map[string]interface{}{"type": "string", "description": "What waits on this decision, in plain words, e.g. \"Friday's growth run\"."},
					"safe_default_by": map[string]interface{}{"type": "string", "description": "RFC3339 or YYYY-MM-DD. Only when the recommended option is safe to apply if the owner does not answer, with high confidence and within the autonomy levels. Shown to the owner; never applied automatically yet."},
				},
				"required": []string{"workspace_path", "input_id", "why", "confidence"},
			}),
		}},
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_decision_outcome",
			Description: "Record what happened after a decision the Goal Lead recommended on and the owner answered (the decision log's result, shown in the Pulse tab): the effect on the goal and the work, from runs and goal readings, in one plain sentence.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string"},
					"input_id":       map[string]interface{}{"type": "string"},
					"outcome":        map[string]interface{}{"type": "string", "minLength": 1, "description": "What happened after, with the date and number when there is one."},
				},
				"required": []string{"workspace_path", "input_id", "outcome"},
			}),
		}},
		{Type: "function", Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_goal_memory",
			Description: "Write the workflow's goal memory (memory/goal.md beside soul.md): owner preferences and answers, decisions and outcomes, lessons, open bets, and what waits on the owner, one line each with its source and date. action=add adds one entry; action=consolidate replaces the whole memory with entries (one line per preference or lesson; drop what is stale or superseded, keep every owner answer that still holds). soul.md wins on conflict: when memory suggests the goal should change, propose a soul.md edit to the owner instead. Owner answers to decisions are copied in automatically.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string"},
					"action":         map[string]interface{}{"type": "string", "enum": []string{"add", "consolidate"}},
					"section":        entry["section"],
					"source":         entry["source"],
					"text":           entry["text"],
					"date":           entry["date"],
					"entries": map[string]interface{}{
						"type":        "array",
						"description": "consolidate only: the whole memory.",
						"items":       map[string]interface{}{"type": "object", "properties": entry, "required": []string{"section", "source", "text"}},
					},
				},
				"required": []string{"workspace_path", "action"},
			}),
		}},
	}
}
