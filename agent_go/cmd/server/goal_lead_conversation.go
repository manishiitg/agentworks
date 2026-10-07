package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The Pulse as its own chat kind (PLAT-697 phase 4,
// docs/design/pulse_goal_owner.md "A new chat kind, not a Crew").
//
// Each workflow with a goal (soul.md plus a primary metric) has one persistent
// Pulse conversation, created lazily on its first goal check or when the
// Pulse tab first opens. It is built from existing parts:
//
//   - Like a Crew's conversation: one stable session id per workflow (kept in
//     the workflow's Pulse state, goal_lead_conversation). Every turn runs in
//     it and names it as restored_conversation_session_id, so a coding CLI
//     resumes its native session (--resume) and an API model replays its saved
//     transcript, through the same path a Crew conversation and a workflow ask
//     use. The CLI's own compaction keeps a long conversation in bounds; past
//     goalLeadRotateAge or goalLeadRotateTurns the next goal check starts the
//     next conversation, and goal memory (memory/goal.md) carries what matters.
//   - Like a workflow chat: the workflow's own session request (its Builder
//     model, MCP servers, skills), a Pulse turn, with every turn held by the
//     phase 2 tool guard to pulse.autonomy (beginGoalWorkTurn). Builder typed
//     tools run only when change is auto.
//   - Platform-defined: the same charter for every workflow, sent as the first
//     turn of each conversation; nothing the owner edits.
//
// Turns: the daily goal check and the full Pulse's Goal Work (scheduler), the
// owner's messages in the Pulse tab, ask_pulse from the workflow's chats
// and steps, and Slack (<workflow-slug>-pulse). The conversation is shown in the
// Pulse tab (goal_lead_messages), not in the Crew list.

const (
	goalLeadRotateAge         = 30 * 24 * time.Hour
	goalLeadRotateTurns       = 90
	goalLeadMessageMaxRunes   = 6000
	goalLeadOwnerMessageRunes = 4000
	goalLeadTurnHardCap       = 2 * time.Hour
	goalLeadSessionPrefix     = "schedule-goallead--"
)

const goalLeadConversationSchema = `CREATE TABLE IF NOT EXISTS goal_lead_conversation (
	id INTEGER PRIMARY KEY CHECK(id = 1),
	workflow_key TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL,
	generation INTEGER NOT NULL,
	started_at TEXT NOT NULL,
	turns INTEGER NOT NULL DEFAULT 0,
	last_turn_at TEXT NOT NULL DEFAULT ''
)`

const goalLeadMessagesSchema = `CREATE TABLE IF NOT EXISTS goal_lead_messages (
	id TEXT PRIMARY KEY,
	at TEXT NOT NULL,
	role TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT '',
	text TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT ''
)`

// Turn kinds; also the role of the message a turn logs.
const (
	goalLeadTurnCheck    = "check"
	goalLeadTurnGoalWork = "goal_work"
	goalLeadTurnOwner    = "owner"
	goalLeadTurnAsk      = "ask"
	goalLeadTurnSlack    = "slack"
	// goalLeadTurnRunFailed: a workflow run failed (goal_lead_owns_reviews.go).
	goalLeadTurnRunFailed = "run_failed"
)

type goalLeadConversation struct {
	SessionID  string `json:"session_id"`
	Generation int    `json:"generation"`
	StartedAt  string `json:"started_at"`
	Turns      int    `json:"turns"`
	LastTurnAt string `json:"last_turn_at,omitempty"`
}

// GoalLeadMessage is one line of the conversation shown in the Pulse tab.
type GoalLeadMessage struct {
	ID        string `json:"id"`
	At        string `json:"at"`
	Role      string `json:"role"`
	Source    string `json:"source,omitempty"`
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty"`
}

func ensureGoalLeadSchema(ctx context.Context, db *sql.DB) error {
	for _, ddl := range []string{goalLeadConversationSchema, goalLeadMessagesSchema, goalLeadQARequestsSchema} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

// goalLeadSessionID is the conversation's session id: stable per workflow and
// generation. The schedule- prefix keeps it an unattended session (no
// workflow-busy lock, never a person's Builder chat, never answers decisions).
func goalLeadSessionID(workflowKey string, generation int) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(workflowKey)))
	return fmt.Sprintf("%s%s-g%d", goalLeadSessionPrefix, hex.EncodeToString(sum[:])[:16], generation)
}

func isGoalLeadSessionID(sessionID string) bool {
	return strings.HasPrefix(strings.TrimSpace(sessionID), goalLeadSessionPrefix)
}

func newGoalLeadID(prefix string) string {
	buf := make([]byte, 5)
	_, _ = rand.Read(buf)
	return prefix + strings.ToUpper(hex.EncodeToString(buf))
}

func readGoalLeadConversation(ctx context.Context, db *sql.DB) (*goalLeadConversation, error) {
	var conv goalLeadConversation
	err := db.QueryRowContext(ctx, `SELECT session_id, generation, started_at, turns, last_turn_at FROM goal_lead_conversation WHERE id = 1`).
		Scan(&conv.SessionID, &conv.Generation, &conv.StartedAt, &conv.Turns, &conv.LastTurnAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

// ensureGoalLeadConversation returns the workflow's Pulse conversation,
// creating it on first use. With rotate, an old or long conversation that is
// not busy is replaced by the next generation.
func ensureGoalLeadConversation(ctx context.Context, workspacePath, workflowKey string, now time.Time, rotate bool, busy func(string) bool) (goalLeadConversation, error) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return goalLeadConversation{}, err
	}
	defer db.Close()
	if err := ensureGoalLeadSchema(ctx, db); err != nil {
		return goalLeadConversation{}, err
	}
	current, err := readGoalLeadConversation(ctx, db)
	if err != nil {
		return goalLeadConversation{}, err
	}
	if current != nil {
		started := parseStoredTime(current.StartedAt)
		stale := current.Turns >= goalLeadRotateTurns || (!started.IsZero() && now.Sub(started) > goalLeadRotateAge)
		if !rotate || !stale || (busy != nil && busy(current.SessionID)) {
			return *current, nil
		}
	}
	next := goalLeadConversation{Generation: 1, StartedAt: formatStoredTime(now)}
	if current != nil {
		next.Generation = current.Generation + 1
	}
	next.SessionID = goalLeadSessionID(firstNonEmptyTrimmed(workflowKey, workspacePath), next.Generation)
	if _, err := db.ExecContext(ctx, `INSERT INTO goal_lead_conversation (id, workflow_key, session_id, generation, started_at, turns, last_turn_at)
		VALUES (1, ?, ?, ?, ?, 0, '') ON CONFLICT(id) DO UPDATE SET workflow_key=excluded.workflow_key, session_id=excluded.session_id,
		generation=excluded.generation, started_at=excluded.started_at, turns=0, last_turn_at=''`,
		strings.TrimSpace(workflowKey), next.SessionID, next.Generation, next.StartedAt); err != nil {
		return goalLeadConversation{}, err
	}
	if current != nil {
		_ = insertGoalLeadMessage(ctx, db, GoalLeadMessage{Role: "system", Text: fmt.Sprintf("A new Pulse conversation started: the last one had %d turns since %s. Goal memory carries what matters.", current.Turns, shortStoredDate(current.StartedAt)), SessionID: next.SessionID}, now)
	}
	return next, nil
}

func shortStoredDate(value string) string {
	if t := parseStoredTime(value); !t.IsZero() {
		return t.Format("2 Jan 2006")
	}
	return "its start"
}

func insertGoalLeadMessage(ctx context.Context, db *sql.DB, msg GoalLeadMessage, now time.Time) error {
	text := strings.TrimSpace(msg.Text)
	if runes := []rune(text); len(runes) > goalLeadMessageMaxRunes {
		text = string(runes[:goalLeadMessageMaxRunes]) + "…"
	}
	if text == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `INSERT INTO goal_lead_messages (id, at, role, source, text, session_id) VALUES (?,?,?,?,?,?)`,
		newGoalLeadID("GLM-"), formatStoredTime(now), strings.TrimSpace(msg.Role), strings.TrimSpace(msg.Source), text, strings.TrimSpace(msg.SessionID))
	return err
}

func appendGoalLeadMessage(ctx context.Context, workspacePath string, msg GoalLeadMessage) error {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGoalLeadSchema(ctx, db); err != nil {
		return err
	}
	return insertGoalLeadMessage(ctx, db, msg, time.Now().UTC())
}

// listGoalLeadMessages returns the latest limit messages, oldest first.
func listGoalLeadMessages(ctx context.Context, workspacePath string, limit int) ([]GoalLeadMessage, error) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []GoalLeadMessage{}, err
	}
	defer db.Close()
	if err := ensureGoalLeadSchema(ctx, db); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT id, at, role, source, text, session_id FROM goal_lead_messages ORDER BY at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GoalLeadMessage{}
	for rows.Next() {
		var msg GoalLeadMessage
		if err := rows.Scan(&msg.ID, &msg.At, &msg.Role, &msg.Source, &msg.Text, &msg.SessionID); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func bumpGoalLeadTurns(ctx context.Context, workspacePath, sessionID string, now time.Time) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.ExecContext(ctx, `UPDATE goal_lead_conversation SET turns = turns + 1, last_turn_at = ? WHERE id = 1 AND session_id = ?`, formatStoredTime(now), sessionID)
}

// workflowHasGoal: the Pulse exists for a workflow with a primary goal
// metric and a soul.md.
func workflowHasGoal(ctx context.Context, workspacePath string) bool {
	ledger, err := stepworkflow.LoadPulseImpactLedger(ctx, workspacePath, 1)
	if err != nil || ledger == nil {
		return false
	}
	primary := false
	for _, metric := range ledger.Metrics {
		if metric.Role == "primary" {
			primary = true
			break
		}
	}
	if !primary {
		return false
	}
	_, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(workspacePath, "/")+"/soul/soul.md")
	return err == nil && exists
}

// goalLeadTurn is one turn of the Pulse conversation.
type goalLeadTurn struct {
	Kind string
	// From names who sent an owner, ask or Slack turn.
	From string
	// Body is the turn's own text: the composed check or Goal Work
	// instruction, or the raw message for owner, ask and Slack turns.
	Body string
	// Perms are the levels held on the turn's tools; nil means the
	// workflow's pulse.autonomy.
	Perms *stepworkflow.GoalWorkPermissions
	// Rotate lets a scheduled turn start the next conversation generation.
	Rotate bool
	// Logged: the incoming message is already in goal_lead_messages.
	Logged bool
	// CallID is the function call an ask turn answers.
	CallID string
}

// goalLeadNow is the clock of Pulse turns (tests move it a day).
var goalLeadNow = time.Now

// goalLeadTurnRunner, when set (tests), replaces the real turn.
var goalLeadTurnRunner func(ctx context.Context, reqMap map[string]interface{}, sessionID, userID string) (internalSessionTurnResult, error)

// goalLeadInFlight counts turns started per workflow, for the Pulse tab's
// "working" state while a turn waits in the conversation queue.
var goalLeadInFlight = struct {
	sync.Mutex
	byWorkflow map[string]int
}{byWorkflow: map[string]int{}}

func goalLeadTurnStarted(workspacePath string) func() {
	goalLeadInFlight.Lock()
	goalLeadInFlight.byWorkflow[workspacePath]++
	goalLeadInFlight.Unlock()
	return func() {
		goalLeadInFlight.Lock()
		if goalLeadInFlight.byWorkflow[workspacePath] <= 1 {
			delete(goalLeadInFlight.byWorkflow, workspacePath)
		} else {
			goalLeadInFlight.byWorkflow[workspacePath]--
		}
		goalLeadInFlight.Unlock()
	}
}

func goalLeadTurnsInFlight(workspacePath string) int {
	goalLeadInFlight.Lock()
	defer goalLeadInFlight.Unlock()
	return goalLeadInFlight.byWorkflow[workspacePath]
}

// goalLeadCharter is the platform-defined instruction, the same for every
// workflow, sent as the first turn of each conversation.
func goalLeadCharter(label, workspacePath string) string {
	return fmt.Sprintf(`PULSE. You are the %s Pulse: the platform's persistent owner of this workflow's goal (workspace_path=%q). This is your one continuing conversation for the goal. The daily goal check, Goal Work, the owner's messages from the Pulse tab, questions from the workflow's chats and steps (ask_pulse), failed runs, Slack messages and QA results all arrive here as turns. These instructions are the same for every workflow; nobody edits them.

How you work:
- The goal is soul/soul.md: read it, never edit it; propose an edit to the owner when the goal should change. Your memory is memory/goal.md (record_pulse_goal_memory, one dated line with its source); soul.md wins on any conflict.
- Your authority is pulse.autonomy (run, outward, change), enforced by the tools on every turn. Within it, act and record it; beyond it, prepare the work and ask the owner one clear decision with your recommendation. You recommend; only the owner decides (record_pulse_recommendation; you cannot answer decisions). Say you do not know the owner's preference instead of guessing it.
- Skills, loaded when a turn needs them: the goal check, read_skill(skills=[{"name":"builder-reference","path":"references/goal-lead-check.md"}]); Goal Work, references/goal-lead-work.md; a structural question about the workflow, references/goal-lead-architecture.md.
- You own QA and architecture for this workflow: no separate Technical or Architecture review runs. QA is not done in this conversation: when a failed run or step blocks or threatens the goal, call record_pulse_qa_request with what to check; a separate run does it and its short result comes back here. A failed run wakes you once for a short turn; your goal check reads run_health. When your checks raise a structural question, use the architecture skill. Larger plan changes go to the workflow's Builder chat or a decision; Workflow Review checks them before the next run.
- Focus areas: propose them with record_pulse_focus_area (at most three active, each with an end date and its own check); the owner confirms with one click. Track them on each goal check and close them with a lesson.
- Keep replies short and plain: what you did, what you need, why.`, label, workspacePath)
}

func goalLeadTurnQuery(label, workspacePath string, firstTurn bool, turn goalLeadTurn, autonomyText string, now time.Time) string {
	var b strings.Builder
	if firstTurn {
		b.WriteString(goalLeadCharter(label, workspacePath))
		b.WriteString("\n\n")
	}
	date := now.UTC().Format("2006-01-02")
	from := firstNonEmptyTrimmed(turn.From, "the owner")
	switch turn.Kind {
	case goalLeadTurnCheck:
		fmt.Fprintf(&b, "PULSE TURN: daily goal check, %s, workspace_path=%q. Load the goal-check skill (references/goal-lead-check.md) when you need the full procedure.\n\n%s", date, workspacePath, turn.Body)
	case goalLeadTurnGoalWork:
		fmt.Fprintf(&b, "PULSE TURN: Goal Work in the full Pulse, %s, workspace_path=%q. Load the Goal Work skill (references/goal-lead-work.md).\n\n%s", date, workspacePath, turn.Body)
	case goalLeadTurnRunFailed:
		fmt.Fprintf(&b, "PULSE TURN: a run of this workflow failed, %s, workspace_path=%q. The goal-check skill (references/goal-lead-check.md, \"Failed runs\") has the rule.\n\n%s\n\n%s", date, workspacePath, turn.Body, autonomyText)
	case goalLeadTurnAsk:
		fmt.Fprintf(&b, `PULSE TURN: [Function call %s] %s, working on this workflow (workspace_path=%q), asks the Pulse (ask_pulse), %s:

%s

Answer as a recommendation: what you recommend and why, the evidence and your confidence. Say plainly when it is the owner's call, or when you do not know the owner's preference (goal memory holds what they already said). You do not decide for the owner and you cannot answer decisions. Your final reply is returned to the caller as the answer: make it self-contained, the caller does not see this conversation. Do not run anything for it beyond your permission levels.

%s`, firstNonEmptyTrimmed(turn.CallID, "-"), from, workspacePath, date, strings.TrimSpace(turn.Body), autonomyText)
	default:
		where := "in the Pulse tab"
		if turn.Kind == goalLeadTurnSlack {
			where = "on Slack; your reply is posted in their thread"
		}
		fmt.Fprintf(&b, `PULSE TURN: a message from %s %s, %s, workspace_path=%q:

%s

Reply briefly and plainly. When they ask why you did something, answer from this conversation, goal memory, the decision log and your recorded checks. Direction that should last goes to goal memory (record_pulse_goal_memory, source owner_answer, in their words). Time-boxed direction becomes a proposed focus area (record_pulse_focus_area action=propose, with an end date and its own check), which they confirm with one click. A change to the goal itself is a proposed soul.md edit in your reply: never edit soul.md. Anything beyond your permission levels: prepare it and say what you need.

%s`, from, where, date, workspacePath, strings.TrimSpace(turn.Body), autonomyText)
	}
	return b.String()
}

// runGoalLeadTurn runs one turn in the workflow's Pulse conversation and
// returns the reply and the conversation's session id. The turn runs as the
// workflow's execution owner, holds the session's tools to the autonomy
// levels, and is logged for the Pulse tab.
func (api *StreamingAPI) runGoalLeadTurn(ctx context.Context, workspacePath string, turn goalLeadTurn) (string, string, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return "", "", err
	}
	if !found || manifest == nil {
		return "", "", fmt.Errorf("workflow %s not found", workspacePath)
	}
	now := goalLeadNow().UTC()
	conv, err := ensureGoalLeadConversation(ctx, workspacePath, firstNonEmptyTrimmed(manifest.ID, workspacePath), now, turn.Rotate, api.conversationTurnOccupied)
	if err != nil {
		return "", "", fmt.Errorf("pulse conversation: %w", err)
	}
	sessionID := conv.SessionID
	defer goalLeadTurnStarted(workspacePath)()

	sched := api.scheduler
	if sched == nil {
		sched = &SchedulerService{api: api}
	}
	label := firstNonEmptyTrimmed(manifest.Label, workspacePath)
	sctx := buildScheduleContext(workspacePath, manifest, WorkflowSchedule{
		ID: manualWorkflowPulseScheduleID, Name: label + " Pulse", ScheduleType: "cron", Mode: "workshop", WorkshopMode: "workshop",
		Description: "The workflow's persistent Pulse conversation",
	})
	if strings.TrimSpace(sctx.OwnerUserID) == "" {
		return "", sessionID, fmt.Errorf("workflow %s has no owner for its Pulse to run as", workspacePath)
	}
	reqMap := sched.buildWorkshopRequest(ctx, sctx)
	// A Pulse turn on the workflow's Builder runtime: the same authority and
	// session key on every turn, so the native session is resumed, never
	// relaunched for a role change.
	markPulseLifecycleTurn(reqMap)
	reqMap["session_title"] = label + " Pulse"
	reqMap["triggered_by_label"] = "Pulse"
	if turn.Kind == goalLeadTurnOwner || turn.Kind == goalLeadTurnSlack || turn.Kind == goalLeadTurnAsk {
		reqMap["triggered_by_label"] = "Pulse: from " + firstNonEmptyTrimmed(turn.From, "the owner")
	}
	if conv.Turns > 0 || api.workflowAskSessionExists(sessionID, workspacePath) {
		reqMap["restored_conversation_session_id"] = sessionID
	}

	perms, autonomyText := goalWorkAutonomy(ctx, workspacePath)
	if turn.Perms != nil {
		perms = *turn.Perms
		autonomyText = stepworkflow.GoalWorkAutonomyInstructions(perms)
	}
	reqMap["query"] = goalLeadTurnQuery(label, workspacePath, conv.Turns == 0, turn, autonomyText, now)

	switch turn.Kind {
	case goalLeadTurnOwner, goalLeadTurnSlack, goalLeadTurnAsk:
		if !turn.Logged {
			_ = appendGoalLeadMessage(ctx, workspacePath, GoalLeadMessage{Role: turn.Kind, Source: turn.From, Text: turn.Body, SessionID: sessionID})
		}
	}

	// A new turn is a deliberate start: a Stop on an earlier turn must not
	// leave the conversation unable to resume (PLAT-130 guards continuations
	// of the stopped turn, not the next one).
	api.clearSessionStopped(sessionID)
	mcpagent.ClearHTTPSessionStopped(sessionID)
	release := beginGoalWorkTurn(sessionID, perms)
	defer release()

	// As the scheduler's own Pulse turns: the principal is the userID, not
	// claims carried on ctx.
	var result internalSessionTurnResult
	if goalLeadTurnRunner != nil {
		result, err = goalLeadTurnRunner(ctx, reqMap, sessionID, sctx.OwnerUserID)
	} else {
		result, err = api.startSessionInternalWithResult(ctx, reqMap, sessionID, sctx.OwnerUserID, nil)
	}
	bumpGoalLeadTurns(context.WithoutCancel(ctx), workspacePath, sessionID, time.Now().UTC())
	reply := strings.TrimSpace(result.FinalResponse)
	logCtx := context.WithoutCancel(ctx)
	if err != nil {
		_ = appendGoalLeadMessage(logCtx, workspacePath, GoalLeadMessage{Role: "system", Source: turn.Kind, Text: "The Pulse's turn did not finish: " + err.Error(), SessionID: sessionID})
		return reply, sessionID, err
	}
	role := "goal_lead"
	if turn.Kind == goalLeadTurnCheck || turn.Kind == goalLeadTurnGoalWork {
		role = turn.Kind
	}
	if reply != "" {
		_ = appendGoalLeadMessage(logCtx, workspacePath, GoalLeadMessage{Role: role, Source: turn.Kind, Text: reply, SessionID: sessionID})
	}
	return reply, sessionID, nil
}

// runGoalLeadPassStep runs a scheduled Pulse step (the goal check, Goal Work
// and its receipt continuation) in the Pulse conversation instead of the
// pass's own session.
func (s *SchedulerService) runGoalLeadPassStep(ctx context.Context, sctx *ScheduleContext, st pulseLifecycleStep) pulseLifecycleStepRunResult {
	kind := goalLeadTurnGoalWork
	if st.label == "goal-check" {
		kind = goalLeadTurnCheck
	}
	_, sessionID, err := s.api.runGoalLeadTurn(ctx, sctx.WorkspacePath, goalLeadTurn{Kind: kind, Body: st.query, Perms: st.goalWork, Rotate: st.label != "review-fix-continuation"})
	if err != nil {
		s.sessionLogf(sctx, sessionID, "[PULSE] %s turn did not finish: %v", st.label, err)
		return pulseLifecycleStepResultForError(err)
	}
	s.sessionLogf(sctx, sessionID, "[PULSE] %s turn done for %s", st.label, sctx.WorkspacePath)
	return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepCompleted}
}

// pulseLifecycleStepResultForError maps a turn error to its step outcome.
func pulseLifecycleStepResultForError(err error) pulseLifecycleStepRunResult {
	outcome := pulseLifecycleStepWaitFailed
	if errors.Is(err, errWorkshopSequenceInterrupted) || errors.Is(err, context.Canceled) {
		outcome = pulseLifecycleStepInterrupted
	} else if errors.Is(err, errWorkshopIdleWaitTimeout) {
		outcome = pulseLifecycleStepTimedOut
	}
	return pulseLifecycleStepRunResult{outcome: outcome, err: err}
}

// goalLeadConversationView is the Pulse tab's conversation: created on first
// open when the workflow has a goal.
func (api *StreamingAPI) goalLeadConversationView(ctx context.Context, workspacePath string) map[string]interface{} {
	view := map[string]interface{}{"has_goal": false, "messages": []GoalLeadMessage{}, "busy": false}
	if !workflowHasGoal(ctx, workspacePath) {
		return view
	}
	view["has_goal"] = true
	key := workspacePath
	if manifest, found, err := ReadWorkflowManifest(ctx, workspacePath); err == nil && found && manifest != nil {
		key = firstNonEmptyTrimmed(manifest.ID, workspacePath)
	}
	conv, err := ensureGoalLeadConversation(ctx, workspacePath, key, time.Now().UTC(), false, nil)
	if err != nil {
		view["error"] = err.Error()
		return view
	}
	view["session_id"] = conv.SessionID
	view["started_at"] = conv.StartedAt
	view["generation"] = conv.Generation
	view["busy"] = goalLeadTurnsInFlight(workspacePath) > 0 || api.conversationTurnOccupied(conv.SessionID)
	if messages, err := listGoalLeadMessages(ctx, workspacePath, 60); err == nil {
		view["messages"] = messages
	}
	return view
}

// handlePostGoalLeadMessage takes the owner's message from the Pulse tab and
// runs it as a turn in the Pulse conversation. It returns at once; the
// reply appears in the conversation.
func (api *StreamingAPI) handlePostGoalLeadMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var body struct {
		WorkspacePath string `json:"workspace_path"`
		Message       string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if query := strings.TrimSpace(r.URL.Query().Get("workspace_path")); query != "" {
		body.WorkspacePath = query
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(body.WorkspacePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if len([]rune(message)) > goalLeadOwnerMessageRunes {
		http.Error(w, fmt.Sprintf("message is longer than %d characters", goalLeadOwnerMessageRunes), http.StatusBadRequest)
		return
	}
	if !workflowHasGoal(r.Context(), workspacePath) {
		http.Error(w, "this workflow has no goal yet (soul.md and a primary goal metric); set one up in the Builder chat first", http.StatusConflict)
		return
	}
	from := "the owner"
	if claims := GetUserFromContext(r.Context()); claims != nil {
		from = firstNonEmptyTrimmed(claims.Username, claims.Email, claims.UserID, from)
	}
	if err := appendGoalLeadMessage(r.Context(), workspacePath, GoalLeadMessage{Role: goalLeadTurnOwner, Source: from, Text: message}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), goalLeadTurnHardCap)
		defer cancel()
		if _, _, err := api.runGoalLeadTurn(ctx, workspacePath, goalLeadTurn{Kind: goalLeadTurnOwner, From: from, Body: message, Logged: true}); err != nil {
			log.Printf("[PULSE] owner message turn for %s failed: %v", workspacePath, err)
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// goalLeadGoalWorkStep is the full Pulse's strategic_review turn as the Goal
// Lead runs it: the same module contract and receipt, with the short Goal
// Work skill instead of the reviewer contract (strategy-auditor.md, which the
// /run-goal-work command and workflows without a goal keep using).
func goalLeadGoalWorkStep(pulseRunID string) pulseLifecycleStep {
	label, _, contract := pulseModuleReviewParts(pulseModuleStrategicReview)
	return pulseLifecycleStep{label: label, query: fmt.Sprintf(`PULSE MODULE REVIEW. pulse_run_id=%q. This turn owns ONLY module=%q (Goal Work). Read the durable Gate worklist with get_pulse_state(view="module", pulse_run_id=<this id>). If this module is not due or already has a terminal result, say so and end this turn.
Otherwise load read_skill(skills=[{"name":"builder-reference","path":"references/goal-lead-work.md"}]) and do it yourself in this turn. Read get_pulse_state(view="review_notes", module=%q) once for prior reasoning. %s
%sDo not render a dashboard, back up, publish or notify.`, pulseRunID, pulseModuleStrategicReview, pulseModuleStrategicReview, contract, pulseReviewerRecordRules)}
}
