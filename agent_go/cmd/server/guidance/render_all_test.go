package guidance

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// readPulseDesignSpec loads docs/design/pulse-post-run-monitor-spec.md.
//
// That file is NOT a reference doc: no prompt loads it and it is not in
// referenceKinds, so it cannot be rendered. It remains the written Pulse design
// spec, and these tests check the spec against the behavior the prompts and
// loaded reference docs implement. A failure here means the spec and the system
// disagree — and the spec is the side more likely to be stale.
func readPulseDesignSpec(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 8 {
		candidate := filepath.Join(dir, "docs", "design", "pulse-post-run-monitor-spec.md")
		if body, readErr := os.ReadFile(candidate); readErr == nil {
			return string(body)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate docs/design/pulse-post-run-monitor-spec.md from the test working directory")
	return ""
}

func TestSpecializeAdvisorsRequiresApprovalAndBoundedActivation(t *testing.T) {
	rendered, err := renderFromRegistry("specialize-advisors", tmplData{Focus: "social acquisition"}, referenceKinds)
	if err != nil {
		t.Fatalf("render specialize-advisors: %v", err)
	}
	for _, want := range []string{
		"Strategy Auditor specialization",
		"Goal Advisor specialization",
		"advisor-specialization-<UTC timestamp>",
		"advisor_specialization_approval_input_id",
		"`workflow.json` directly",
		"social acquisition",
		"DELTA-ONLY CONTRACT",
		"If the matching base contract already asks for it, remove it",
		"references/strategy-auditor.md",
		"references/goal-advisor.md",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("specialize-advisors guidance missing %q:\n%s", want, rendered)
		}
	}
}

func TestWorkflowGuideSmallBusinessAudience(t *testing.T) {
	rendered, err := renderFromRegistry("workflow-guide", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render workflow-guide: %v", err)
	}
	for _, want := range []string{
		"## Audience: non-technical small-business owners",
		"## Word translations (use the left column with users)",
		"your results page",
		"regular check-up with findings and decisions",
		"Never show file paths, table names, IDs, status codes",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("workflow-guide missing small-business audience contract %q", want)
		}
	}
}

func containsNormalizedText(haystack, needle string) bool {
	return strings.Contains(
		strings.Join(strings.Fields(haystack), " "),
		strings.Join(strings.Fields(needle), " "),
	)
}

// TestAllGuidanceTemplatesRender renders every template in both registries with
// empty caller context. A template that references a tmplData field that does
// not exist (or has a malformed action) only fails at execute time, which
// previously panicked at materialize time in production (buildMegaSkill). This
// guards that whole class of bug at test time.
func TestAllGuidanceTemplatesRender(t *testing.T) {
	for kind := range allKinds {
		if _, err := renderFromRegistry(kind, tmplData{}, allKinds); err != nil {
			t.Errorf("allKinds/%s failed to render: %v", kind, err)
		}
	}
	for kind := range referenceKinds {
		if _, err := renderFromRegistry(kind, tmplData{}, referenceKinds); err != nil {
			t.Errorf("referenceKinds/%s failed to render: %v", kind, err)
		}
	}
}

func TestEngineeringReviewAndPulseFixerAreSeparateCommands(t *testing.T) {
	if _, ok := allKinds["engineering-review"]; !ok {
		t.Fatal("engineering-review guidance is not registered")
	}
	if _, ok := allKinds["pulse-fixer"]; !ok {
		t.Fatal("pulse-fixer guidance is not registered")
	}
}

// get_reference_doc was removed when reference bundles became attached skills.
// A stale call in any rendered flow is dead on arrival for Pulse and workshop
// agents, so guard the complete registered guidance surface against regression.
func TestAllGuidanceUsesAttachedSkillReader(t *testing.T) {
	registries := []struct {
		name  string
		kinds map[string]kindMeta
	}{
		{name: "commands", kinds: allKinds},
		{name: "references", kinds: referenceKinds},
	}

	for _, registry := range registries {
		for kind := range registry.kinds {
			rendered, err := renderFromRegistry(kind, tmplData{}, registry.kinds)
			if err != nil {
				t.Errorf("%s/%s failed to render: %v", registry.name, kind, err)
				continue
			}
			if strings.Contains(rendered, "get_reference_doc") {
				t.Errorf("%s/%s still calls removed get_reference_doc; use read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/<kind>.md\"}])", registry.name, kind)
			}
		}
	}
}

func TestFocusedScheduledPulseReferencesStayComplete(t *testing.T) {
	tests := map[string]struct {
		wants []string
	}{
		"pulse-gate": {
			wants: []string{
				"progressive evidence scan", "CONCERNS:", "record_pulse_worklist", "one decision for every",
				"cannot suppress a new materially harmful miss", "Gate must not launch reviewers",
			},
		},
		"pulse-review-fixer": {
			wants: []string{
				"exactly once", "durable evidence", "from prose", `get_pulse_state(view="backlog", detail="compact")`,
				"normal Workflow Builder tools", "terminal", "cannot erase or block other due work", "priority-ordered Fix queue",
				"one reconciled `ownership_manifest`", "`kb_purity_manifest`", "`db_ownership_manifest`", "Learning-access changes\nbelong to Architecture",
				"proposal_only", "there is no evidence-wait route", "strategic-proposal-", "No mandatory Markdown checkpoint",
			},
		},
		"pulse-finalizer": {
			wants: []string{
				"Never treat missing as skipped/successful", "shown in the Pulse popup", "Do not write a separate presentation artifact",
				"directly in this parent", "Publish", "Notify", "record_pulse_result",
			},
		},
	}

	for kind, tc := range tests {
		rendered, err := renderFromRegistry(kind, tmplData{}, referenceKinds)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range tc.wants {
			if !containsNormalizedText(rendered, want) {
				t.Fatalf("%s reference missing %q", kind, want)
			}
		}
	}
}

func TestGoalAdvisorAliasUsesCanonicalStrategyReview(t *testing.T) {
	data := tmplData{Focus: "explore better reporting options", RunFolder: "iteration-7/group-a", WorkshopMode: "workshop"}
	canonical, err := renderFromRegistry("strategy-auditor", data, allKinds)
	if err != nil {
		t.Fatal(err)
	}
	alias, err := renderFromRegistry("goal-advisor", data, allKinds)
	if err != nil {
		t.Fatal(err)
	}
	if alias != canonical {
		t.Fatal("goal-advisor must render the same strategic review, including focus and run context")
	}
	for _, mode := range []string{"workshop", "run", "reporting"} {
		if modeAllowed("goal-advisor", mode) != modeAllowed("strategy-auditor", mode) {
			t.Errorf("goal-advisor must have the same mode restrictions as strategy-auditor in %s", mode)
		}
	}
}

func TestManualPulseCommandsKeepRunSetupReviewAndFixBoundariesSeparate(t *testing.T) {
	tests := map[string][]string{
		"ops-review": {
			"STANDALONE TECHNICAL REVIEW — CORRECTNESS FOCUS",
			"does the current approved design",
			"Do not launch another reviewer",
			"edit files or configuration",
			"structural improvements to Architecture",
			"A no-issue conclusion is valid",
		},
		"strategy-auditor": {
			// strategic_review is Goal Work (docs/design/pulse_goal_work.md);
			// the manual command runs the same pass and holds its permission
			// levels itself because the Builder chat is not runtime-restricted.
			"STANDALONE GOAL WORK",
			"without running Pulse Gate, Goal Advisor",
			"hold the permission levels yourself",
			"classify\n   individual findings",
			// data-module="strategy_auditor" was builder/improve.html dashboard
			// markup, retired along with the rest of that doc.
			"Do not launch `/goal-advisor` automatically",
		},
		"engineering-review": {
			"TECHNICAL REVIEW PHASE",
			"continuing Workflow Builder conversation",
			`"name":"builder-reference","path":"references/ops-review.md"`,
			"Standalone Operations Review",
			"manual=true",
			"note_only=true",
			"Do not call\n   `record_pulse_worklist`",
			"Own the review yourself",
			"link it to an existing issue, promote it with evidence, or reject it",
			"Do not apply repairs",
			"same retained Review+Fix task may later",
		},
		"pulse-fixer": {
			"PULSE FIX PHASE",
			"caller-supplied Fix message",
			"Do not rerun Technical Review",
			"Workflow observations are evidence",
			"Close every active issue in this pass",
			"Review completion and repair",
		},
	}

	for kind, wants := range tests {
		registry := allKinds
		if kind == "ops-review" {
			registry = referenceKinds
		}
		rendered, err := renderFromRegistry(kind, tmplData{}, registry)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range wants {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s guidance missing %q", kind, want)
			}
		}
	}

	advisor, err := renderFromRegistry("goal-advisor", tmplData{}, allKinds)
	if err != nil {
		t.Fatalf("render goal-advisor: %v", err)
	}
	// The positive "describes its one-off manual path" assertion that used to
	// sit here was removed by 0174b6aff "simplify Pulse workflow reviews"
	// (2026-08-08); goal-advisor no longer mentions manual invocation at all.
	// The negative invariant below still holds and is still worth keeping.
	if strings.Contains(advisor, "pulse_review_only=true") {
		t.Fatal("goal-advisor must not configure recurring Pulse")
	}
}

func TestStandaloneOpsReviewRunsDirectlyAndRequiresTerminalModuleResult(t *testing.T) {
	raw, err := os.ReadFile("templates/system/ops-review.md")
	if err != nil {
		t.Fatalf("read ops-review template: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"Perform the review yourself in this turn",
		"record_pulse_finding",
		`source="technical_review"`,
		`human_input_id`,
		"Never emit `decision_required` without this question",
		"record_pulse_result",
		`module="technical_review"`,
		"required-output impact",
		"normal execution noise",
	} {
		if !containsNormalizedText(prompt, want) {
			t.Errorf("standalone ops-review missing direct typed-completion contract %q", want)
		}
	}
	if strings.Contains(prompt, "run_in_background(") || strings.Contains(prompt, "Read the child completion") {
		t.Fatal("standalone ops-review still delegates to a redundant nested reviewer")
	}
}

func TestDeployedChannelSessionEndWordsHaveNoControlMeaning(t *testing.T) {
	raw, err := os.ReadFile("templates/system/deployed-channel.md")
	if err != nil {
		t.Fatalf("read deployed-channel template: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"Session-end words are ordinary text, never session controls",
		"do not end, clear, restart, or pause the session",
		`Never announce "session ended"`,
	} {
		if !containsNormalizedText(prompt, want) {
			t.Errorf("deployed-channel template missing session-end-words rule %q", want)
		}
	}
}

func TestStandaloneStrategyAuditRunsDirectlyAndRequiresTerminalModuleResult(t *testing.T) {
	raw, err := os.ReadFile("templates/review/strategy-auditor.md")
	if err != nil {
		t.Fatalf("read strategy-auditor template: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"Perform the review yourself in this turn",
		"record_pulse_finding",
		"record_pulse_result",
		"module=strategic_review",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("standalone strategy audit missing direct typed-completion contract %q", want)
		}
	}
	if strings.Contains(prompt, "run_in_background(") || strings.Contains(prompt, "Read the child completion") {
		t.Fatal("standalone strategy audit still delegates to a redundant nested reviewer")
	}
}

func TestAgentDelegationEligibilityStaysConsistentAcrossGuidance(t *testing.T) {
	canonical, err := renderFromRegistry("design-plan", tmplData{}, allKinds)
	if err != nil {
		t.Fatalf("render design-plan: %v", err)
	}
	for _, want := range []string{
		"real runtime orchestration decision",
		"A fixed child set and order does not justify adaptive delegation",
		"Routes are bounded capabilities",
		"predetermined isolated agentic tasks can remain explicit plan steps",
	} {
		if !containsNormalizedText(canonical, want) {
			t.Fatalf("canonical design-plan guidance missing %q", want)
		}
	}

	for _, kind := range []string{"plan-design", "agent", "optimize-playbook"} {
		doc := RenderSystemDoc(kind)
		if !containsNormalizedText(doc, "fixed child set and order does not justify adaptive routes") {
			t.Fatalf("%s guidance weakened the canonical fixed-child invariant", kind)
		}
	}

	for _, kind := range []string{"agent", "optimize-playbook"} {
		doc := RenderSystemDoc(kind)
		if strings.Contains(doc, "Scripted-mode todo_task") || strings.Contains(doc, "Orchestrator scripted mode (deterministic delegation") {
			t.Fatalf("%s still documents the removed orchestrator scripted fast path", kind)
		}
	}
}

func TestMeasurementPlanGuidanceCoversFlexibleContract(t *testing.T) {
	guidance, err := renderFromRegistry("measurement-plan", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render measurement-plan: %v", err)
	}
	for _, want := range []string{
		"evidence-backed",
		"record_goal_observations",
		"get_goal_metrics",
		"no topology change",
		"read-only history",
		"Builder and Pulse share the measurement",
		"window_start",
		"window_end",
		"an iteration folder is not required",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("measurement guidance missing %q\n\nGuidance:\n%s", want, guidance)
		}
	}
	for _, forbidden := range []string{
		"measurement-router",
		"measure-outcomes",
		"workflow_metrics",
	} {
		if strings.Contains(guidance, forbidden) {
			t.Fatalf("measurement guidance mandates removed topology (%q)\n\nGuidance:\n%s", forbidden, guidance)
		}
	}
}

func TestPulseCostGuidanceReconcilesRawLedgersWithoutDoubleCounting(t *testing.T) {
	postRun := readPulseDesignSpec(t)

	for _, want := range []string{
		"execution_id",
		"archived_run_folder",
		"legacy fallback",
		"group_folder",
		"by_model",
		"authoritative LLM total",
		"by_step_and_model",
		"never add",
		"unattributed/orchestrator",
		"workflow_orchestrator",
		"scripted/zero-LLM step",
		"run-folder",
	} {
		if !strings.Contains(postRun, want) {
			t.Fatalf("post-run-monitor cost reconciliation guidance missing %q", want)
		}
	}
}

func TestPulseGuidanceTracesStateChangesToRuntimeConsumers(t *testing.T) {
	postRun := readPulseDesignSpec(t)
	// Gate keeps the failure-mode flags visible (via the bug_review pointer) so
	// it can classify a suspect signal; the full reachability method lives in
	// pulse-bug-review, loaded only when bug_review is due.
	for _, want := range []string{
		"wrong_store_write",
		"shadow_store_drift",
		"successful write to a plausible table is",
	} {
		if !strings.Contains(postRun, want) {
			t.Fatalf("post-run monitor missing control-path contract %q", want)
		}
	}
	bugReview, err := renderFromRegistry("pulse-bug-review", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render pulse-bug-review: %v", err)
	}
	for _, want := range []string{
		"control-path reachability check",
		"wrong_store_write",
		"shadow_store_drift",
		"affected decision and which persisted value it consumed",
	} {
		if !strings.Contains(bugReview, want) {
			t.Fatalf("pulse-bug-review missing control-path contract %q", want)
		}
	}

	// The improve-database control-path block that used to sit here was
	// removed with the improve-* checklists: Stores Health now reviews store
	// health from evidence packs instead of loading per-store checklists.
	artifactReview, err := renderFromRegistry("review-artifact-drift", tmplData{}, allKinds)
	if err != nil {
		t.Fatalf("render review-artifact-drift: %v", err)
	}
	for _, want := range []string{
		"trace the exact changed record to the current runtime",
		"clean changelog/file diff is not enough",
		"plausible but non-canonical store",
	} {
		if !strings.Contains(artifactReview, want) {
			t.Fatalf("artifact review missing control-path contract %q", want)
		}
	}
}

func TestPulseGuidanceRequiresReviewedBaselineBeforeCadenceSkip(t *testing.T) {
	postRun := readPulseDesignSpec(t)
	for _, want := range []string{
		"Reviewed-baseline rule",
		"successful workflow run is evidence for a review; it is not a substitute",
		"completed, evidence-backed",
		"baseline review for that module",
		"**review outcomes**, not run",
		"not count as clean reviews",
		"review's checkpoint forward",
		"bounded adaptive backoff",
		"baseline pending",
		"baseline cannot justify skipping",
	} {
		if !strings.Contains(postRun, want) {
			t.Fatalf("post-run monitor missing reviewed-baseline contract %q", want)
		}
	}
}

func TestPulseGuidanceRequiresRuntimeAuthorityAndVisibleFreshness(t *testing.T) {
	postRun := readPulseDesignSpec(t)
	for _, want := range []string{
		"SQLite/runtime state is authoritative",
		"`builder/improve.html` is the durable explanatory",
		"HTML never overrides contradictory runtime state",
		"not measured this run · last measured",
		"Every skipped module must set at least one concrete next-check condition",
		"what new evidence caused the override",
		"progressive evidence scan",
		"one ordered finalizer turn",
		"record_pulse_result(command=...)",
		"not automatically due every Pulse",
		"One Agent-Owned Review+Fix Turn",
		"existing unchanged, existing with new evidence, reopened, or genuinely",
		"every evidence-backed severity-ordered finding row",
		"structured Fix queue",
		"Never trust HTML as recovery truth",
		"blindly reapply",
		"fixed_verified",
		"must not update `builder/improve.html`",
		"dedicated Dashboard prevents competing",
		"Build a conflict map",
		"explicit user-approved decisions and constraints",
		"mark only the affected",
		"finding-id manifest",
		"give every finding exactly one evidence-backed",
		"module's finding IDs",
		"Do not claim",
		"approval revalidation",
		"Unrelated drift",
		"stale_not_applied",
		"Never silently rebase or broaden",
		"only passed post-change proof",
		"changed_unverified",
		"owns review selection",
		"READ-ONLY REVIEW",
		"main Pulse agent owns the Review+Fix turn",
		"Go never launches a residual Fixer or recovery agent",
		"backend independently enforces",
		"confirm every module marked",
		"Never silently treat a",
		"missing result as skipped or successful",
		"Step concerns are first-class run evidence",
		"`CONCERNS: <brief evidence-backed concern>`",
		"runs/<run_folder>/logs/<step>/execution/execution-final-summary.json",
		"runs/<run_folder>/logs/<step>/execution/execution-attempt-*.json",
		"runs/<run_folder>/execution/<step>/session.json",
		"Never silently drop a concern",
		"Off-track goals tighten Workflow Review cadence",
		"below target",
		"declining, or stalled",
		"no exploratory QA checkpoint was completed after the latest observed goal",
		"does not justify a long calendar cooldown",
		"finding-free reviews over unchanged runtime paths may widen",
		"`correctness_bug`",
		"`efficiency_or_coaching`",
		"`insufficient_evidence`",
		"successful step may have chosen the wrong",
		"prior Workflow Review recorded `efficiency_or_coaching` trace evidence",
		"Backup risk: local only",
		"no verified destination is off-device",
		"Never describe this state as healthy",
		"warning in every Pulse",
		"notification until off-device protection is verified",
		"material issues newly found or reopened",
		"verified fixes and verified no-change closures",
		"exact active pending count",
		"Fixed by Pulse",
		"Still pending",
		"Needs your decision",
	} {
		if !strings.Contains(postRun, want) {
			t.Fatalf("post-run-monitor missing %q", want)
		}
	}

	// The deep Bug Review mechanics were extracted out of the Gate-loaded
	// post-run-monitor doc into pulse-bug-review, loaded only when bug_review
	// is due. Guard against re-inlining them into the frequent Gate turn.
	for _, moved := range []string{
		"Observable execution-trace review",
		"semantic execution defects",
		"execution/execution-attempt-*-iteration-*-conversation.json",
		"Judge observable decisions and evidence, not hidden chain-of-thought",
		"Route `efficiency_or_coaching` findings",
		"Exploratory QA contract",
	} {
		if strings.Contains(postRun, moved) {
			t.Fatalf("post-run-monitor should not re-inline extracted Bug Review contract %q", moved)
		}
	}
	if !strings.Contains(postRun, `read_skill(skills=[{"name":"builder-reference","path":"references/pulse-bug-review.md"}])`) {
		t.Fatal("post-run-monitor missing pointer to pulse-bug-review")
	}

	// The fix-verification contract is single-sourced: post-run-monitor and
	// pulse-fixer reference it instead of restating the detail. Guard against
	// the detail drifting back into the Gate-loaded post-run-monitor doc.
	if !strings.Contains(postRun, `read_skill(skills=[{"name":"builder-reference","path":"references/fix-verification.md"}])`) {
		t.Fatal("post-run-monitor missing pointer to fix-verification")
	}
	for _, moved := range []string{"baseline only, never proof", "mtime alone"} {
		if strings.Contains(postRun, moved) {
			t.Fatalf("post-run-monitor should reference fix-verification, not restate %q", moved)
		}
	}
	fixVerify, err := renderFromRegistry("fix-verification", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render fix-verification: %v", err)
	}
	for _, want := range []string{
		"post-change evidence boundary",
		"baseline only, never proof",
		"real runtime consumer",
		"a successful write alone is not proof",
		"mtime alone",
		"changed_unverified",
		"later normal run",
	} {
		if !strings.Contains(fixVerify, want) {
			t.Fatalf("fix-verification missing %q", want)
		}
	}

	fixPractices, err := renderFromRegistry("pulse-fixer-practices", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render pulse-fixer-practices: %v", err)
	}
	for _, want := range []string{
		"targeted evidence",
		"`query_step`/`get_step_prompts`",
		"Never recursively print an entire",
		"Separate symptom from root cause",
		"Map the contract boundary",
		"Schema and artifact contract repair",
		"producer stale",
		"validator stale",
		"contract split",
		"missing semantics",
		"negative fixture",
		"Prevalidation is a guard, not the durable repair",
		"Database repair",
		"Tool, path, and permission repair",
		"Scheduler and lifecycle repair",
		"Evaluation and report repair",
		"Learning and skill purity repair",
		"Cross-store ownership repair",
		"one authoritative owner",
		"`kb_purity_manifest`",
		"`db_ownership_manifest`",
		"Reduce learning writes after cleanup",
		"Do not launder content through references",
		"re-read every content-bearing Markdown file",
		"`learnings_access=\"read-write\"`",
	} {
		if !strings.Contains(fixPractices, want) {
			t.Fatalf("pulse-fixer-practices missing %q", want)
		}
	}

	bugReview, err := renderFromRegistry("pulse-bug-review", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render pulse-bug-review: %v", err)
	}
	for _, want := range []string{
		"Exploratory QA contract",
		"control-path reachability",
		"wrong_store_write",
		"Observable execution-trace review",
		"semantic execution defects",
		"execution/execution-attempt-*-iteration-*-conversation.json",
		"Judge observable decisions and evidence, not hidden chain-of-thought",
		"`correctness_bug`",
		"`efficiency_or_coaching`",
		"`insufficient_evidence`",
		"Route `efficiency_or_coaching` findings",
		// Weak-validation-gate check: flag a gate that passes on a self-asserted
		// marker without proving the real effect; not every step has a db.
		"self-asserted marker",
		"not every step has a db",
		// Repeated final-gate repairs deserve a bounded contract-health review,
		// not a blanket schema expansion or deletion pass.
		"Validation-contract health",
		"what meaningful bad outcome could pass if this check did not exist?",
		"boolean and a string pattern",
		"negative fixture",
	} {
		if !strings.Contains(bugReview, want) {
			t.Fatalf("pulse-bug-review missing %q", want)
		}
	}
}

func TestPulseGuidanceRejudgesActiveExperimentCadenceFromCurrentEvidence(t *testing.T) {
	postRun := readPulseDesignSpec(t)
	for _, want := range []string{
		"Every Gate must re-judge current goal evidence",
		"planned evidence boundary, not a lock",
		"reachable in the real runtime control",
		"not received a fair test",
		"implementation/control-path evidence",
		"real business or strategy",
	} {
		if !strings.Contains(postRun, want) {
			t.Fatalf("post-run monitor missing active-experiment cadence contract %q", want)
		}
	}

	// The goal-advisor-specific active-experiment lifecycle assertions that
	// used to follow here were removed by 0174b6aff "simplify Pulse workflow
	// reviews" (2026-08-08); goal-advisor no longer carries this content.
}

func TestStrategyAdvisorGuidanceBalancesExplorationEvidenceAndHumanDecisions(t *testing.T) {
	// strategic_review is Goal Work (docs/design/pulse_goal_work.md): it does
	// goal-advancing work within permission levels instead of only proposing.
	auditor, err := renderFromRegistry("strategy-auditor", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render strategy-auditor: %v", err)
	}
	for _, want := range []string{
		"Goal Work",
		"whether the workflow is achieving its goal",
		"doing that work",
		"get_goal_metrics(workspace_path=...)",
		`get_pulse_state(view="goal_work")`,
		"Undone work",
		"Unknown to the user",
		"Binding constraints",
		"if every step ran perfectly",
		"record_pulse_goal_work",
		"pulse/work/",
		"Permission levels",
		"never delete steps or schedules",
		"soul.md goals and constraints are never yours to edit",
		"Never purchase or spend money yourself",
		"create_human_input_request",
		"Challenging constraints",
		"never propose loosening",
		"Never break a constraint while challenging it",
		"materially new evidence",
		"never zero",
		"Activity (posting more) is not proof of growth",
		"Never change metric definitions or targets yourself",
		"record_pulse_finding",
		"A technical handoff alone is not a Goal Work result",
	} {
		if !containsNormalizedText(auditor, want) {
			t.Fatalf("Goal Work guidance missing %q:\n%s", want, auditor)
		}
	}

	gate, err := renderFromRegistry("pulse-gate", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render pulse-gate: %v", err)
	}
	for _, want := range []string{
		"`strategic_review`",
		"activity and outcomes diverge",
		"Missing telemetry is",
		"Never make one reviewer due merely because another reviewer",
		"Select each module independently",
		"Strategic Review combines the former Strategy Auditor and Goal Advisor",
		"Strategic Review for business usefulness or strategic headroom",
		"alternatives immediately",
		"materially different approaches",
	} {
		if !strings.Contains(gate, want) {
			t.Fatalf("pulse-gate missing Strategy Auditor routing %q:\n%s", want, gate)
		}
	}

	reviewer, err := renderFromRegistry("pulse-review-fixer", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render pulse-review-fixer: %v", err)
	}
	for _, want := range []string{
		"Strategic Review is one product/business sequence",
		"reasoning phases need separate messages only when useful",
		"Strategic Review owns its typed writes and terminal result in the same task",
		"It does not inherit technical repair authority",
		"materially different",
	} {
		if !strings.Contains(reviewer, want) {
			t.Fatalf("pulse-review-fixer missing Strategy Auditor boundary %q:\n%s", want, reviewer)
		}
	}
}

func TestTierGuidanceProtectsQualityWhileGoalsAreBelowTarget(t *testing.T) {
	cases := map[string][]string{
		"llm-selection": {
			"material workflow goal is below target",
			"representative eval/run evidence is at target",
			"Missing evidence means do not downgrade",
		},
		"optimize-playbook": {
			"material goals are below target",
			"proven quality-equivalent outputs",
			"explicitly approved reversible downgrade trial",
		},
	}
	for kind, wants := range cases {
		rendered, err := renderFromRegistry(kind, tmplData{}, referenceKinds)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range wants {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s missing goal-quality tier guard %q", kind, want)
			}
		}
	}

	// post-run-monitor is the design spec, not a rendered reference doc.
	// Same contract, read from its documented location.
	spec := readPulseDesignSpec(t)
	for _, want := range []string{
		"Goal quality outranks tier savings",
		"material success criterion is",
		"not evidence for a downgrade",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("pulse design spec missing %q", want)
		}
	}

}

func TestLLMOpsGuidanceReviewsExactPinsWithoutSilentUpgrade(t *testing.T) {
	cases := map[string][]string{
		"pulse-gate": {
			"Compare exact pins",
			"list_provider_models",
			"default_tier_models",
			"Provider-profile defaults auto-update",
			"infer freshness by name",
		},
		"llm-selection": {
			"Exact pins do not move automatically",
			"list_provider_models",
			"default_tier_models",
			"Never silently replace an exact pin",
			"Upgrade, Keep current, or Decide later",
		},
	}

	for kind, wants := range cases {
		rendered, err := renderFromRegistry(kind, tmplData{}, referenceKinds)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range wants {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s missing exact-pin freshness contract %q:\n%s", kind, want, rendered)
			}
		}
	}
	// post-run-monitor is the design spec, not a rendered reference doc.
	// Same contract, read from its documented location.
	spec := readPulseDesignSpec(t)
	for _, want := range []string{
		"Inventory every exact model pin",
		"list_provider_models",
		"Provider-profile workflows inherit current defaults",
		"Upgrade, Keep current, or Decide later",
		"newer catalog model is a review candidate",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("pulse design spec missing %q", want)
		}
	}

}

// TestGoalAdvisorPrioritizesStrategyOverHTMLFormatting asserted an
// analysis-first-over-HTML-formatting contract goal-advisor.md no longer
// carries -- removed by 0174b6aff "simplify Pulse workflow reviews"
// (2026-08-08). All 5 of its phrases confirmed absent by rendering the
// current template, not by grepping stale expectations. Removed 2026-08-17.

func TestPulseCardsKeepTechnicalEvidenceOutOfUserTimeline(t *testing.T) {
	monitor := readPulseDesignSpec(t)
	checks := map[string]struct {
		rendered string
		wants    []string
	}{
		"post-run-monitor": {monitor, []string{"SQLite-backed reviewer records", "structured Pulse state", "must not update `builder/improve.html`", "dedicated Dashboard"}},
	}
	for label, check := range checks {
		for _, want := range check.wants {
			if !strings.Contains(check.rendered, want) {
				t.Fatalf("%s missing human-readable card contract %q", label, want)
			}
		}
	}
}

func TestPulseRunsEveryDueReviewerAndWritesAttributedResults(t *testing.T) {
	monitor := readPulseDesignSpec(t)
	for _, want := range []string{
		`same main-agent conversation`,
		`cheapest sufficient approach`,
		`handles due modules in canonical order`,
		`owns review selection`,
		`terminal module receipts`,
		`later Dashboard stage`,
		`must not update ` + "`builder/improve.html`",
	} {
		if !strings.Contains(monitor, want) {
			t.Fatalf("post-run-monitor missing complete reviewer contract %q", want)
		}
	}

	// The pulse-setup module/source-of-truth contract that used to be checked
	// here was builder/improve.html guidance; improve.html is fully retired
	// and pulse-setup no longer mentions any of these phrases, confirmed by
	// rendering it. Removed 2026-08-17.

	// "data-pulse-section"/"data-module" were builder/improve.html dashboard
	// markup, dropped along with the rest of the retired doc; confirmed absent
	// from every template on disk, not specific to these two kinds.
	for _, kind := range []string{"ops-review", "strategy-auditor"} {
		registry := allKinds
		if kind == "ops-review" {
			registry = referenceKinds
		}
		review, renderErr := renderFromRegistry(kind, tmplData{}, registry)
		if renderErr != nil {
			t.Fatalf("render %s: %v", kind, renderErr)
		}
		if !strings.Contains(review, "Do not truncate") {
			t.Fatalf("%s missing standalone report contract %q", kind, "Do not truncate")
		}
	}
}

// TestPulseRelatedGuidanceUsesFourPartSectionOwnership asserted a
// `data-pulse-section="..."` / `data-module="..."` HTML-attribute scheme —
// builder/improve.html dashboard markup. improve.html is fully retired; the
// scheme is confirmed absent from every template on disk
// (`grep -rc "data-pulse-section\|data-module=" cmd/server/guidance/templates`
// returns nothing). Removed 2026-08-17.

// TestMaintenanceImproveGuidanceIsReadOnlyForPulseFixerHandoff asserted the
// read-only reviewer contracts of the four improve-* checklists
// (improve-learnings, improve-knowledge, improve-database, improve-report).
// Those checklists were deleted: they had no slash command and their only
// loader pointed at the wrong bundle. Stores Health now reviews store health
// from evidence packs. Removed with the templates.

// TestReviewArtifactDriftSharesPlanDriftReviewMechanismAndStaysReadOnlyElsewhere
// covers PLAT-258's slash/scheduled parity gap: /review-artifact-drift used to
// be a fully separate, fully read-only checklist that only READ
// plan_drift_review's precomputed evidence and deferred to it, never actually
// running its collector/repair contract/completion writer itself. It is now a
// two-part contract: Part 1 dispatches the real plan_drift_review procedure
// (shared candidate collector, repair authority, completion writer), Part 2
// remains the original read-only checklist for everything plan_drift_review
// does not cover.
func TestReviewArtifactDriftSharesPlanDriftReviewMechanismAndStaysReadOnlyElsewhere(t *testing.T) {
	rendered, err := renderFromRegistry("review-artifact-drift", tmplData{}, allKinds)
	if err != nil {
		t.Fatalf("render review-artifact-drift: %v", err)
	}
	for _, want := range []string{
		// Part 1: genuinely dispatches the shared mechanism, not just a read.
		`get_pulse_state(view="module")`,
		"plan_drift_candidates",
		"__workflow_drift_review__",
		`read_skill(skills=[{"name":"builder-reference","path":"references/plan-drift-review.md"}])`,
		"real repair authority, identical to `plan_drift_review`'s",
		"record_plan_drift_review",
		`record_pulse_result`,
		`module="plan_drift_review"`,
		// Part 2: unchanged read-only checklist, own completion writer.
		"stays strictly read-only",
		"Part 2 — the read-only checklist",
		"Never launch another reviewer",
		"mark_changelog_artifact_reviewed",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("review-artifact-drift missing %q:\n%s", want, rendered)
		}
	}
}

func TestPlanDriftReviewClosesDependencyReceiptsInSamePass(t *testing.T) {
	rendered, err := renderFromRegistry("plan-drift-review", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render plan-drift-review: %v", err)
	}
	for _, want := range []string{
		"Close the exact plan-change dependency receipts",
		"mark_changelog_artifact_reviewed",
		"all six",
		"receipt-only Technical pass",
		"Do not mark a newer",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("plan-drift-review missing %q:\n%s", want, rendered)
		}
	}
}

// review-code is gone (see the removal note above TestReviewImproveLogMigrationIsExtracted's
// former location); "builder/improve.html" is gone from every remaining
// specialist's own handoff text too, confirmed by rendering each one, since
// improve.html itself no longer exists for any of them to hand findings off
// to. What each specialist still owns — a structured finding packet it
// returns rather than writing HTML directly — is unchanged.
//
// goal-advisor no longer fits this pattern at all (0174b6aff "simplify Pulse
// workflow reviews" moved it to a different content model entirely — none of
// finding_id/target_key/recommended_fix/verification/user_judgment_required
// survive in it) and was removed from kinds. ops-review, strategy-auditor,
// and review-artifact-drift deliberately dropped finding_id/target_key in
// favor of "no invented identifier" — a real design decision, not drift —
// while keeping the rest of the packet contract; they get their own want-list.
func TestPulseSpecialistsReturnStructuredPacketsAndParentOwnsHTML(t *testing.T) {
	commonWants := []string{"recommended_fix", "verification", "user_judgment_required"}
	// The four improve-* entries that used to sit here were removed with
	// those checklists (no slash command; only loader pointed nowhere).
	kinds := map[string][]string{
		"design-plan":           append([]string{"finding_id", "target_key"}, commonWants...),
		"ops-review":            {"no invented identifier", "canonical issue roots", "Do not create a separate recommendation"},
		"strategy-auditor":      {"no invented identifier", "canonical issues", "Do not create separate focus"},
		"review-artifact-drift": {"no invented identifier", "Do not create separate recommendation"},
	}
	for kind, wants := range kinds {
		registry := allKinds
		if kind == "ops-review" {
			registry = referenceKinds
		}
		rendered, err := renderFromRegistry(kind, tmplData{}, registry)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range wants {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s missing structured specialist handoff %q", kind, want)
			}
		}
	}
}

func TestStandalonePulseReviewCommandsUsePersistedReviewerPipeline(t *testing.T) {
	for kind, module := range map[string]string{
		"ops-review":            "technical_review",
		"strategy-auditor":      "strategic_review",
		"review-artifact-drift": "technical_review",
	} {
		registry := allKinds
		if kind == "ops-review" {
			registry = referenceKinds
		}
		rendered, err := renderFromRegistry(kind, tmplData{}, registry)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		wants := []string{
			`module=` + module,
			"SQLite",
			"structured finding lifecycle",
			`get_pulse_state(view="review")`,
		}
		if kind == "ops-review" {
			wants = []string{
				"Do the review directly in this agent",
				"only canonical issues and one terminal review result",
				`module="technical_review"`,
				"record_pulse_result",
			}
		} else if kind == "strategy-auditor" {
			wants = []string{
				"perform the review directly",
				"canonical issues",
				`module=strategic_review`,
				"record_pulse_result",
			}
		}
		for _, want := range wants {
			if !containsNormalizedText(rendered, want) {
				t.Fatalf("%s missing persisted standalone-review contract %q", kind, want)
			}
		}
	}
}

// goal-advisor no longer links assumption-audit at all (0174b6aff "simplify
// Pulse workflow reviews" moved it to a different content model); every
// other kind below still does, confirmed by rendering each one.
func TestImprovementAndPlanGuidanceIncludesAssumptionAudit(t *testing.T) {
	for _, kind := range []string{
		"design-plan",
		"review-artifact-drift",
		"strategy-auditor",
	} {
		rendered, err := renderFromRegistry(kind, tmplData{}, allKinds)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		if !strings.Contains(rendered, "assumption-audit") {
			t.Fatalf("%s guidance does not include assumption-audit", kind)
		}
	}

	designPlan, err := renderFromRegistry("design-plan", tmplData{}, allKinds)
	if err != nil {
		t.Fatalf("render design-plan: %v", err)
	}
	for _, want := range []string{
		"It is read-only",
		"dependent artifacts",
		"VISUAL MAP",
		"PRIORITIES",
	} {
		if !strings.Contains(designPlan, want) {
			t.Fatalf("combined design-plan guidance missing %q", want)
		}
	}
	// "never discard findings" was removed from design-plan by 0174b6aff
	// "simplify Pulse workflow reviews" (2026-08-08).
	if _, exists := allKinds["review-plan"]; exists {
		t.Fatal("review-plan must remain merged into design-plan")
	}
	// The improve-* parent-provision loop that used to sit here was removed
	// with those checklists.
	audit, err := renderFromRegistry("assumption-audit", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render assumption-audit: %v", err)
	}
	for _, want := range []string{
		"Explicit user constraint",
		"Verified external constraint",
		"Current design choice",
		"Agent-inferred assumption",
		"SQLite-backed Pulse lifecycle",
		"Do not turn targeted maintenance into a full audit",
	} {
		if !strings.Contains(audit, want) {
			t.Fatalf("assumption-audit missing %q", want)
		}
	}
	// "Do not add an assumptions panel" was removed from assumption-audit.md
	// by 0174b6aff "simplify Pulse workflow reviews" (2026-08-08) -- another
	// builder/improve.html-dashboard-specific instruction that no longer
	// applies now that improve.html is retired.
}

// TestGoalAdvisorTreatsCleanAbstentionAsStrategyEvidence asserted an
// elaborate strategy-first-pass / active-experiment-lifecycle apparatus
// (PHASE 1A/1B, data-experiment-kind, advisor-experiment cards) removed by
// 0174b6aff "simplify Pulse workflow reviews" (2026-08-08). All 32 phrases
// confirmed absent by rendering the current template. Removed 2026-08-17.

// The goal-advisor half of this test (a metrics-subsystem-abstention
// contract) was removed by 0174b6aff "simplify Pulse workflow reviews"
// (2026-08-08); all 6 of its phrases confirmed absent by rendering the
// current template. The improve-report handoff it hands off to is unchanged
// except one phrase, also removed by the same commit.
// TestGoalAdvisorMetricsFlowUsesPlanAndReportHandoff asserted the measurement
// handoff inside improve-report (GOAL ADVISOR MEASUREMENT HANDOFF). That
// checklist was deleted: it had no slash command and no live loader.
// Removed with the template.

func TestPlanReviewAndGoalAdvisorPreferCoherentAgenticSteps(t *testing.T) {
	checks := map[string][]string{
		"design-plan": {
			"Modern agents can own a substantial end-to-end outcome in one step",
			"Validation sequence, not micro-steps",
			"first work turn the complete outcome",
			"one-message-per-routine-action sequences",
		},
		// goal-advisor was checked here too until 0174b6aff "simplify Pulse
		// workflow reviews" (2026-08-08) removed this content from it; every
		// other kind below still carries it, confirmed by rendering each one.
		"plan-design": {
			"Give the work turn the complete outcome",
			"do not create one item per checklist line",
			"re-open the evidence and verify every success criterion",
			"tiny routine instructions",
			"one coherent agentic outcome",
		},
	}

	for kind, wants := range checks {
		registry := allKinds
		if kind == "plan-design" {
			registry = referenceKinds
		}
		rendered, err := renderFromRegistry(kind, tmplData{}, registry)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range wants {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s missing coherent-agentic-step guidance %q", kind, want)
			}
		}
	}
}

func TestDeterministicFetchersFeedLargeAgenticProcessors(t *testing.T) {
	checks := map[string]struct {
		registry map[string]kindMeta
		wants    []string
	}{
		"design-plan": {
			registry: allKinds,
			wants: []string{
				"Scripted acquisition, agentic processing",
				"batch related calls",
				"feed the durable rows/artifacts into a large agent",
				"10+-run evidence bar is only for *freezing*",
			},
		},
		// goal-advisor carried this too until 0174b6aff "simplify Pulse
		// workflow reviews" (2026-08-08); every other kind below still does.
		"plan-design": {
			registry: referenceKinds,
			wants: []string{
				"Deterministic fetcher → agentic processor",
				"do not create one step per endpoint or command",
				"scripted regular step executes it",
				"Consume deterministic evidence; do not fetch it conversationally",
			},
		},
		"scripted": {
			registry: referenceKinds,
			wants: []string{
				"Declare these steps `scripted` from initial design",
				"No run-history threshold is required",
				"regular scripted fetcher(s) → agent processor",
				// PLAT-436: scripts are built by the Builder, not healed by a run.
				"Scripts are built by you, not healed by the run",
				"no LLM rewrites",
				"`execute_step` (the only place an agent may write or repair",
			},
		},
		"agent": {
			registry: referenceKinds,
			wants: []string{
				"fetch-and-normalize-authoritative-data",
				"Do not use one step per endpoint",
				"execute-request-spec",
				// Store-writable allow-list (db/assets is the only step-writable file home).
				"the hard allow-list",
				// Validate-on-what-it-produces, db-first, no forced throwaway JSON.
				"Validate on what the step actually produces",
				"will not force a throwaway output file",
				"has no gate at all",
			},
		},
		"step-config": {
			registry: referenceKinds,
			wants: []string{
				"Scripts are the default for DETERMINISTIC execution",
				"Use coherent scripted fetchers, not micro-scripts",
				"10+ representative-run threshold applies only before freezing",
			},
		},
	}

	for kind, check := range checks {
		rendered, err := renderFromRegistry(kind, tmplData{}, check.registry)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range check.wants {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s missing deterministic-fetcher guidance %q", kind, want)
			}
		}
	}

	stale := map[string]struct {
		registry map[string]kindMeta
		text     string
	}{
		"planning-steps":    {registry: referenceKinds, text: "one atomic action with no"},
		"optimize-playbook": {registry: referenceKinds, text: "add a separate step after it that reads the output"},
		"agent":             {registry: referenceKinds, text: "manages multiple discrete tasks"},
	}
	for kind, check := range stale {
		rendered, err := renderFromRegistry(kind, tmplData{}, check.registry)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		if strings.Contains(rendered, check.text) {
			t.Fatalf("%s retains stale micro-step guidance %q", kind, check.text)
		}
	}
}

func TestSharedContextSpansOwnProofValidationAndRepair(t *testing.T) {
	checks := map[string]struct {
		registry map[string]kindMeta
		wants    []string
	}{
		"planning-steps": {
			registry: referenceKinds,
			wants: []string{
				"one large `agent` per shared-context span",
				"proof/provenance output",
				"Use multiple large sequences when their contexts should not be shared",
				"The builder must decide this from",
			},
		},
		"plan-design": {
			registry: referenceKinds,
			wants: []string{
				"one large `agent` for each coherent shared-context span",
				"proof/evidence contract",
				"Multiple large sequences are correct when their contexts should not be shared",
				"builder must decide this from the workflow semantics",
			},
		},
		// goal-advisor carried this too until 0174b6aff "simplify Pulse
		// workflow reviews" (2026-08-08); every other kind here still does.
		"optimize-playbook": {
			registry: referenceKinds,
			wants: []string{
				"keep it in the same shared context",
				"repair/double-check turn",
				"Strengthen before splitting",
				"Validate in context",
			},
		},
		"agent": {
			registry: referenceKinds,
			wants: []string{
				"one persistent conversation",
				"re-read the system of record and prove the effect",
				"one large sequence per coherent shared-context span",
			},
		},
	}

	for kind, check := range checks {
		rendered, err := renderFromRegistry(kind, tmplData{}, check.registry)
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		for _, want := range check.wants {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s missing shared-context proof guidance %q", kind, want)
			}
		}
	}
}

// Check the shipped skill bundle: examples remain discoverable in their owning
// references, and no live reference sends the builder to the retired document.
func TestWorkflowCompositionExamplesLiveInOwningReferences(t *testing.T) {
	for _, mode := range []string{"workshop", "run", "multi-agent"} {
		for _, skill := range []*llmtypes.Skill{MaterializeReferenceSkill(mode), MaterializeGuidanceSkill(mode)} {
			if skill == nil {
				continue
			}
			if strings.Contains(skill.Content, "workflow-patterns") {
				t.Fatalf("%s skill still advertises retired workflow-patterns", mode)
			}
			for _, file := range skill.SupportingFiles {
				if strings.Contains(file.RelPath, "workflow-patterns") || strings.Contains(string(file.Content), "workflow-patterns") {
					t.Fatalf("%s/%s still contains a retired reference", mode, file.RelPath)
				}
			}
		}
	}
	for kind, wants := range map[string][]string{
		"plan-design":       {"Composition examples", "readiness", "draft"},
		"agent":             {"re-read the system of record", "processed-versus-selected counts", "source_sql", "max_parallel", "known upfront", "unknown work breakdown is one use case, not a prerequisite", "already supplied by the user"},
		"stores":            {"transaction or retry contract", "window.report.query", "knowledgebase/notes/", "learnings/_global/SKILL.md"},
		"human-in-the-loop": {"provide the actual draft/change/evidence", "Schedules must complete unattended"},
	} {
		doc := RenderSystemDoc(kind)
		for _, want := range wants {
			if !containsNormalizedText(strings.ToLower(doc), strings.ToLower(want)) {
				t.Fatalf("%s lost composition guidance %q", kind, want)
			}
		}
	}
}

// The store freshness mechanism: Gate reads the code-owned _freshness ledgers and
// marks stores_health due (for its learnings/KB dimensions) on a confirmation-recency
// signal; the reviewer docs gain a re-verify -> demote pass and protect the
// code-owned ledger from edits.
func TestPulseStoreFreshnessTriggerAndReviewerPass(t *testing.T) {
	postRun := readPulseDesignSpec(t)
	for _, want := range []string{
		"learnings/_global/_freshness.json",
		"knowledgebase/_freshness.json",
		"last_confirmed_run",
		"freshness (confirmation recency)",
		"complete skill package has no recorded",
		"every content-bearing Markdown reference",
		"must leave the entire package",
		"index or valid Markdown shape alone is not proof",
		"one reconciled `ownership_manifest`",
		"one semantic item, one authoritative owner",
		"`kb_purity_manifest`",
		"`db_ownership_manifest`",
		"Recommend `learnings_access=\"read\"` for mature",
	} {
		if !strings.Contains(postRun, want) {
			t.Fatalf("post-run-monitor missing freshness trigger %q", want)
		}
	}

	// The improve-learnings / improve-knowledge freshness-pass blocks that
	// used to sit here were removed with those checklists.
}

// TestNoTemplateNamesARemovedPulseTool is the invariant that failed the last
// time a tool contract moved: a read_skill signature change landed while 36
// templates still emitted the old form, so every call was schema-rejected until
// the templates caught up.
//
// The reviewer-facing Pulse surface is compact: state, one Gate receipt, one
// result, and canonical issue writes. A template naming a removed tool instructs an agent to
// call something that no longer exists, and the failure surfaces as a broken
// tool rather than a stale prompt. This walks every embedded template, not only
// the registered kinds, so a template added outside a registry is covered too.
func TestNoTemplateNamesARemovedPulseTool(t *testing.T) {
	removed := []string{
		"record_pulse_impact",
		"get_pulse_module_state",
		"get_pulse_finding_backlog",
		"get_pulse_review_result",
		"start_pulse_fix_attempt",
		"mark_pulse_module_result",
		"mark_pulse_final_command_result",
	}
	visited := 0
	err := fs.WalkDir(templatesFS, "templates", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		body, readErr := templatesFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		visited++
		for _, name := range removed {
			if strings.Contains(string(body), name) {
				t.Errorf("%s still instructs agents to call removed Pulse tool %q; "+
					"the surface is get_pulse_state(view=...), record_pulse_worklist, record_pulse_result, and canonical issue writes",
					path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded templates: %v", err)
	}
	if visited == 0 {
		t.Fatal("no templates were walked; the embed pattern or path changed")
	}
}

// The improve-* checklists had no slash command and no live loader, and
// ops-review / specialize-advisors are reference docs, not commands — but
// prose kept presenting all of them as invokable `/commands`, so agents were
// instructed to invoke things no menu offers. This walks every embedded
// template and fails any slash spelling or tool-call form of a name that is
// not a command. Plain `references/<name>.md` load paths are legitimate and
// intentionally not matched.
func TestNoTemplateInvokesARemovedCommand(t *testing.T) {
	removed := []string{
		"`/improve-report`",
		"`/improve-knowledge`",
		"`/improve-learnings`",
		"`/improve-database`",
		"`/ops-review`",
		"`/specialize-advisors`",
		`kind="improve-report"`,
		`kind="improve-knowledge"`,
		`kind="improve-learnings"`,
		`kind="improve-database"`,
		`kind="ops-review"`,
		`kind="specialize-advisors"`,
	}
	visited := 0
	err := fs.WalkDir(templatesFS, "templates", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		body, readErr := templatesFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		visited++
		for _, name := range removed {
			if strings.Contains(string(body), name) {
				t.Errorf("%s still invokes removed command %q; it has no menu entry and no tool kind",
					path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded templates: %v", err)
	}
	if visited == 0 {
		t.Fatal("no templates were walked; the embed pattern or path changed")
	}
}

// read_skill deliberately accepts one file per call so large references cannot
// be combined into a result that the coding CLI truncates. Keep generated
// guidance aligned with that schema: a prompt that batches items guarantees a
// recoverable but noisy tool failure before the agent can do useful work.
func TestNoTemplateBatchesReadSkillFiles(t *testing.T) {
	visited := 0
	err := fs.WalkDir(templatesFS, "templates", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		body, readErr := templatesFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		visited++
		for lineNumber, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, "read_skill") && strings.Contains(line, "},{") {
				t.Errorf("%s:%d batches multiple read_skill files; issue one call per file", path, lineNumber+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded templates: %v", err)
	}
	if visited == 0 {
		t.Fatal("no templates were walked; the embed pattern or path changed")
	}
}

// review-improve-log, its migration doc, and its skeleton were all
// builder/improve.html guidance. improve.html itself was fully retired
// (Pulse's own DB-backed findings and the SQLite Pulse popup replaced it);
// there is no HTML journal left for any of this to describe. Removed
// 2026-08-17 rather than kept green against a doc that no longer exists.

func TestPulseReviewsUseOneOptionalSQLiteNoteWithoutReportingTurns(t *testing.T) {
	for _, kind := range []string{"technical-review", "architecture-review", "strategy-auditor", "plan-drift-review"} {
		rendered, err := renderFromRegistry(kind, tmplData{}, referenceKinds)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`view="review_notes"`, "optional review_note", "No prescribed sections", "note_only=true", "A no-change conclusion is valid"} {
			if !containsNormalizedText(rendered, want) {
				t.Fatalf("%s missing minimal recording contract %q", kind, want)
			}
		}
		for _, forbidden := range []string{"Update the run-scoped checkpoint", "Preserve checkpoint", "final persistence phase owns", "Keep a compact checkpoint:"} {
			if containsNormalizedText(rendered, forbidden) {
				t.Fatalf("%s retains mandatory bookkeeping %q", kind, forbidden)
			}
		}
	}
}

func TestArchitectureOwnsMeasuredTierChanges(t *testing.T) {
	rendered, err := renderFromRegistry("architecture-review", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"execution_tier", "query_workflow_costs", "output quality, retries, duration", "not proof a cheaper tier preserves quality", "rollback", "human_input_id", "No per-run tier switching", "Never silently replace a user pin"} {
		if !containsNormalizedText(rendered, want) {
			t.Fatalf("architecture tier contract missing %q", want)
		}
	}
	for _, kind := range []string{"step-config", "pulse-fixer-practices", "llm-selection", "pulse-gate"} {
		text, err := renderFromRegistry(kind, tmplData{}, referenceKinds)
		if err != nil {
			t.Fatal(err)
		}
		for _, retired := range []string{"disables adaptive tiering", "3 stable runs", "letting adaptive tiering do the work"} {
			if strings.Contains(strings.ToLower(text), retired) {
				t.Fatalf("%s still instructs automatic tier promotion: %s", kind, retired)
			}
		}
	}
}

func TestPulseResearchReviewsUseDifferentHorizonsAndStrategyDoesNotLoopOnSymptoms(t *testing.T) {
	gate, err := renderFromRegistry("pulse-gate", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Architecture normally uses the longest review horizon",
		"Strategic Review uses the shorter research horizon",
		"place Architecture later than Strategy",
		`"booking-heavy"`,
		"perfect-execution counterfactual",
	} {
		if !containsNormalizedText(gate, want) {
			t.Fatalf("pulse gate missing review-horizon contract %q", want)
		}
	}

	strategy, err := renderFromRegistry("strategy-auditor", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"assume it is fixed, and continue with the goal",
		"A technical handoff alone is not a Goal Work result",
		"Do not let a recurring operational symptom consume repeated passes",
	} {
		if !containsNormalizedText(strategy, want) {
			t.Fatalf("strategy reviewer missing symptom-loop guard %q", want)
		}
	}
}
