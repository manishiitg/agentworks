package step_based_workflow

import (
	"fmt"
	"strings"
	"testing"
)

// PLAT-055. These pin the four behaviors that make the merged turn different
// from the learnings turn it replaced: routing (C), per-step file ownership (D),
// a measured size signal (E), and compaction (F). Each one exists because its
// absence produced a specific observed failure.

func reflectionInput() StepReflectionTurnInput {
	return StepReflectionTurnInput{
		StepID:            "execute-actions-step-exec-reply-targets",
		StepDescription:   "post queue-approved replies",
		LearningObjective: "Capture reply selectors and timing that work reliably.",
		DBTableNames:      []string{"replies_posted", "tweet_performance", "daily_metrics"},
		SkillIndexLines:   42,
	}
}

func TestReflectionTurnRoutesEachStoreExplicitly(t *testing.T) {
	msg := BuildStepReflectionTurn(reflectionInput())

	// The routing table is the fix. Social Media's learning_objective already
	// banned incident narratives and stale counts by name and the agent wrote
	// them anyway, because the rule named stores the turn could not reach.
	for _, want := range []string{
		"Route each thing to the store that owns it",
		"Pulse Technical Review evaluates it",
		"soul/soul.md",
		"Learnings is not a fallback",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("reflection turn missing routing content %q", want)
		}
	}

	// The staleness test is the self-checkable version of the rule.
	if !strings.Contains(msg, "wrong in a month") {
		t.Error("reflection turn missing the staleness test")
	}
}

func TestReflectionTurnNamesRealDBTables(t *testing.T) {
	msg := BuildStepReflectionTurn(reflectionInput())

	// server A Latency pasted percentile tables and cost baselines into learnings
	// that already existed in latency_baselines and cost_daily_metrics, with
	// fresher data. Nothing told the step those tables existed, so caching felt
	// safer than betting a later run would query. Naming them is what makes
	// "reference the table" actionable rather than aspirational.
	for _, table := range []string{"replies_posted", "tweet_performance", "daily_metrics"} {
		if !strings.Contains(msg, table) {
			t.Errorf("reflection turn did not name existing table %q", table)
		}
	}
	if !strings.Contains(msg, "never paste its values here") {
		t.Error("reflection turn did not forbid copying DB values into learnings")
	}
}

// PLAT-058. The skill is one shared, topic-organized artifact that every step
// improves — NOT a per-step file set.
//
// An earlier revision told each step to own `references/<step-id>.md`. On its
// first live run that fragmented the skill by execution structure instead of by
// subject: it produced `execute-actions-step-exec-reply-targets.md` alongside
// the 110 KB `reply-target-execution.md` that SKILL.md actually links to, and
// left four "not yet folded into" pointers behind. Two steps independently
// filed harness concerns about it, one classifying it `harness_issue` with the
// exact impact — "new technique notes would silently stop being discoverable
// via the index, and the two files would drift out of sync."
//
// The 48 KB SKILL.md that motivated per-step ownership is prevented by the
// index-is-an-index and compaction rules instead, which do not require carving
// the skill up per step.
func TestReflectionTurnTreatsSkillAsOneSharedTopicOrganisedArtifact(t *testing.T) {
	msg := BuildStepReflectionTurn(reflectionInput())

	// The step id must never be presented as a write target.
	if strings.Contains(msg, "references/execute-actions-step-exec-reply-targets.md") {
		t.Error("reflection turn still names a per-step file as the write target")
	}
	for _, want := range []string{
		"one skill for the whole workflow",
		"shared by every step",
		"organized by **topic**",
		"topic file that owns it",
		"never for a step, a step id, or a run",
		"index, not a content home",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("reflection turn missing shared-skill content %q", want)
		}
	}

	// Orphans from the retired convention must be folded back in, not left to
	// rot alongside the topic file — otherwise the fork this replaced survives.
	if !strings.Contains(msg, "named after a step") || !strings.Contains(msg, "delete the orphan") {
		t.Error("reflection turn does not instruct folding step-named orphans back into their topic file")
	}

	// Reads were never the problem, and a shared skill makes them more valuable:
	// what another step recorded about this surface is exactly what prevents a
	// duplicate entry.
	if !strings.Contains(msg, "Read widely before writing") {
		t.Error("reflection turn does not push the agent to read other steps' contributions")
	}
}

// PLAT-055 follow-up. No fixed line count: an index legitimately grows with
// the number of contributing steps, so a number sized for a 6-step workflow
// (upwork's healthy index is ~96 lines) would be actively wrong for a 50-step
// one. What actually matters is structural — does each entry stay a one-line
// link, or has real detail (a paragraph, a selector, a timing rule) leaked in
// — so the same judgment instruction applies at 42 lines and at 510 lines
// alike; only the reported current count differs.
func TestReflectionTurnJudgesIndexStructurallyNotBySize(t *testing.T) {
	small := reflectionInput() // SkillIndexLines: 42
	large := reflectionInput()
	large.SkillIndexLines = 510 // server A Latency's actual bloated index size

	for name, in := range map[string]StepReflectionTurnInput{"small index": small, "large index": large} {
		msg := BuildStepReflectionTurn(in)
		wantLines := fmt.Sprintf("%d lines", in.SkillIndexLines)
		if !strings.Contains(msg, wantLines) {
			t.Errorf("%s: current index size not reported (want %q)", name, wantLines)
		}
		if !strings.Contains(msg, "Judge it structurally, not by size") {
			t.Errorf("%s: missing the structural-judgment instruction", name)
		}
		if !strings.Contains(msg, "one line") {
			t.Errorf("%s: missing the one-line-per-entry rule", name)
		}
	}
}

func TestReflectionTurnRequiresCompactionOverDatedAppends(t *testing.T) {
	msg := BuildStepReflectionTurn(reflectionInput())

	// Social Media re-documented the same two bugs four times in one day, one
	// entry admitting "even though this was already documented above".
	for _, want := range []string{"Update in place", "never the identity"} {
		if !strings.Contains(msg, want) {
			t.Errorf("reflection turn missing compaction rule %q", want)
		}
	}
}

// PLAT-058. Cleanup duty is qualitative and unconditional — every turn, on any
// file, at any size. A byte threshold is a poor proxy for quality (linkedin's
// cdp-browser.md is a healthy 23 KB covering all browser automation for that
// workflow) and per-step sizes are meaningless now that the skill is organized
// by topic rather than by step.
func TestReflectionTurnRequiresCleanupJudgmentOnEveryFileItTouches(t *testing.T) {
	msg := BuildStepReflectionTurn(reflectionInput())
	for _, want := range []string{
		"compact, precise, and informative",
		"Restated facts",
		"Narrative instead of technique",
		"someone else's job",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q", want)
		}
	}
	// The duty must attach to whatever file is touched, not to "your file".
	if !strings.Contains(msg, "every file you touch") {
		t.Error("cleanup duty is not scoped to every file the turn touches")
	}
}

// PLAT-173. The KB half of this same turn had none of the anti-append guidance
// the learnings half above carries, and the omission produced exactly the
// failure it would have prevented: customer-login's app-structure.md grew past
// every stated threshold because each survey cycle appended a fresh dated
// section instead of correcting the existing one. A step cannot be faulted for
// stacking sections when the only KB instruction it received was where to write
// and what to contribute.
func TestReflectionKBSectionRequiresUpdateInPlaceNotDatedAppends(t *testing.T) {
	in := reflectionInput()
	in.KBAccess = KBAccessReadWrite
	in.KBContribution = "Record the app's durable page and endpoint structure."
	msg := BuildStepReflectionTurn(in)

	// The same three duties the learnings half states, owed by the KB half too:
	// read the file before writing it, correct rather than append, and clean up
	// what is already there regardless of how small this turn's addition is.
	for _, want := range []string{
		"Update the existing section in place",
		"a new dated section",
		"read the whole topic file",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("KB section missing anti-append rule %q", want)
		}
	}

	// Compaction is the writing step's own duty. stores.md claimed notes
	// "compact themselves" past 20KB/30 sections; no such mechanism exists, and
	// a step told compaction is automatic has a positive reason to keep
	// appending. The turn must say plainly that nothing else will do it.
	if !strings.Contains(msg, "Nothing compacts these files for you") {
		t.Error("KB section does not tell the step that compaction is its own duty")
	}
}

func TestReflectionTurnOmitsSectionsThatDoNotApply(t *testing.T) {
	// Learnings only.
	learningsOnly := BuildStepReflectionTurn(reflectionInput())
	if strings.Contains(learningsOnly, "Knowledgebase — durable business") {
		t.Error("KB section rendered for a step with no KB contribution")
	}

	// KB only: a step with no learning objective still routes and contributes.
	kbOnly := reflectionInput()
	kbOnly.LearningObjective = ""
	kbOnly.KBAccess = KBAccessReadWrite
	kbOnly.KBContribution = "Record durable client-quality signals."
	msg := BuildStepReflectionTurn(kbOnly)
	if msg == "" {
		t.Fatal("KB-only step produced no reflection turn")
	}
	if strings.Contains(msg, "Learnings — reusable execution technique") {
		t.Error("learnings section rendered without an objective")
	}
	if !strings.Contains(msg, "Record durable client-quality signals.") {
		t.Error("KB contribution contract not carried into the turn")
	}
}

func TestReflectionTurnSkippedWhenNoStoreIsDue(t *testing.T) {
	// Neither learnings nor KB due: emitting a turn purely for the concern
	// outlet would add an LLM call to every read-only learning step
	// (LinkedIn has 6 of 6 locked), where the previous code emitted nothing.
	none := reflectionInput()
	none.LearningObjective = ""
	if msg := BuildStepReflectionTurn(none); msg != "" {
		t.Errorf("expected no reflection turn when no store is due, got %d bytes", len(msg))
	}

	// A KB contribution without write access is not due either.
	readOnlyKB := reflectionInput()
	readOnlyKB.LearningObjective = ""
	readOnlyKB.KBAccess = "read"
	readOnlyKB.KBContribution = "something"
	if msg := BuildStepReflectionTurn(readOnlyKB); msg != "" {
		t.Error("KB contribution without write access should not produce a turn")
	}
}

func TestLoadWorkflowDBTableNamesExcludesPlatformTables(t *testing.T) {
	// The list exists to show a step where its own observations belong. Naming
	// harness bookkeeping would invite a step to write into Pulse's records.
	for _, name := range []string{"pulse_finding_details", "pulse_review_log", "run_concerns", "report_human_inputs", "eval_results"} {
		if !isPlatformOwnedTable(name) {
			t.Errorf("%q should be filtered out of the step-facing table list", name)
		}
	}
	for _, name := range []string{"replies_posted", "latency_baselines", "cost_daily_metrics", "job_candidates"} {
		if isPlatformOwnedTable(name) {
			t.Errorf("%q is workflow-owned and must stay in the list", name)
		}
	}
}
