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
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
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
// owner's messages from the Pulse tab's "Talk to Pulse" box, ask_pulse from the
// workflow's chats and steps (a person's Builder chat relays the owner's
// words), and Slack (<workflow-slug>-pulse). The conversation
// (goal_lead_messages) shows in the "<workflow> Pulse" chat tab, not in the
// Pulse tab and not in the Crew list (owner, 2026-10-08).

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
	// goalLeadTurnOwner: the owner's message from the Pulse tab.
	goalLeadTurnOwner = "owner"
	goalLeadTurnAsk   = "ask"
	goalLeadTurnSlack = "slack"
	// goalLeadTurnRunFailed: a workflow run failed (goal_lead_owns_reviews.go).
	goalLeadTurnRunFailed      = "run_failed"
	goalLeadTurnFunctionResult = "function_result"
	goalLeadTurnAgentMessage   = "agent_message"
)

var errPulseResultIneligible = errors.New("Pulse result continuation is no longer eligible")

// Pulse permissions are a turn lease. Serialize its normal turns before
// taking that lease, including completion turns queued behind owner input.
var goalLeadTurnLocks sync.Map

func lockGoalLeadTurn(ctx context.Context, workspacePath string) (func(), error) {
	value, _ := goalLeadTurnLocks.LoadOrStore(workspacePath, make(chan struct{}, 1))
	lane := value.(chan struct{})
	select {
	case lane <- struct{}{}:
		return func() { <-lane }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

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
	// Pulse on means the Pulse agent owns the workflow; Pulse off means the owner
	// manages it (owner, 2026-10-08). A goal metric is not required: without
	// one, setting it up is Pulse's first job.
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest == nil || !manifest.PulseEnabled() {
		return false
	}
	return workflowHasSoul(ctx, workspacePath)
}

// workflowHasSoul reports whether the workflow has written its goal in soul.md.
func workflowHasSoul(ctx context.Context, workspacePath string) bool {
	_, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(workspacePath, "/")+"/soul/soul.md")
	return err == nil && exists
}

// workflowRunSetupView is what runs for the workflow whoever manages it: its
// schedules with their after-run options, and the manual-run options. The
// Pulse tab shows it, most of all when Pulse is off.
func workflowRunSetupView(ctx context.Context, workspacePath string) map[string]interface{} {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest == nil {
		return nil
	}
	schedules := []map[string]interface{}{}
	for _, schedule := range manifest.Schedules {
		kind := strings.TrimSpace(schedule.ScheduleType)
		if kind == "" {
			kind = "cron"
		}
		schedules = append(schedules, map[string]interface{}{
			"name": schedule.Name, "type": kind, "enabled": schedule.Enabled,
			"cron_expression": schedule.CronExpression, "timezone": schedule.Timezone,
			"after_run": manifest.EffectiveAfterRun(schedule),
		})
	}
	return map[string]interface{}{
		"schedules":        schedules,
		"manual_after_run": manifest.EffectiveManualAfterRun(),
		"workflow_review":  LatestWorkflowReview(workspacePath),
	}
}

// goalLeadTurn is one turn of the Pulse conversation.
type goalLeadTurn struct {
	Kind string
	// A result belongs to the conversation that made the call, never a
	// newer generation created while its target was working.
	ExpectedSessionID string
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
}

// goalLeadOwnerDirectionRules: what Pulse does with the owner's own words.
const goalLeadOwnerDirectionRules = `When the owner asks how the goal is doing or why you did something, answer from this conversation, goal memory, the decision log and your recorded checks. Direction that should last goes to goal memory (record_pulse_goal_memory, source owner_answer, in their words). Time-boxed direction becomes a proposed focus area (record_pulse_focus_area action=propose, with an end date and its own check), which they confirm with one click in the Pulse tab. A change to the goal itself is a proposed soul.md edit in your reply: never edit soul.md. Say in your reply what you recorded or proposed.`

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
	return fmt.Sprintf(`PULSE. You are the %s Pulse: the platform's persistent owner of this workflow's goal (workspace_path=%q). This is your one continuing conversation for the goal. Automatic turns (the goal check, Goal Work, failed runs) and messages from the workflow's Builder chat and steps arrive here.

No person reads this conversation. There is no human here to ask: never end a turn with a question to a person or wait for one. Your colleague is the Builder chat; the owner talks to the Builder chat, and it brings you what the owner says. These instructions are the same for every workflow; nobody edits them.

You are the expert on this goal: measurement, growth and funnels, experiments, debugging runs, costs, and plan design (steps, routes, schedules). Act like one. Measurement comes first: until the goal is measured properly, getting it measured is your top job. Use your skills: before you judge something, load the skill for it and follow its method. Form your own view from the evidence and explain what should happen and why. Work with Builder as a colleague on measurement: you know the goal and its evidence; Builder knows implementation and source constraints. Agree the best meaningful metric, formula, source method, unit, scope, window, cadence and freshness. Builder implements recording through ordinary workflow steps; you both verify the source-backed DB readings and history. Ask for implementation or repair with your evidence and what done looks like, discuss its reply, then check the result.

How you work:
- The goal is soul/soul.md: read it, never edit it; propose an edit to the owner when the goal should change. Your memory is memory/goal.md (record_pulse_goal_memory, one dated line with its source); soul.md wins on any conflict.
- You read; the Builder chat acts. Your own tools read the workflow and keep your records (goal checks, memory, recommendations, focus areas, decisions, notifications). To run or change anything, ask the Builder chat with ask_builder, even with full autonomy. Your permission levels say what the Builder may do for you without the owner. When something important needs the owner (beyond your levels, a goal change, a real trade-off), ask the Builder chat to raise one clear decision for the owner with the options and your recommendation; it tells you the decision id, and you attach your recommendation with record_pulse_recommendation. You recommend; only the owner decides (you cannot answer decisions). Say you do not know the owner's preference instead of guessing it.
- Skills: your own pack, read_skill(skills=[{"name":"pulse","path":"references/<skill>.md"}]); its index lists each skill and when to use it. Load one when a turn needs it: %s.
- You are this workflow's only reviewer: there are no separate Technical or Architecture reviews. When a failed run or step blocks or threatens the goal, diagnose it (the inspect skill) and ask the Builder chat to debug and fix it with your evidence. A failed run wakes you once for a short turn; your goal check reads run_health. When your checks raise a structural question, use the architecture skill.\n- No goal metric yet: work out from soul.md what to measure and ask the Builder chat to set it up (the measure skill); until then the goal is not measured.
- The workflow's Builder chat edits the workflow; you own the goal. Work with it as a conversation, not one-off messages: ask what changed and why or what the owner decided, ask it to make a change, answer its questions, and follow up on its reply (did it work, what next) until the item is done or clearly blocked. It works within the same permission levels as you, and either agent explicitly chooses whether and when to send a reply with send_message(inbox_id=<reply address>). Final chat text is never forwarded. Read incoming messages with read_agent_messages and schedule an explicit self-wakeup when you want to follow up later. Record what matters in goal memory (source builder_answer). Workflow Review checks plan changes before the next run.
- Focus areas: propose them with record_pulse_focus_area (at most three active, each with an end date and its own check); the owner confirms with one click. Track them on each goal check and close them with a lesson.
- Keep replies short and plain: what you did, what you need, why.`, label, workspacePath, strings.Join(agentworksproduct.PulseSkills(), ", "))
}

// goalLeadSystemSection is the Pulse conversation's standing instructions,
// added to its system prompt on every turn (the workflow phase prompt in
// server.go) and never repeated in its messages: the charter, how it talks
// with the owner and the workflow's chats, and its current permission levels.
// Its hash joins the chat policy fingerprint, so a change, for example to
// pulse.autonomy, relaunches the retained CLI on the same conversation with
// the new prompt, like other agents.
func goalLeadSystemSection(ctx context.Context, workspacePath string) string {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return ""
	}
	label := workspacePath
	if manifest, found, err := ReadWorkflowManifest(ctx, workspacePath); err == nil && found && manifest != nil {
		label = firstNonEmptyTrimmed(manifest.Label, workspacePath)
	}
	perms, _ := goalWorkAutonomy(ctx, workspacePath)
	autonomyText := pulseLevelsText(perms)
	return "## Pulse\n\n" + goalLeadCharter(label, workspacePath) +
		"\n\nWho is talking to you: a message that starts with a sender (\"the Builder chat (<their name>): ...\", \"a step of this workflow: ...\") comes from that chat. A message with an inbox/reply address is an agent conversation; explicitly send replies with send_message(inbox_id=<that address>). Your final chat answer is not forwarded. Replies are optional. A message headed \"PULSE TURN:\" is an automatic one (goal check, Goal Work, a failed run). Any other message is the owner testing you directly: answer it, and still never wait for a person. " + goalLeadOwnerDirectionRules +
		"\n\n" + autonomyText + "\n" + pulsePaceText(workflowPulsePace(ctx, workspacePath))
}

func goalLeadSystemSectionKey(section string) string {
	if section == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(section))
	return hex.EncodeToString(sum[:])
}

// goalLeadTurnQuery is one turn's message. The standing instructions are in
// the system prompt: the owner's message is sent as typed, a chat's message
// with its sender, and an automatic turn with one header line and its task.
// overrideLevels is set only when the turn holds other permission levels than
// the workflow's (a failed-run turn).
func goalLeadTurnQuery(turn goalLeadTurn, overrideLevels string, now time.Time) string {
	date := now.UTC().Format("2006-01-02")
	levels := ""
	if strings.TrimSpace(overrideLevels) != "" {
		levels = "\n\nThis turn only: " + strings.TrimSpace(overrideLevels)
	}
	switch turn.Kind {
	case goalLeadTurnCheck:
		return fmt.Sprintf("PULSE TURN: goal check and Goal Work, %s.\n\n%s%s", date, turn.Body, levels)
	case goalLeadTurnGoalWork:
		return fmt.Sprintf("PULSE TURN: Goal Work, %s.\n\n%s%s", date, turn.Body, levels)
	case goalLeadTurnRunFailed:
		return fmt.Sprintf("PULSE TURN: a run of this workflow failed, %s.\n\n%s%s", date, turn.Body, levels)
	case goalLeadTurnFunctionResult:
		return fmt.Sprintf("PULSE TURN: a requested result arrived, %s.\n\n%s\n\nReconcile this result with the associated Goal Work, latest goal check and goal memory using the existing record tools. Correct stale running claims and record actual evidence, blockers and pending verification. A completed chat reply saying work started does not mean that background work finished. Preserve intentional next-check times and measurement windows unless the evidence requires a change. Do not launch duplicate work or another reviewer; owner approval is still required by current permissions.%s", date, turn.Body, levels)
	case goalLeadTurnAgentMessage:
		return strings.TrimSpace(turn.Body) + levels
	case goalLeadTurnAsk:
		return firstNonEmptyTrimmed(turn.From, "a chat of this workflow") + ": " + strings.TrimSpace(turn.Body) + levels
	default:
		// The owner talking to Pulse (the Pulse tab or Slack): as typed.
		return strings.TrimSpace(turn.Body) + levels
	}
}

// runGoalLeadTurn runs one turn in the workflow's Pulse conversation and
// returns the reply and the conversation's session id. The turn runs as the
// workflow's execution owner, holds the session's tools to the autonomy
// levels, and is logged for the Pulse tab.
func (api *StreamingAPI) runGoalLeadTurn(ctx context.Context, workspacePath string, turn goalLeadTurn) (string, string, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	unlock, err := lockGoalLeadTurn(ctx, workspacePath)
	if err != nil {
		return "", "", err
	}
	defer unlock()
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return "", "", err
	}
	if !found || manifest == nil {
		return "", "", fmt.Errorf("workflow %s not found", workspacePath)
	}
	if (turn.Kind == goalLeadTurnFunctionResult || turn.Kind == goalLeadTurnAgentMessage) && !manifest.PulseEnabled() {
		return "", "", errPulseResultIneligible
	}
	now := goalLeadNow().UTC()
	conv, err := ensureGoalLeadConversation(ctx, workspacePath, firstNonEmptyTrimmed(manifest.ID, workspacePath), now, turn.Rotate, api.conversationTurnOccupied)
	if err != nil {
		return "", "", fmt.Errorf("pulse conversation: %w", err)
	}
	sessionID := conv.SessionID
	if turn.Kind == goalLeadTurnFunctionResult || turn.Kind == goalLeadTurnAgentMessage {
		if sessionID != turn.ExpectedSessionID || api.autoNotificationSessionUnreachable(sessionID) {
			return "", sessionID, errPulseResultIneligible
		}
		if api.conversationTurnOccupied(sessionID) {
			return "", sessionID, fmt.Errorf("Pulse conversation is busy; retain its result for retry")
		}
	}
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
	if turn.Kind == goalLeadTurnFunctionResult || turn.Kind == goalLeadTurnAgentMessage {
		reqMap["is_auto_notification"] = true
	}
	reqMap["session_title"] = label + " Pulse"
	reqMap["triggered_by_label"] = "Pulse"
	if turn.Kind == goalLeadTurnOwner || turn.Kind == goalLeadTurnSlack || turn.Kind == goalLeadTurnAsk {
		reqMap["triggered_by_label"] = "Pulse: from " + firstNonEmptyTrimmed(turn.From, "the owner")
	}
	if conv.Turns > 0 || api.workflowAskSessionExists(sessionID, workspacePath) {
		reqMap["restored_conversation_session_id"] = sessionID
	}

	perms, _ := goalWorkAutonomy(ctx, workspacePath)
	overrideLevels := ""
	if turn.Perms != nil {
		perms = *turn.Perms
		overrideLevels = pulseLevelsText(perms)
	}
	reqMap["query"] = goalLeadTurnQuery(turn, overrideLevels, now)

	switch turn.Kind {
	case goalLeadTurnOwner, goalLeadTurnSlack, goalLeadTurnAsk, goalLeadTurnFunctionResult, goalLeadTurnAgentMessage:
		if !turn.Logged {
			_ = appendGoalLeadMessage(ctx, workspacePath, GoalLeadMessage{Role: turn.Kind, Source: turn.From, Text: turn.Body, SessionID: sessionID})
		}
	}

	// A new turn is a deliberate start: a Stop on an earlier turn must not
	// leave the conversation unable to resume (PLAT-130 guards continuations
	// of the stopped turn, not the next one).
	if turn.Kind != goalLeadTurnFunctionResult && turn.Kind != goalLeadTurnAgentMessage {
		api.clearSessionStopped(sessionID)
		mcpagent.ClearHTTPSessionStopped(sessionID)
	}
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

// handlePostGoalLeadMessage takes the owner's message from the Pulse tab's
// "Talk to Pulse" box and runs it as a turn in the Pulse conversation. It
// returns at once; the reply appears in the "<workflow> Pulse" chat tab.
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
		http.Error(w, "this workflow's Pulse is off or has no goal yet (Pulse on and soul/soul.md); set it up in the Builder chat first", http.StatusConflict)
		return
	}
	if err := api.sendGoalLeadOwnerMessage(r.Context(), workspacePath, message); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// sendGoalLeadOwnerMessage logs the owner's message in the Pulse conversation
// and runs it as a Pulse turn in the background (the Pulse tab's direct box
// and the MCP builder_pulse_chat tool).
func (api *StreamingAPI) sendGoalLeadOwnerMessage(ctx context.Context, workspacePath, message string) error {
	from := "the owner"
	if claims := GetUserFromContext(ctx); claims != nil {
		from = firstNonEmptyTrimmed(claims.Username, claims.Email, claims.UserID, from)
	}
	if err := appendGoalLeadMessage(ctx, workspacePath, GoalLeadMessage{Role: goalLeadTurnOwner, Source: from, Text: message}); err != nil {
		return err
	}
	go func() {
		turnCtx, cancel := context.WithTimeout(context.Background(), goalLeadTurnHardCap)
		defer cancel()
		if _, _, err := api.runGoalLeadTurn(turnCtx, workspacePath, goalLeadTurn{Kind: goalLeadTurnOwner, From: from, Body: message, Logged: true}); err != nil {
			log.Printf("[PULSE] owner message turn for %s failed: %v", workspacePath, err)
		}
	}()
	return nil
}

// goalLeadGoalWorkStep is the full Pulse's strategic_review turn as the Goal
// Lead runs it: the same module contract and receipt, with the short Goal
// Work skill instead of the reviewer contract (strategy-auditor.md, which the
// /run-goal-work command and workflows without a goal keep using).
func goalLeadGoalWorkStep(pulseRunID string) pulseLifecycleStep {
	label, _, contract := pulseModuleReviewParts(pulseModuleStrategicReview)
	return pulseLifecycleStep{label: label, query: fmt.Sprintf(`PULSE MODULE REVIEW. pulse_run_id=%q. This turn owns ONLY module=%q (Goal Work). Read the durable Gate worklist with get_pulse_state(view="module", pulse_run_id=<this id>). If this module is not due or already has a terminal result, say so and end this turn.
Otherwise load read_skill(skills=[{"name":"pulse","path":"references/goal-lead-work.md"}]) and do it yourself in this turn. Read get_pulse_state(view="review_notes", module=%q) once for prior reasoning. %s
%sDo not render a dashboard, back up, publish or notify.`, pulseRunID, pulseModuleStrategicReview, pulseModuleStrategicReview, contract, pulseReviewerRecordRules)}
}
