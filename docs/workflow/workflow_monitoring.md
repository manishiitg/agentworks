# Workflow results and logs

Open a workflow inside its Crew, then choose Runs. Select the execution you want to inspect. Each run retains its inputs, activity, named steps, checks, files, logs and available usage.

## Judge the saved result

Queued or started means the server accepted work. A completed process or agent turn is not proof of a useful result. Read the workflow's checks and required outputs, including partial or unavailable evidence. A failed check remains visible; Python decides whether to stop.

Steps are labels for Python functions, conditions and loops. They help locate what happened in that run. They are not independently runnable plan nodes. To fix work, change the workflow through its Crew and start a new run when appropriate; keep the earlier evidence.

## Inspect without running again

External MCP and the CLI expose `runs action=get`, `logs` and `status`. Keep the workflow ID for every call. Status also needs the session ID returned by the start receipt; get and logs use that workflow's saved run folder. A workflow started through `functions action=run_trigger` can also be checked with its `trigger_status` receipt. Read the current catalog for the precise fields.

`runs action=logs` supplies bounded stdout/stderr, failure tracebacks, saved inputs, step timings and agent answer/provider-log locations. For an agent turn, read its full answer file instead of assuming the bounded text preview is complete. Provider logs contain only the tool and text events the platform actually captured.

Use the returned file locations. Parent workflow outputs and native agent files may have different granted locations inside one run; do not assume an old iteration/group/step path or substitute another run's output. Quote only the relevant lines and keep secret values and private conversations out of the report.

## Repeating work

A rerun uses the current source with the earlier run's saved inputs and preset, and reports source changes. It starts a real new execution. Database writes, app calls and sends can happen again; do not rerun an uncertain start or an external action merely to read its logs.

A native agent output can receive one correction turn in the same conversation when the workflow checks it. Preserve completed effects and original observation times during that correction. A missing connection or declined action remains a clear limitation.

## Summary and usage

The run record is the summary. Business email or Slack delivery is an explicit workflow action, not an automatic owner digest. Available token and cost records are evidence for reviewing that execution; absence of captured usage is not proof that no work happened.
