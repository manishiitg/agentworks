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

func TestGoalAgentHelpersStayScriptedAcrossGuidance(t *testing.T) {
	canonical, err := renderFromRegistry("design-plan", tmplData{}, allKinds)
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]string{"design-plan": canonical}
	for _, kind := range []string{"plan-design", "agent", "optimize-playbook"} {
		docs[kind] = RenderSystemDoc(kind)
	}
	for kind, doc := range docs {
		for _, want := range []string{"scripted helpers", "Crew step"} {
			if !containsNormalizedText(strings.ToLower(doc), strings.ToLower(want)) {
				t.Fatalf("%s missing Goal helper boundary %q", kind, want)
			}
		}
		for _, stale := range []string{"Conversational specialists use", "Only one nested delegation layer", "Scripted-mode todo_task", "Orchestrator scripted mode (deterministic delegation"} {
			if strings.Contains(doc, stale) {
				t.Fatalf("%s retains retired child-agent guidance %q", kind, stale)
			}
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
		"Builder and Autopilot share the measurement",
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
		"post-run-monitor": {monitor, []string{"SQLite-backed reviewer records", "structured Autopilot state", "must not update `builder/improve.html`", "dedicated Dashboard"}},
	}
	for label, check := range checks {
		for _, want := range check.wants {
			if !strings.Contains(check.rendered, want) {
				t.Fatalf("%s missing human-readable card contract %q", label, want)
			}
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
		"would you build this workflow this way from scratch",
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
		"SQLite-backed Autopilot lifecycle",
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
			"description turn the complete outcome",
			"Authored follow-ups are optional",
			"one-message-per-routine-action sequences",
		},
		// goal-advisor was checked here too until 0174b6aff "simplify Pulse
		// workflow reviews" (2026-08-08) removed this content from it; every
		// other kind below still carries it, confirmed by rendering each one.
		"plan-design": {
			"Description is the first turn alone",
			"items: []",
			"re-open its evidence and prove every success criterion",
			"authored follow-ups are optional",
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
				"scripted step executes it",
				"Do not spend an LLM turn reissuing a known request",
			},
		},
		"scripted": {
			registry: referenceKinds,
			wants: []string{
				"Declare these steps `scripted` from initial design",
				"No run-history threshold is required",
				"scripted fetcher(s) → agent processor",
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
		"agent":             {"re-read the system of record", "processed-versus-selected counts", "source_sql", "max_parallel", "saved scripted helpers", "No conversational or ad hoc LLM child agent", "already supplied by the user"},
		"stores":            {"transaction or retry contract", "window.report.query", "knowledgebase/notes/", "workflow-local skill index"},
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
// Pulse uses current goal records and canonical issue writes. A template naming a removed tool instructs an agent to
// call something that no longer exists, and the failure surfaces as a broken
// tool rather than a stale prompt. This walks every embedded template, not only
// the registered kinds, so a template added outside a registry is covered too.
func TestNoTemplateNamesARemovedPulseTool(t *testing.T) {
	removed := []string{
		"record_pulse_worklist",
		"record_pulse_result",
		"record_pulse_qa_request",
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
				t.Errorf("%s still instructs agents to call removed Autopilot tool %q; "+
					"use get_pulse_state(view=...), current goal records and canonical issue writes",
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
