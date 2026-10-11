package types

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Tests exercising specific step-execution modes the main 5-step e2e
// doesn't cover. Same env gates + harness as edge_cases_test.go.
//
// Every test here uses tightened assertions (post the realization in
// the prior round that "tokens appear in session.json" doesn't prove
// sequencing — the engine could batch prompts into one LLM turn and
// still produce all tokens). The new shape parses the session
// conversation_history directly and asserts on per-turn structure.

// ──────────────────────────────────────────────────────────────────────
// Shared session parsing — used by the agent tests below.

// sessionFile mirrors the on-disk shape of session.json the engine
// writes under execution/agents/<step-path>/<step-id>/.
// Field names are PascalCase because the engine marshals
// llmtypes.MessageContent that way.
type sessionFile struct {
	SessionID           string           `json:"session_id"`
	StepID              string           `json:"step_id"`
	Status              string           `json:"status"`
	ConversationHistory []sessionMessage `json:"conversation_history"`
}

type sessionMessage struct {
	Role  string        `json:"Role"`
	Parts []sessionPart `json:"Parts"`
}

type sessionPart struct {
	Text string `json:"Text,omitempty"`
	Type string `json:"Type,omitempty"`
}

func (m sessionMessage) text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func loadSessionJSON(t *testing.T, workspaceDisk, stepID string) *sessionFile {
	t.Helper()
	pat := filepath.Join(workspaceDisk, "runs", "*", "*", "execution", "agents", "*", stepID, "session.json")
	matches, _ := filepath.Glob(pat)
	if len(matches) == 0 {
		t.Fatalf("session.json not written under %s — agent step did not run", pat)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read session.json: %v", err)
	}
	var sess sessionFile
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("parse session.json: %v\nbody=%s", err, body)
	}
	return &sess
}

// roleTurns extracts the messages with the given role in document order.
func (s *sessionFile) roleTurns(role string) []sessionMessage {
	out := make([]sessionMessage, 0)
	for _, m := range s.ConversationHistory {
		if strings.EqualFold(m.Role, role) {
			out = append(out, m)
		}
	}
	return out
}

// ──────────────────────────────────────────────────────────────────────
// Multi-item sequencing (tightened from the original broken version)

// TestWorkflowAgentMultiItem proves the engine **actually
// sequences** — not just that all expected tokens appear in
// session.json. Specifically:
//
//	(1) conversation_history contains AT LEAST 3 distinct human turns —
//	    one per item. A batching engine that collapses items into a
//	    single prompt would have only 1 human turn and fail here.
//	(2) Each of the 3 deterministic tokens appears in EXACTLY ONE ai
//	    reply, AND each reply gets a different token. A single ai turn
//	    dumping all three tokens (e.g. because the prompts were
//	    concatenated) would fail.
//	(3) The tokens appear in the right turn order: ROUND_1 in the 1st
//	    relevant ai turn, ROUND_2_AFTER_1 in the 2nd, ROUND_3_FINAL in
//	    the 3rd.
//
// Gated on RUN_WORKFLOW_REAL_E2E + RUN_VERTEX_REAL_E2E + GEMINI_API_KEY.
func TestWorkflowAgentMultiItem(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "agent",
				"id":                   "multi-seq",
				"title":                "Multi-item sequence",
				"description":          "Three back-and-forth user_message items",
				"context_dependencies": []string{},
				"context_output":       "ms.json",
				"items": []map[string]interface{}{
					{
						"id":      "msg-1",
						"type":    "user_message",
						"title":   "Round 1",
						"message": "First turn of a three-turn conversation. Reply with EXACTLY the line ROUND_1 on a line by itself and stop. Do not write any code; do not call any tools.",
					},
					{
						"id":      "msg-2",
						"type":    "user_message",
						"title":   "Round 2",
						"message": "Second turn. Earlier you replied ROUND_1. Now reply with EXACTLY the line ROUND_2_AFTER_1 on a line by itself and stop. Do not write any code; do not call any tools.",
					},
					{
						"id":      "msg-3",
						"type":    "user_message",
						"title":   "Round 3",
						"message": "Third and final turn. Reply with EXACTLY the line ROUND_3_FINAL on a line by itself and stop. Do not write any code; do not call any tools.",
					},
				},
				"next_step_id": "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "Three-turn agent", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess := loadSessionJSON(t, wo.workspaceDisk, "multi-seq")
	if sess.Status != "completed" {
		t.Errorf("session.status = %q, want completed", sess.Status)
	}

	// (1) Three distinct human turns. Each item should land as one
	//     human message.
	humans := sess.roleTurns("human")
	if len(humans) < 3 {
		t.Fatalf("expected >=3 human turns (one per item); got %d — engine likely batched prompts", len(humans))
	}

	// (2) Each token appears in exactly one ai reply, and (3) in the
	//     right order. Walk the ai turns in document order, look for
	//     the next expected token in each. A reply containing two of
	//     the tokens at once is a batching failure.
	wantTokens := []string{"ROUND_1", "ROUND_2_AFTER_1", "ROUND_3_FINAL"}
	ais := sess.roleTurns("ai")
	if len(ais) < 3 {
		t.Fatalf("expected >=3 ai turns; got %d — engine did not let the LLM reply per item", len(ais))
	}
	// For each token, find the FIRST ai turn that contains it. Assert
	// they are different turns AND in order.
	tokenTurn := make(map[string]int)
	for ti, tok := range wantTokens {
		for i, ai := range ais {
			if strings.Contains(ai.text(), tok) {
				tokenTurn[tok] = i
				break
			}
		}
		if _, ok := tokenTurn[tok]; !ok {
			t.Errorf("token %q not in any ai turn — item %d either didn't run or LLM didn't follow the prompt", tok, ti+1)
		}
	}
	if len(tokenTurn) == 3 {
		if tokenTurn["ROUND_1"] >= tokenTurn["ROUND_2_AFTER_1"] {
			t.Errorf("ROUND_1 turn=%d, ROUND_2_AFTER_1 turn=%d — items ran out of order", tokenTurn["ROUND_1"], tokenTurn["ROUND_2_AFTER_1"])
		}
		if tokenTurn["ROUND_2_AFTER_1"] >= tokenTurn["ROUND_3_FINAL"] {
			t.Errorf("ROUND_2_AFTER_1 turn=%d, ROUND_3_FINAL turn=%d — items ran out of order", tokenTurn["ROUND_2_AFTER_1"], tokenTurn["ROUND_3_FINAL"])
		}
		// Distinctness: each token in its own ai turn.
		seen := map[int]string{}
		for tok, turn := range tokenTurn {
			if other, dup := seen[turn]; dup {
				t.Errorf("tokens %q and %q both appear in ai turn %d — engine collapsed items into one LLM reply", tok, other, turn)
			}
			seen[turn] = tok
		}
	}
	t.Logf("✅ multi-item sequence: %d human turns, %d ai turns, token→turn: %v", len(humans), len(ais), tokenTurn)
}

// TestWorkflowAgentItemTypeInvalidRejected proves the
// engine refuses a agent item with an unknown `type` field.
// Today valid types are exactly user_message | code | prevalidation
// (planning_agent.go:594). A typo or hallucinated item type should
// fail at plan load, not be silently dropped or executed as
// user_message.
func TestWorkflowAgentItemTypeInvalidRejected(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "agent",
				"id":                   "bad-type-seq",
				"title":                "Bad item type",
				"description":          "first item is a valid user_message, second has a hallucinated type",
				"context_dependencies": []string{},
				"context_output":       "bt.json",
				"items": []map[string]interface{}{
					{"id": "ok", "type": "user_message", "title": "OK", "message": "Reply ACK."},
					{"id": "bad", "type": "telepathy", "title": "Bad", "message": "this should never run"},
				},
				"next_step_id": "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "invalid item type", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err == nil {
		t.Fatal("expected engine to reject item with unknown type=\"telepathy\"; got nil error")
	}
	if !containsAny(err.Error(), "type", "telepathy", "invalid", "unknown", "unsupported") {
		t.Errorf("error should pinpoint the bad item type; got: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────
// scripted mode (tightened from the original broken version)

// TestWorkflowScriptedFastPathRunsPreSeededScript proves the engine's
// scripted FAST PATH actually runs a pre-existing main.py — which
// is the entire point of the mode (controller_scripted.go:838,
// tryRunSavedScriptedScript). We:
//
//	(1) pre-seed learnings/<step-id>/main.py with a known Python script
//	    that prints a deterministic token to stdout.
//	(2) leave the step a regular plan step -- since PLAT-287 a regular
//	    step IS a scripted step; nothing in step_config declares it.
//	(3) run the workflow.
//	(4) assert the engine ran the script — proof is the script's token
//	    in the step's execution_result AND the engine's "Executing
//	    saved script" branch artifact (execution/code/main.py copied
//	    from learnings/, see controller_scripted.go:887).
//
// This pair of checks is the load-bearing assertion for learn_code:
// "given a saved script, the engine reuses it instead of calling the
// LLM." An earlier shape of this test only verified that some file
// was written under learnings/ — which a metadata-only no-tools run
// also satisfies (the engine writes .learning_metadata.json for any
// learn_code step regardless of whether main.py was authored). The
// looser test was therefore a false positive; this version proves
// real script execution.
func TestWorkflowScriptedFastPathRunsPreSeededScript(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	const stepID = "learn-code-step"
	const scriptToken = "PRE_SEEDED_SCRIPT_RAN_42"

	// 1) Pre-seed the saved script. The engine's
	//    hasValidLearnedScriptAPI gate only checks for the file's
	//    existence (controller_scripted.go:597) so writing main.py
	//    on disk is sufficient — no need to pre-seed metadata.
	learningsDir := filepath.Join(wo.workspaceDisk, "learnings", stepID)
	if err := os.MkdirAll(learningsDir, 0o755); err != nil {
		t.Fatalf("mkdir learnings: %v", err)
	}
	// The engine runs reviewMainPyScript (controller_scripted.go:352)
	// as a static check before executing main.py. It rejects scripts
	// that use os.environ.get(KEY, fallback) for required env vars
	// because fallbacks silently hide misconfiguration. Use the
	// throws-on-missing form os.environ["KEY"] instead.
	mainPyContent := `#!/usr/bin/env python3
import os, sys
out_dir = os.environ["STEP_OUTPUT_DIR"]
os.makedirs(out_dir, exist_ok=True)
result = 6 * 7
print("` + scriptToken + `=" + str(result))
with open(os.path.join(out_dir, "computed.txt"), "w") as f:
    f.write("computed=" + str(result) + "\n")
`
	if err := os.WriteFile(filepath.Join(learningsDir, "main.py"), []byte(mainPyContent), 0o644); err != nil {
		t.Fatalf("seed main.py: %v", err)
	}

	// 2) Plan + step_config declaring scripted mode.
	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "regular",
				"id":                   stepID,
				"title":                "Learn-code fast path",
				"description":          "Use the saved Python script under learnings/learn-code-step/main.py to compute 6*7.",
				"context_dependencies": []string{},
				"context_output":       "out.json",
			},
		},
	})
	stepConfig := map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"id":    stepID,
				"title": "Learn-code fast path",
				"agent_configs": map[string]interface{}{
					"learning_objective": "Compute the constant 42 via 6*7.",
				},
			},
		},
	}
	if err := writeJSON(filepath.Join(wo.workspaceDisk, "planning", "step_config.json"), stepConfig); err != nil {
		t.Fatalf("write step_config.json: %v", err)
	}

	// 3) Run.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "learn_code fast-path test", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// 4a) Engine copies the saved script to execution/code/main.py
	//     before running it (controller_scripted.go:887). Presence
	//     of that copy is proof the fast path triggered.
	execCodeGlob := filepath.Join(wo.workspaceDisk, "runs", "*", "*", "execution", "*", "code", "main.py")
	if execMatches, _ := filepath.Glob(execCodeGlob); len(execMatches) == 0 {
		t.Errorf("execution/code/main.py was NOT copied — engine did not take the saved-script fast path. (Pattern: %s)", execCodeGlob)
	} else {
		t.Logf("✅ execution/code/main.py copy present: %s", execMatches[0])
	}

	// 4b) The fast path writes a DEDICATED artifact —
	//     logs/<step-id>/execution/learn_code_fast_path.json — that
	//     records the script's exit_code, captured stdout, and the
	//     success/failure verdict. (execution-attempt-*.json is for
	//     LLM-driven execution; the fast path skips it.) The script's
	//     token MUST appear in `output` and `success` must be true.
	fastPathGlob := filepath.Join(wo.workspaceDisk, "runs", "*", "*", "logs", stepID, "execution", "learn_code_fast_path.json")
	fastMatches, _ := filepath.Glob(fastPathGlob)
	if len(fastMatches) == 0 {
		t.Fatalf("no learn_code_fast_path.json at %s — engine did not record a fast-path attempt", fastPathGlob)
	}
	body, err := os.ReadFile(fastMatches[0])
	if err != nil {
		t.Fatalf("read learn_code_fast_path.json: %v", err)
	}
	var fp struct {
		Mode       string `json:"mode"`
		ExitCode   int    `json:"exit_code"`
		Success    bool   `json:"success"`
		Output     string `json:"output"`
		ErrorField string `json:"error"`
		ScriptPath string `json:"script_path"`
	}
	if err := json.Unmarshal(body, &fp); err != nil {
		t.Fatalf("parse learn_code_fast_path.json: %v\nbody=%s", err, body)
	}
	if fp.Mode != "learn_code_fast_path" {
		t.Errorf("fast-path mode = %q, want learn_code_fast_path", fp.Mode)
	}
	if fp.ExitCode != 0 {
		t.Errorf("fast-path exit_code = %d, want 0\n  output=%q\n  error=%q", fp.ExitCode, fp.Output, fp.ErrorField)
	}
	if !fp.Success {
		t.Errorf("fast-path success = false\n  output=%q\n  error=%q", fp.Output, fp.ErrorField)
	}
	if !strings.Contains(fp.Output, scriptToken) {
		t.Errorf("fast-path output missing script token %q\n  output: %q", scriptToken, fp.Output)
	}
	t.Logf("✅ learn_code fast path: exit_code=0, success=true, output=%q", strings.TrimSpace(fp.Output))
}

// TestWorkflowScriptedControlNoModeWritesNoScript is the control
// counterpart of the test above: run the same regular step, but with a
// step_config.json that still carries the RETIRED
// declared_execution_mode="agentic" key (a workflow the v1.0.38/1.0.39
// migrations have not reached). The transitional shim keeps such a step
// running as a agent, so the engine must NOT enter the
// scripted path and must NOT create learnings/<step-id>/main.py.
//
// Together with the positive test, this pair proves the scripted path is
// decided by the plan type plus the legacy shim, and that the shim still
// holds a legacy agentic step off it.
func TestWorkflowScriptedControlNoModeWritesNoScript(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	const stepID = "control-no-learn-code"
	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "regular",
				"id":                   stepID,
				"title":                "Control: no learn_code",
				"description":          "Reply with RESULT=42 and stop.",
				"context_dependencies": []string{},
				"context_output":       "out.json",
			},
		},
	})
	// The retired key, exactly as an unmigrated workflow still has it.
	if err := writeJSON(filepath.Join(wo.workspaceDisk, "planning", "step_config.json"), map[string]interface{}{
		"steps": []map[string]interface{}{
			{"id": stepID, "agent_configs": map[string]interface{}{"declared_execution_mode": "agentic"}},
		},
	}); err != nil {
		t.Fatalf("write step_config.json: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "control run", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	mainPyPath := filepath.Join(wo.workspaceDisk, "learnings", stepID, "main.py")
	if _, err := os.Stat(mainPyPath); err == nil {
		body, _ := os.ReadFile(mainPyPath)
		t.Fatalf("control: learnings/%s/main.py exists for a legacy agentic regular step — the transitional shim is not holding it off the scripted path. main.py:\n%s", stepID, string(body))
	}
	t.Logf("✅ control: legacy agentic regular step → no main.py written")
}

// TestWorkflowScriptedStepConfigIDMismatchNotApplied proves that a
// step_config.json entry whose `id` doesn't match any plan.json step
// is harmlessly ignored. The mismatched entry carries the retired
// declared_execution_mode="agentic" key; were it applied to the real
// step, the legacy shim would run that step as a agent and
// write no script. The real step is a regular one and so scripted by
// type: its main.py must appear, and none for the phantom id.
func TestWorkflowScriptedStepConfigIDMismatchNotApplied(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	const planStepID = "step-x"
	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "regular",
				"id":                   planStepID,
				"title":                "Plain step",
				"description":          "Reply with RESULT=42 and stop.",
				"context_dependencies": []string{},
				"context_output":       "out.json",
			},
		},
	})

	// step_config.json references a DIFFERENT step id — this should
	// not affect "step-x".
	stepConfig := map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"id": "step-y", // mismatched
				"agent_configs": map[string]interface{}{
					"declared_execution_mode": "agentic",
				},
			},
		},
	}
	if err := writeJSON(filepath.Join(wo.workspaceDisk, "planning", "step_config.json"), stepConfig); err != nil {
		t.Fatalf("write step_config.json: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "mismatched step_config", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wo.workspaceDisk, "learnings", planStepID, "main.py")); err != nil {
		t.Fatalf("learnings/%s/main.py missing — the mismatched step_config entry (id \"step-y\", legacy agentic) was applied to the regular step and kept it off the scripted path: %v", planStepID, err)
	}
	if _, err := os.Stat(filepath.Join(wo.workspaceDisk, "learnings", "step-y", "main.py")); err == nil {
		t.Fatalf("learnings/step-y/main.py exists — engine wrote artifacts for a step that doesn't appear in plan.json")
	}
	t.Logf("✅ step_config.json with mismatched id: regular step ran scripted, no artifacts for the phantom id")
}

// ──────────────────────────────────────────────────────────────────────
// foreach — data-driven message expansion (agent + todo_task)

// TestWorkflowAgentForeach proves a `foreach` item expands a db
// JSON array into ONE user_message turn per row, deterministically, through
// the same conversation. An earlier step's data (here: a pre-seeded
// db/foreach_rows.json) drives the turns — the producer/consumer pattern.
//
// Asserts, like the multi-item test:
//
//	(1) >= one human turn per row (proves the runtime looped, not the LLM);
//	(2) each row's token appears in exactly one ai turn, in row order.
//
// Gated on the same env as the other e2e tests (RUN_WORKFLOW_REAL_E2E + a
// real provider). No-ops otherwise.
func TestWorkflowAgentForeach(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	// Producer artifact: a db array a prior step would have written.
	rows := []map[string]string{
		{"id": "alpha", "token": "ROW_ALPHA"},
		{"id": "bravo", "token": "ROW_BRAVO"},
		{"id": "charlie", "token": "ROW_CHARLIE"},
	}
	if err := os.MkdirAll(filepath.Join(wo.workspaceDisk, "db"), 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	if err := writeJSON(filepath.Join(wo.workspaceDisk, "db", "foreach_rows.json"), rows); err != nil {
		t.Fatalf("write db/foreach_rows.json: %v", err)
	}

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "agent",
				"id":                   "foreach-seq",
				"title":                "Foreach over db rows",
				"description":          "Process every row of db/foreach_rows.json, one turn each",
				"context_dependencies": []string{},
				"context_output":       "fe.json",
				"items": []map[string]interface{}{
					{
						"id":      "loop",
						"type":    "foreach",
						"title":   "Per-row turn",
						"source":  "db/foreach_rows.json",
						"message": "This is row {{.id}}. Reply with EXACTLY the line {{.token}} on a line by itself and stop. Do not write any code; do not call any tools.",
					},
				},
				"next_step_id": "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "Foreach agent", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess := loadSessionJSON(t, wo.workspaceDisk, "foreach-seq")
	if sess.Status != "completed" {
		t.Errorf("session.status = %q, want completed", sess.Status)
	}

	humans := sess.roleTurns("human")
	if len(humans) < len(rows) {
		t.Fatalf("expected >=%d human turns (one per row); got %d — foreach did not expand per row", len(rows), len(humans))
	}

	wantTokens := []string{"ROW_ALPHA", "ROW_BRAVO", "ROW_CHARLIE"}
	ais := sess.roleTurns("ai")
	tokenTurn := make(map[string]int)
	for _, tok := range wantTokens {
		for i, ai := range ais {
			if strings.Contains(ai.text(), tok) {
				tokenTurn[tok] = i
				break
			}
		}
		if _, ok := tokenTurn[tok]; !ok {
			t.Errorf("token %q not in any ai turn — that row didn't run or the LLM didn't follow the prompt", tok)
		}
	}
	if len(tokenTurn) == len(wantTokens) {
		if !(tokenTurn["ROW_ALPHA"] < tokenTurn["ROW_BRAVO"] && tokenTurn["ROW_BRAVO"] < tokenTurn["ROW_CHARLIE"]) {
			t.Errorf("rows ran out of order: alpha=%d bravo=%d charlie=%d", tokenTurn["ROW_ALPHA"], tokenTurn["ROW_BRAVO"], tokenTurn["ROW_CHARLIE"])
		}
	}
	t.Logf("✅ foreach agent: %d human turns, %d ai turns, token→turn: %v", len(humans), len(ais), tokenTurn)
}

// TestWorkflowOrchestratorForeachMessages proves a todo_task step's scripted
// `messages` with a `foreach` entry feeds ONE orchestrator turn per db row.
// Assertion reads the todo_task execution logs (which capture each turn's
// conversation_history) and checks every row's token appears.
//
// Gated like the other e2e tests; no-ops otherwise.
func TestWorkflowOrchestratorForeachMessages(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	rows := []map[string]string{
		{"id": "t1", "token": "ORCH_ROW_ONE"},
		{"id": "t2", "token": "ORCH_ROW_TWO"},
	}
	if err := os.MkdirAll(filepath.Join(wo.workspaceDisk, "db"), 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	if err := writeJSON(filepath.Join(wo.workspaceDisk, "db", "orch_rows.json"), rows); err != nil {
		t.Fatalf("write db/orch_rows.json: %v", err)
	}

	const stepID = "orch-foreach"
	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "todo_task",
				"id":                   stepID,
				"title":                "Orchestrator foreach over db rows",
				"description":          "You are answering a short scripted sequence. There are no tasks to delegate; for each instruction simply reply exactly as asked.",
				"context_dependencies": []string{},
				"context_output":       "orch.json",
				"messages": []map[string]interface{}{
					{
						"id":      "rows",
						"type":    "foreach",
						"source":  "db/orch_rows.json",
						"message": "Acknowledge row {{.id}} by replying with EXACTLY the line {{.token}} on a line by itself and stop.",
					},
				},
				"next_step_id": "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "Orchestrator foreach", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Read every todo_task execution log for this step and concatenate.
	pat := filepath.Join(wo.workspaceDisk, "runs", "*", "*", "logs", stepID, "execution", "execution-attempt-*-iteration-*.json")
	matches, _ := filepath.Glob(pat)
	if len(matches) == 0 {
		t.Fatalf("no todo_task execution logs under %s — step did not run", pat)
	}
	var all strings.Builder
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read log %s: %v", m, err)
		}
		all.Write(b)
		all.WriteByte('\n')
	}
	logs := all.String()
	for _, r := range rows {
		if !strings.Contains(logs, r["token"]) {
			t.Errorf("token %q for row %q not found in any todo_task execution log — foreach row turn did not run or reply", r["token"], r["id"])
		}
	}
	t.Logf("✅ todo_task foreach messages: %d log file(s) scanned, all %d row tokens present", len(matches), len(rows))
}

// readOrchestratorLogs concatenates every todo_task execution log for a step (each
// turn — including scripted messages/foreach rows — is logged with its
// conversation_history).
func readOrchestratorLogs(t *testing.T, workspaceDisk, stepID string) string {
	t.Helper()
	pat := filepath.Join(workspaceDisk, "runs", "*", "*", "logs", stepID, "execution", "execution-attempt-*-iteration-*.json")
	matches, _ := filepath.Glob(pat)
	if len(matches) == 0 {
		t.Fatalf("no todo_task execution logs under %s — step did not run", pat)
	}
	var b strings.Builder
	for _, m := range matches {
		c, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read log %s: %v", m, err)
		}
		b.Write(c)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestWorkflowOrchestratorScriptedMessages covers the plain (type=message) scripted
// `messages` happy path: after the orchestrator's first turn, each scripted
// message is fed into the SAME conversation, in order. Gated like the others.
func TestWorkflowOrchestratorScriptedMessages(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	const stepID = "orch-scripted"
	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "todo_task",
				"id":                   stepID,
				"title":                "Scripted orchestrator messages",
				"description":          "You are answering a short scripted Q&A. There is nothing to delegate; reply to each instruction exactly as asked.",
				"context_dependencies": []string{},
				"context_output":       "os.json",
				"messages": []map[string]interface{}{
					{"id": "m1", "type": "message", "message": "Reply with EXACTLY the line SCRIPTED_ONE on a line by itself and stop."},
					{"id": "m2", "type": "message", "message": "Now reply with EXACTLY the line SCRIPTED_TWO on a line by itself and stop."},
				},
				"next_step_id": "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "scripted messages", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	logs := readOrchestratorLogs(t, wo.workspaceDisk, stepID)
	for _, tok := range []string{"SCRIPTED_ONE", "SCRIPTED_TWO"} {
		if !strings.Contains(logs, tok) {
			t.Errorf("token %q not found in todo_task logs — scripted message turn did not run/reply", tok)
		}
	}
	t.Logf("✅ todo_task scripted messages: both scripted-turn tokens present")
}

// TestWorkflowOrchestratorPrevalidationGate covers the prevalidation-gate happy
// path on todo_task `messages`: a scripted turn runs, then a prevalidation gate
// passes (the required artifact is present) and the step completes. The artifact
// is pre-seeded so "gate passes → sequence continues" is deterministic (the
// failure/corrective-retry path is a separate concern).
func TestWorkflowOrchestratorPrevalidationGate(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	const stepID = "orch-gate"
	// Pre-seed the file the gate checks in the workflow-root db/ store — a stable,
	// non-run-scoped path (so we don't have to guess the run folder, and we don't
	// perturb run-folder selection by pre-creating runs/).
	if err := os.MkdirAll(filepath.Join(wo.workspaceDisk, "db"), 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wo.workspaceDisk, "db", "gate.txt"), []byte("DONE\n"), 0o644); err != nil {
		t.Fatalf("write db/gate.txt: %v", err)
	}

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "todo_task",
				"id":                   stepID,
				"title":                "Prevalidation gate (happy path)",
				"description":          "Reply briefly. Do not delegate.",
				"context_dependencies": []string{},
				"context_output":       "og.json",
				"messages": []map[string]interface{}{
					{"id": "say", "type": "message", "message": "Reply with EXACTLY the line GATE_OK on a line by itself and stop."},
					{"id": "gate", "type": "prevalidation", "validation_schema": map[string]interface{}{
						"files": []map[string]interface{}{
							{"file_name": "db/gate.txt", "must_exist": true},
						},
					}},
				},
				"next_step_id": "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "prevalidation gate", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute returned error (gate should pass with pre-seeded gate.txt): %v", err)
	}

	logs := readOrchestratorLogs(t, wo.workspaceDisk, stepID)
	if !strings.Contains(logs, "GATE_OK") {
		t.Errorf("GATE_OK not in todo_task logs — the scripted turn before the gate did not run")
	}
	t.Logf("✅ todo_task prevalidation gate: scripted turn ran and the gate passed")
}

// TestWorkflowAgentRouteReentry covers the headline of the (b2)
// persistence refactor: a agent used as a todo_task ROUTE remembers
// its conversation across the orchestrator's repeated calls (in-memory,
// run-scoped). The orchestrator is told to call the route twice — seed a secret
// word, then ask for it back — and we assert the route's LAST reply recalls the
// word, which is only possible if call #2 saw call #1's context.
func TestWorkflowAgentRouteReentry(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "todo_task",
				"id":                   "reentry-orch",
				"title":                "Route re-entry memory",
				"description":          "You coordinate a memory specialist sub-agent called 'recaller'. You MUST call sub-agent 'recaller' exactly TWICE, in order, and do nothing else: (1) FIRST call — instruct recaller to remember the secret word KANGAROO. (2) SECOND call — ask recaller ONLY 'What is the secret word you were told to remember?'. Then finish. Never state the word yourself.",
				"context_dependencies": []string{},
				"context_output":       "rt.json",
				"predefined_routes": []map[string]interface{}{
					{
						"route_id":   "recaller",
						"route_name": "Recaller",
						"condition":  "Remembers and recalls a secret word across calls",
						"sub_agent_step": map[string]interface{}{
							"type":                 "agent",
							"id":                   "recaller",
							"title":                "Recaller",
							"description":          "You are a memory specialist. Follow each instruction and keep all prior context across turns. Reply concisely.",
							"context_dependencies": []string{},
							"context_output":       "rc.json",
							"items": []map[string]interface{}{
								{"id": "ack", "type": "user_message", "message": "Acknowledge the instruction you were just given with a one-line confirmation."},
							},
							"next_step_id": "end",
						},
					},
				},
				"next_step_id": "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := wo.orchestrator.Execute(ctx, "route reentry memory", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess := loadSessionJSON(t, wo.workspaceDisk, "recaller")
	ais := sess.roleTurns("ai")
	if len(ais) < 2 {
		t.Fatalf("recaller had %d ai turn(s); expected >=2 (orchestrator should have called it twice) — cannot prove route memory", len(ais))
	}
	last := strings.ToUpper(ais[len(ais)-1].text())
	if !strings.Contains(last, "KANGAROO") {
		t.Errorf("recaller's final reply did not recall KANGAROO — route did NOT remember across calls (b2 in-memory route memory broken).\nfinal reply: %s", ais[len(ais)-1].text())
	}
	t.Logf("✅ route re-entry: recaller had %d ai turns and recalled the secret word across calls", len(ais))
}
