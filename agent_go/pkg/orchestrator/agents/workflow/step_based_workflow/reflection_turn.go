package step_based_workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// PLAT-055. One reflection turn replacing the KB turn followed by the learnings
// turn.
//
// The split was itself the defect. During the KB turn the agent has not yet
// worked out what it learned; by the learnings turn the KB door has closed and
// the database was never reachable from a reflection turn at all. "Which store
// owns this?" was therefore never a decision the agent got to make — it was
// decided by whichever door happened to be open, and learnings was usually the
// only one. That is how a validator-bug report, a percentile table and a live
// security inventory all ended up inside "skill" files.
//
// With every outlet open at once, routing becomes a real choice, and the rules
// below become followable rather than aspirational. It is also one LLM call per
// contributing step instead of two.

// StepReflectionTurnInput carries everything the merged turn needs.
type StepReflectionTurnInput struct {
	StepID          string
	StepDescription string

	// LearningObjective gates the learnings half; empty means this step
	// contributes no learnings and that section is omitted.
	LearningObjective   string
	LearningsTargetPath string

	// KBAccess/KBContribution gate the knowledgebase half on the same terms the
	// standalone KB turn used.
	KBAccess          string
	KBContribution    string
	KBNotesTargetPath string

	HasBrowserAccess bool

	// DBTableNames is the workflow's actual table list. Injecting it is what
	// makes "reference the table instead of copying its values" actionable: a
	// step holding a measurement otherwise has no way to know a home for it
	// already exists, and caching into the skill file feels safer than betting a
	// future run will go looking.
	DBTableNames []string

	// SkillIndexLines reports the index's current size. It is a signal, not a
	// budget: the judgment asked for is structural (is each entry still a link
	// plus a one-line description?), and applies identically at 42 lines and at
	// 510. Per-step file sizes are deliberately absent — the skill is organized
	// by topic, so no single file belongs to the step being reflected on.
	SkillIndexLines int
}

func (in StepReflectionTurnInput) writesLearnings() bool {
	return strings.TrimSpace(in.LearningObjective) != ""
}

func (in StepReflectionTurnInput) writesKB() bool {
	return kbAccessAllowsWrite(in.KBAccess) && strings.TrimSpace(in.KBContribution) != ""
}

// BuildStepReflectionTurn renders the merged post-completion turn. Returns
// empty when the step has nothing to reflect on and nothing to report.
func BuildStepReflectionTurn(in StepReflectionTurnInput) string {
	stepID := strings.TrimSpace(in.StepID)
	if stepID == "" {
		return ""
	}
	learnings := in.writesLearnings()
	kb := in.writesKB()
	// No store to write to means no turn. Emitting one purely for the concern
	// outlet would add an LLM call to every step that contributes nothing —
	// including every read-only learning step, where the old code
	// correctly emitted nothing at all. Pulse reviews retained execution output
	// and deterministic receipts directly after the run.
	if !learnings && !kb {
		return ""
	}

	learningsRoot := strings.TrimRight(strings.TrimSpace(in.LearningsTargetPath), "/")
	if learningsRoot == "" {
		learningsRoot = filepath.Join("learnings", GlobalLearningID)
	}
	skillPath := filepath.Join(learningsRoot, "SKILL.md")
	referencesPath := filepath.Join(learningsRoot, "references")

	var b strings.Builder
	b.WriteString("## Reflection (dedicated turn)\n\n")
	b.WriteString("Your step work is complete and pre-validation passed. This turn is for recording what the run produced beyond its outputs. ")
	b.WriteString("You are the only party that watched this run happen, so anything you noticed and do not record here is lost.\n\n")

	// ---- Routing rule. The core of PLAT-055. ----
	b.WriteString("### Route each thing to the store that owns it\n\n")
	b.WriteString("You can write to several stores in this turn. Choose deliberately — do not use one as a dumping ground because it is convenient.\n\n")
	b.WriteString("| What you have | Where it goes |\n|---|---|\n")
	b.WriteString("| Reusable execution technique — selectors, timings, auth flows, API quirks, retry/recovery patterns | learnings |\n")
	b.WriteString("| Measurements, counts, run results, current status | the database (you already wrote these during the step) |\n")
	b.WriteString("| Durable business/domain narrative | knowledgebase |\n")
	b.WriteString("| A defect, contradiction, or platform problem | retain concrete evidence in the step result; Pulse Technical Review evaluates it |\n")
	b.WriteString("| An owner constraint value (cap, limit, threshold) | nowhere — it lives in `soul/soul.md` and is injected every run |\n\n")

	b.WriteString("**The test that settles most cases: if it will be wrong in a month, it is not a learning.** ")
	b.WriteString("`this API needs region us-east-1` stays true. `spend is ~$50/day` and `3 items are stale` do not.\n\n")
	b.WriteString("**Learnings is not a fallback.** If something belongs to another store, put it there. ")
	b.WriteString("Do not write it into learnings because it seemed easier to reach — that is how these files fill with incident narratives and stale numbers that later runs then trust.\n\n")

	if len(in.DBTableNames) > 0 {
		b.WriteString("**These tables already exist and any future run can query them with `query_workflow_db`:**\n`")
		b.WriteString(strings.Join(in.DBTableNames, "`, `"))
		b.WriteString("`\n\n")
		b.WriteString("If your observation belongs in one of them it is already recorded — name the table, never paste its values here. A number copied out of the database is stale the moment the next run writes.\n\n")
	}

	// PUL-D0BAC922: reflection narration contradicted the artifacts the step had
	// just written (6 cases on latency-report) because it restated results from
	// conversation memory. Results must come from the files or database.
	b.WriteString("**State results only from what the step actually produced.** Before you mention any count, status or outcome of this run, re-read the output files the step wrote or query the database. Never restate results from memory of the conversation; if the files and your memory disagree, the files are right.\n\n")

	// Pulse Technical Review independently evaluates retained output and runtime
	// receipts. Reflection does not file observations or infer issue identity.

	if learnings {
		b.WriteString(buildReflectionLearningsSection(in, skillPath, referencesPath))
	}
	if kb {
		b.WriteString(buildReflectionKBSection(in))
	}

	b.WriteString("### Closing\n\n")
	b.WriteString("- This is your only reflection turn for this step; there is no second pass.\n")
	if learnings {
		b.WriteString("- If there is genuinely nothing new worth capturing, do **not** force an edit. Say so briefly and why.\n")
		b.WriteString("- If you changed files, end with exactly one line: `Learnings updated: files changed: <comma-separated list>`.\n")
	}
	b.WriteString("- Available tools: `execute_shell_command` for read-only inspection (`cat`, `ls`, `find`, `wc`), `query_workflow_db` for reads, and `diff_patch_workspace_file` for writes.\n")

	return b.String()
}

func buildReflectionLearningsSection(in StepReflectionTurnInput, skillPath, referencesPath string) string {
	var b strings.Builder
	b.WriteString("### Learnings — reusable execution technique only\n\n")

	// ---- Topic ownership (D, revised). ----
	//
	// This deliberately does NOT name a per-step file. An earlier revision told
	// each step to own `references/<step-id>.md`, which fragmented one skill by
	// execution structure instead of by subject: it produced names like
	// `execute-actions-step-exec-reply-targets.md`, orphaned the topic files
	// SKILL.md actually links to, and re-forked on every step rename or split.
	// Two steps independently filed harness concerns about it on its first live
	// run. The problem that rule was meant to solve — one shared SKILL.md
	// growing to 48 KB — is handled by the index-is-an-index and compaction
	// rules below, which do not require carving the skill up per step.
	//
	// The index is already in this agent's prompt under '## Skill', so no
	// discovery step is needed to find the owning topic.
	b.WriteString("**This is one skill for the whole workflow, shared by every step and improved by all of them.** ")
	b.WriteString("It is organized by **topic**, not by step. The index in `")
	b.WriteString(skillPath)
	b.WriteString("` — already in your prompt above under `## Skill` — names the topic file that owns each area of this workflow's execution knowledge.\n\n")

	b.WriteString("**Put your contribution in the topic file that owns it.** Extend that file, however many steps also write to it. ")
	b.WriteString("If your observation genuinely belongs to no existing topic, create a new one named for the *subject* (`references/<topic>.md`, e.g. `browser-session.md`, `rate-limits.md`) — never for a step, a step id, or a run. ")
	b.WriteString("Add its index line when you do.\n\n")

	b.WriteString("**If you find a file named after a step** (a leftover from an earlier convention), fold its content into the topic file that owns that subject, delete the orphan, and remove its index line — as part of this turn.\n\n")

	// The artifact is a Skill, not a notes pile. Its frontmatter `description` is
	// what tells a future reader (and any skill-selection surface) what it
	// covers; a stale one misrepresents the whole artifact. Social Media's still
	// read "Minimal reset learning baseline" while holding 300 KB of execution
	// technique across a dozen topics.
	b.WriteString("This is a **skill** in the standard format — frontmatter, then the index, then `references/`. ")
	b.WriteString("If your change makes the frontmatter `description` inaccurate about what the skill now covers, correct that one line too.\n\n")

	// Deliberately no size threshold here — a byte count is a poor proxy for
	// quality. A large file can be genuinely dense, well-organized coverage of
	// real complexity; a small one can already be redundant. Judge the content
	// on its own terms every turn, not against a number.
	b.WriteString("**Keep every file you touch compact, precise, and informative — a reference, not a growing log.** Each time you touch one, actually read the whole thing and check for the patterns below, whether or not you have new content to add:\n")
	b.WriteString("- **Restated facts.** Two or more entries establishing the same behavior — merge them into one, keep whichever is most current, delete the rest. A date is metadata on an entry (`verified 2026-07-02`), never its identity; \"third confirmation\" and \"fourth confirmation\" of the same bug are one entry, not four.\n")
	b.WriteString("- **Narrative instead of technique.** A trial-and-error account of what you tried, run IDs, attempt numbers — the outcome belongs here; the story of getting there does not.\n")
	b.WriteString("- **Content that isn't yours to keep.** Anything covered by the routing table above (a fact, a result, a suspected platform bug) that ended up here anyway — move it to where it belongs, or raise it as a concern, right now rather than leaving it for later.\n")
	b.WriteString("If you find these, fix them as part of this turn even when your own new observation is small. A file only gets this way because every turn treated cleanup as someone else's job.\n\n")

	b.WriteString("**`")
	b.WriteString(skillPath)
	b.WriteString("` is an index, not a content home.** Each entry is one line: a link to a topic file plus a short description of what it covers. ")
	b.WriteString("Add or correct the line for a topic you changed the scope of; leave the rest alone.\n\n")

	// ---- Size signal (E). ----
	// Deliberately no fixed line count — an index legitimately grows with the
	// number of contributing steps, so a number tuned for a 6-step workflow
	// would be wrong for one with 50. What actually matters is structural: does
	// each entry still read as a link plus a one-line description, or has it
	// become a place where paragraphs of detail accumulated.
	if in.SkillIndexLines > 0 {
		b.WriteString(fmt.Sprintf("> The index is currently %d lines. ", in.SkillIndexLines))
	}
	b.WriteString("**Judge it structurally, not by size:** every entry should be one line — a link plus a short description of what that file covers. If you find a paragraph, a selector, a timing rule, or any other real detail sitting in the index instead of behind a link, that is the defect regardless of how long the index is overall — move it into the owning file and leave a link, as part of this turn.\n\n")

	// ---- Compaction (F). ----
	b.WriteString("**Update in place; do not stack confirmations.** Before adding a new section, read the topic file and check whether an existing entry already covers this behavior. ")
	b.WriteString("If it does, correct or strengthen that entry. Do not append a new dated block restating it — a date is metadata on an entry (`last verified 2026-07-02`), never the identity of one. ")
	b.WriteString("Four entries saying the same thing on four dates is a defect, not a history.\n\n")

	b.WriteString("**Read widely before writing.** Read any file under `")
	b.WriteString(referencesPath)
	b.WriteString("/` — you are improving a shared skill, so what another step already recorded about this surface is directly relevant, and duplicating it is the failure mode to avoid.\n\n")

	b.WriteString("**Write rules:**\n")
	b.WriteString("1. **Read before writing.** `cat` the topic file you intend to change, and the index, to see what is already there.\n")
	b.WriteString("2. **Patch, never rewrite.** Use `diff_patch_workspace_file` for every write, including creating a new topic file. Do not use shell redirection, heredocs, tee, Python, or built-in file-edit tools. Never rewrite the index wholesale — you would destroy other topics' entries.\n")
	b.WriteString("3. **Reconcile what you touch.** Where content you are editing contradicts what this run observed — an obsolete selector, a changed path — fix it in the same patch rather than leaving a \"this may be stale\" caveat beside it.\n")
	b.WriteString("4. **Cross-reference, don't duplicate.** If your lesson overlaps another step's file, name that file instead of copying its content.\n")
	b.WriteString("5. **No ephemeral references.** Session-local handles (`@e1`, `e68`) are meaningless in a later run.\n")
	b.WriteString("6. **No fabrication.** Record only what you actually did and verified. If you are unsure a pattern is reliable, say so in the note.\n")
	b.WriteString("7. **No secrets**, and no owner constraint values — name the constraint, never its number, and raise a concern if you find one copied in.\n\n")

	if in.HasBrowserAccess {
		b.WriteString(BuildBrowserLearningRules())
		b.WriteString("\n")
	}

	b.WriteString("**Your contribution contract for this step:**\n")
	b.WriteString(strings.TrimSpace(in.LearningObjective))
	b.WriteString("\n\n")

	if description := strings.TrimSpace(in.StepDescription); description != "" {
		b.WriteString("**Current step description** (source of truth when reconciling stale guidance):\n")
		b.WriteString(description)
		b.WriteString("\n\n")
	}
	return b.String()
}

func buildReflectionKBSection(in StepReflectionTurnInput) string {
	notesTarget := strings.TrimRight(strings.TrimSpace(in.KBNotesTargetPath), "/")
	if notesTarget == "" {
		notesTarget = filepath.Join(KnowledgebaseFolderName, KBNotesFolderName)
	}
	notesIndex := filepath.Join(notesTarget, KBNotesIndexFileName)

	var b strings.Builder
	b.WriteString("### Knowledgebase — durable business and domain narrative\n\n")
	b.WriteString("You are the sole KB writer for this step; no separate update agent runs.\n\n")
	b.WriteString("**Target:** `")
	b.WriteString(notesTarget)
	b.WriteString("/` with registry `")
	b.WriteString(notesIndex)
	b.WriteString("`. `cat` the registry first to see which topics exist and reuse one rather than creating a near-duplicate. ")
	b.WriteString("Never glob `notes/*.md` — the file count is unbounded.\n\n")
	b.WriteString("Topic ids: entity-scoped narrative uses the entity slug (`company-acme.md`); a cross-cutting pattern uses `pattern-<slug>`. ")
	b.WriteString("Write every change with `diff_patch_workspace_file`, including registry updates.\n\n")
	b.WriteString("Do not write to `knowledgebase/context/` — that store is user-owned.\n\n")

	// ---- Anti-append (PLAT-173). ----
	// The learnings half of this same turn states these duties at length; the KB
	// half stated none of them, and the omission produced exactly the failure
	// they prevent. customer-login's app-structure.md passed every stated
	// threshold because each survey cycle appended a fresh dated section rather
	// than correcting the existing one — the step's own flag named the pattern:
	// "each appending a new dated per-cycle section instead of updating in
	// place". A step that is only told where to write and what to contribute
	// has been given no reason to do anything else.
	b.WriteString("**Update the existing section in place; do not append a new dated one.** Before you write, `cat` and read the whole topic file. ")
	b.WriteString("If a section already covers what you observed, correct or strengthen it — including replacing a claim this run disproved. ")
	b.WriteString("Adding a new dated section that restates, contradicts, or supersedes an existing one without reconciling it is the defect: a reader going top-to-bottom lands on the stale claim as if it were current. ")
	b.WriteString("A date is metadata on an entry (`last verified 2026-07-02`), never the identity of one.\n\n")

	// stores.md told steps that notes "compact themselves when they exceed 20KB
	// or 30 sections". No such mechanism exists anywhere in the platform: the
	// post-step KB agent that once could compact is retired for this path, and
	// no workshop-mode KB skill triggers per-cycle. A
	// step told compaction is automatic has a positive reason to keep appending.
	b.WriteString("**Nothing compacts these files for you.** No automatic pass condenses old sections, and no separate agent cleans up behind this turn — ")
	b.WriteString("if a file you touch has accumulated near-duplicate sections across runs, fold them into one current entry now, as part of this turn, even when your own addition is small. ")
	b.WriteString("Demote genuinely superseded point-in-time observations into a `## Historical context` block, keeping their dates, rather than deleting them outright.\n\n")

	b.WriteString("**Your contribution contract:**\n")
	b.WriteString(strings.TrimSpace(in.KBContribution))
	b.WriteString("\n\nDo not invent facts this step did not establish; partial coverage beats fabricated coverage.\n\n")
	return b.String()
}

// LoadWorkflowDBTableNames lists the workflow's own tables.
//
// Platform-owned bookkeeping (Pulse lifecycle, report human inputs) is filtered
// out: naming those in a step's routing rule would invite a step to write into
// the harness's own records, and they are not where a step's observations
// belong. The point of the list is to show a step that a home for its
// measurement already exists.
func LoadWorkflowDBTableNames(ctx context.Context, workspacePath string) ([]string, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if isPlatformOwnedTable(name) {
			continue
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func isPlatformOwnedTable(name string) bool {
	switch {
	case strings.HasPrefix(name, "pulse_"):
		return true
	case strings.HasPrefix(name, "report_human_input"):
		return true
	// eval_results / eval_route_scores are retired eval tables: steps must
	// never write them, so they stay platform-owned as read-only history.
	case name == "run_concerns", name == "eval_results", name == "eval_route_scores":
		return true
	}
	return false
}
