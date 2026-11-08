# Questions, decisions and workflow inputs

Crew conversations and workflow runs have different jobs. Use the Crew agent when a person needs to explain a request, choose an option or review a proposed change. Use a workflow for repeatable work that already has its inputs.

## Crew conversations

An attended Crew chat or shared Ask conversation can use its offered clarification or question cards. Answering records the person's choice. The agent must apply any requested change explicitly and verify it; an answer alone is not proof that a change or send occurred.

Keep the person's own conversation and current sharing boundaries. A message from another agent is attributed to its sender, and does not become owner approval or control of another person's tab. Follow the tools actually offered for an unattended agent turn when a person is unavailable.

## Workflow runs

A Python workflow never waits for a question mid-run. Declare necessary values in `script.json`, with types, required fields, defaults and named presets. If a required value or connection is missing, stop with a clear reason rather than creating a waiting plan step.

For work that depends on an earlier human decision, save that decision through the Crew's existing authorized tools and pass or read the precise values the workflow requires. Preserve its normal approval and identity checks. Do not manufacture an answer, broaden access or replay an uncertain external action.

The workflow can notify the owner of progress or a problem, and may send business messages when sending is part of the requested job. Those are explicit effects. The run record remains its summary; no automatic owner email or digest is implied.

## Continue after a decision

Review the saved result in Runs, discuss the missing value with the Crew agent and start the appropriate whole workflow when its inputs are ready. A `@step` label is not a standalone run or resume target. Keep earlier failed and partial evidence so the person can see what actually happened.
