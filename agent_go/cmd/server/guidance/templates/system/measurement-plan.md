## Measurement through ordinary workflow steps

The owner decides what success means. Measure it in an ordinary workflow task and save the result in that workflow's database; the dashboard reads those saved results. No separate collector, Goal engine, metric API or required schema is needed.

Inspect existing sources and producer contracts before changing anything. Reuse an appropriate step's measurements and existing tables. Add an ordinary measuring step only when no existing step can own the work. Routes and the number of outcomes do not force a topology change.

A useful saved reading retains its definition, exact unit/scope, source method, actual observation time, source-run identity and evidence. Period-based comparisons need actual compatible window bounds. Preserve existing IDs and historical records; a changed measurement meaning must not rewrite an old series. An iteration folder is not a source-batch identity.

Use the normal workflow database tools. Retries read back the same source-run records, append missing results and report conflicts; do not overwrite history. Numeric zero means a measured zero. Missing, stale, blocked or incompatible data remains explicitly unavailable. Never invent a target, observation time or reporting period.

Compare only matching methods, units, scope and supported windows. Separate business outcomes from supporting activity and diagnostics. A difference does not establish attribution or causation. Verify source evidence before saying that progress is real.

Collection failure must not erase successful business work. Keep the task's existing partial-result behavior and permissions. Do not take extra actions merely to obtain measurements. Keep retired evaluation files and ledgers as historical evidence; new runs do not depend on them.
