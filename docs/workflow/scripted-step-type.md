# Scripted workflow steps

Use `type: "scripted"` for deterministic work implemented by a saved script.
`ScriptedPlanStep` is the canonical Go shape; `agent` is the conversational
step. Builder tools, predefined routes, orphan definitions and the canvas use
these same names. The saved-plan reader accepts the retired `regular` spelling
and subsequent saves write `scripted`. The saved step type determines execution;
there is no separate execution-mode override.

## Deployment migration

This naming change does not need another workflow contract upgrade or a Builder
model turn. Read compatibility makes deploying the new binary sufficient.
To normalize persisted plans too, drain the server, back up its document roots,
then run the deterministic job from the deployed revision:

```sh
python3 scripts/migrate_scripted_step_types.py /path/to/workspace-docs
python3 scripts/migrate_scripted_step_types.py /path/to/workspace-docs --apply
```

Pass all relevant document roots, including user-specific roots. Review dry-run
output first. The job walks live `planning/plan.json` files, including orphan
definitions and nested predefined routes, and changes only structural step type
values. It preserves descriptions, schemas, configuration and unknown fields.
It validates all JSON before writing, saves a backup, atomically replaces each
changed file and is idempotent. Run snapshots and workflow contract markers are
left intact. A partial I/O failure can be retried after fixing it.

The previous agent shape migration is also deterministic (`migrate_agent_steps`),
but its v1.0.47 contract checkpoint is separate. Do not stamp old workflows as
current merely because their step names were normalized: they may still owe
earlier schema or behavioral upgrades.
