package step_based_workflow

import (
	"path"
	"strings"
)

// BuildStepKBGuidance returns the KB contribution guidance to splice into a step
// agent's system prompt when it's responsible for writing KB itself.
//
// Notes-only KB model: the knowledgebase is a set of per-topic markdown files
// under knowledgebase/notes/ plus notes/_index.json as a registry. Permitted
// content writes go through diff_patch_workspace_file.
//
// Returns no contract without a write grant. Targets, patch-only restrictions
// and the declared contribution remain binding context; topic and registry
// procedures live in the workflow-knowledgebase skill.
func BuildStepKBGuidance(kbAccess, kbContribution string) string {
	return BuildStepKBGuidanceWithTarget(kbAccess, kbContribution, "")
}

func BuildStepKBGuidanceWithTarget(kbAccess, kbContribution, notesTargetPath string) string {
	if !kbAccessAllowsWrite(kbAccess) {
		return ""
	}

	notesTarget := strings.TrimRight(strings.TrimSpace(notesTargetPath), "/")
	if notesTarget == "" {
		notesTarget = "knowledgebase/notes"
	}
	notesIndex := path.Join(notesTarget, KBNotesIndexFileName)

	var b strings.Builder
	b.WriteString("\n### Knowledgebase contribution\n**Target:** `" + notesTarget + "/` plus registry `" + notesIndex + "`. Use these exact paths; do not rely on your shell working directory.\n")
	b.WriteString("Open the workflow-knowledgebase skill for topic and registry procedures. You are the direct writer. Use diff_patch_workspace_file for every KB content write, including new topic files; do not edit context/ or erase earlier notes. Do not use shell redirection, heredocs, tee, Python or built-in file editors for KB writes.\n")
	if contribution := strings.TrimSpace(kbContribution); contribution != "" {
		b.WriteString("For this step, contribute: " + contribution + "\n")
	}

	return b.String()
}

// BuildKBContributionReviewMessage returns the one-shot user message injected
// after a step's first successful completion in direct-write mode, asking the
// step agent to verify its KB contributions against the author's contract.
//
// Returns an empty string when a review is not warranted (kbAccess doesn't
// permit writes, or contribution is empty). This is the final
// turn for KB work on the step — no self-nudging loop.
func BuildKBContributionReviewMessage(kbAccess, contribution string) string {
	return BuildKBContributionReviewMessageWithTarget(kbAccess, contribution, "")
}

func BuildKBContributionReviewMessageWithTarget(kbAccess, contribution, notesTargetPath string) string {
	if !kbAccessAllowsWrite(kbAccess) {
		return ""
	}
	trimmed := strings.TrimSpace(contribution)
	if trimmed == "" {
		return ""
	}
	notesTarget := strings.TrimRight(strings.TrimSpace(notesTargetPath), "/")
	if notesTarget == "" {
		notesTarget = "knowledgebase/notes"
	}
	notesIndex := path.Join(notesTarget, KBNotesIndexFileName)

	var b strings.Builder
	b.WriteString("## Knowledgebase Contribution Self-Review (final turn)\n\n")
	b.WriteString("The step's output validation passed — before it's accepted, verify you fulfilled your `knowledgebase_contribution` contract.\n\n")
	b.WriteString("**Target:** `")
	b.WriteString(notesTarget)
	b.WriteString("/` plus registry `")
	b.WriteString(notesIndex)
	b.WriteString("`. Use these exact paths; do not rely on cwd.\n\n")

	b.WriteString("**Enumerate what you contributed:**\n")
	b.WriteString("- Topics you wrote or updated under `notes/` (list the markdown filenames and which sections you added).\n\n")

	b.WriteString("**Compare against the contract.** If anything required is missing, use `diff_patch_workspace_file` under the target folder to add it now, and update `")
	b.WriteString(notesIndex)
	b.WriteString("` accordingly. Do not use shell redirection, heredocs, tee, Python, or built-in file-edit tools for KB writes. If every requirement is already covered, reply with a short summary of what you contributed — no further tool calls needed.\n\n")

	b.WriteString("**Contract:**\n")
	b.WriteString(trimmed)
	b.WriteString("\n\n")

	b.WriteString("**Important:** this is your final turn for KB work on this step. After this response, the step will be accepted regardless of any further gaps — there is no second review. Do not invent facts the step did not actually establish; partial coverage is better than fabricated coverage.\n\n")

	b.WriteString("**Raising a concern.** If, while reconciling your contribution, you find that a KB note contradicts what this run actually observed, that the same fact is recorded differently in the KB versus `db/`, or that a durable note restates an owner constraint value (those live in `soul/soul.md` and must never be copied into the KB), report it with a line in this exact form before your summary:\n")
	b.WriteString("`CONCERNS: <what contradicts what, naming both sources and the evidence path>`\n")
	b.WriteString("Fix what you own (the notes you write) and report the rest rather than leaving a \"this is stale\" caveat next to the stale content.\n")

	return b.String()
}
