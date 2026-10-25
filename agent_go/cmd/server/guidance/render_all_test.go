package guidance

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

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

}

// TestGoalAdvisorPrioritizesStrategyOverHTMLFormatting asserted an
// analysis-first-over-HTML-formatting contract goal-advisor.md no longer
// carries -- removed by 0174b6aff "simplify Pulse workflow reviews"
// (2026-08-08). All 5 of its phrases confirmed absent by rendering the
// current template, not by grepping stale expectations. Removed 2026-08-17.

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
// TestNoTemplateNamesARemovedPulseTool is the invariant that failed the last
// time a tool contract moved: a read_skill signature change landed while 36
// templates still emitted the old form, so every call was schema-rejected until
// the templates caught up.
//
// Pulse uses current goal records and canonical issue writes. A template naming a removed tool instructs an agent to
// call something that no longer exists, and the failure surfaces as a broken
// tool rather than a stale prompt. This walks every embedded template, not only
// the registered kinds, so a template added outside a registry is covered too.

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

func TestNoTemplateNamesARemovedTool(t *testing.T) {
	removed := []string{
		"record_pulse_worklist",
		"record_pulse_result",
		"record_pulse_qa_request",
		"record_pulse_impact",
		"get_pulse_module_state",
		"get_pulse_finding_backlog",
		"get_goal_update_result",
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
