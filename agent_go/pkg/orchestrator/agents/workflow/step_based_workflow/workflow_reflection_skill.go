package step_based_workflow

const workflowReflectionProcedures = `# Post-run reflection

Use this skill during the reflection turn after the task has passed validation.
The turn supplies the requested contributions and their target paths; it does
not authorize repeating the task, new external actions, or edits to plans/soul.
If there is no useful new guidance, skip edits and say so.

Choose the right store:
- Learnings hold reusable execution technique: verified API behavior, selector
  recipes, timing and recovery patterns. They are one workflow-wide skill,
  organized by topic rather than by step or run.
- Knowledgebase notes hold verified business/domain context, when a contribution
  is requested and write access exists. Open workflow-knowledgebase for topic
  and registry rules; read notes/_index.json first, never glob all notes.
- Counts, costs, statuses and run-specific results belong to output artifacts or
  the workflow database. Refer to existing evidence rather than caching these in
  learnings. Check files or permitted database reads before stating results;
  a table's existence does not prove that an observation was recorded.
- Keep defect evidence with the step result for review. Never copy secret values,
  personal credentials or owner constraint values into reusable notes.

Read the target index and relevant topic files before editing. Use an existing
entry when it covers the behavior; correct or strengthen it rather than stacking
another dated confirmation. Reconcile stale or duplicate guidance in files you
touch. Preserve relevant historical business observations as historical context.
Keep the work scoped to the requested contribution.

For learnings, SKILL.md is an index with standard frontmatter and one linked topic
plus a short description per entry. Detail belongs in references/<topic>.md.
Choose names by subject, not step ID. Fold relevant old step-specific notes into
the owning topic and fix its index link without losing their useful content.
Update the frontmatter description only when its scope changes.

Use diff_patch_workspace_file for every write, including new topics. Do not
rewrite shared files wholesale or write with shell redirection, heredocs, tee,
Python or native file-edit tools. Re-read the changed section to verify it.

For browser lessons actually established in this run, retain semantic locators,
page conditions, timing/auth quirks and verified postconditions. Never retain
snapshot handles such as @e1, generated selectors, credentials or account data.
Open agent-browser for more detail only if relevant; do not browse merely to
fill in reflection notes. Label unverified ideas as candidates.

Finish with a short account of what changed and the affected files, or why no
new guidance was saved. Recording a note is not an external delivery receipt.
`
