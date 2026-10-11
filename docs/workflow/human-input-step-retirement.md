# Retiring human_input steps

The five workflow step types are `agent`, `scripted`, `routing`, `branch` and
`crew`. The dedicated `human_input` type, its authoring tools, executor and canvas
node are removed. A remaining saved record fails with a migration error.

Human interaction remains available:

- Supply known values through declared run variables or typed function arguments.
  Agent descriptions render `{{VARIABLE}}` from those values. Declare
  `required_variables: ["MONTH"]` on the first consumer; blank or missing values
  stop a known selected path before work starts. Direct and dynamically reached
  consumers enforce the same requirement at their execution boundary.
- Supply operator instructions with `execute_step(human_input=...)` or
  `run_full_workflow(human_inputs={"step-id": "instructions"})`. They affect only
  that executable step, including delegated route agents; they do not rewrite
  its saved description or create an input step.
- Agents call `human_feedback` for urgent input such as CAPTCHA or OTP. Its
  bounded wait resumes the calling agent with the answer.
- Use `create_human_input_request` for a durable decision that can wait, and
  `branch` with `route_source: "human"` for a planned fixed choice.

## Saved-plan migration

This needs a data migration before starting the new build. Removing a question
node can change route targets, output-file dependencies and variable references.
There is deliberately no automatic replacement that guesses answers, approves
work or inserts a new agent for every old question. Review the complete affected
plan and its consumers first. Keep credentials in the existing secret facilities.

Prepare one recipe JSON for every affected live plan, at the same relative path
under a recipe root. It contains the original plan's byte `source_sha256`, the
reviewed replacement `plan`, an optional replacement `step_config` array or object with `steps` (guarded by `step_config_sha256`) and
optional `variables_add` definitions with only `name` and `description`.
The migration preserves existing variable values and groups; new definitions
start empty. Recipes must never carry secrets.

```sh
python3 scripts/migrate_human_input_steps.py /path/to/workspace-docs \
  --recipes /path/to/reviewed-recipes
# Drain executions and every plan writer before applying:
python3 scripts/migrate_human_input_steps.py /path/to/workspace-docs \
  --recipes /path/to/reviewed-recipes --apply --server-drained
```

The job validates all candidates before writing, refuses changed source plans,
symlinks and dangling next-step references, backs up replaced files and uses atomic
file replacement. A second successful run makes no changes. Historical runs,
published releases, saved versions and backups retain their original records.
Historical log events remain displayable. This deployment migration does not
stamp or bypass unrelated workflow contract upgrades.

After migration, check every affected route, declared input, artifact consumer,
script argument and schedule/function entry point before resuming executions.
See [human feedback](human_feedback_system.md) and
[communication protocols](../communication-protocols.md) for the runtime tools.
