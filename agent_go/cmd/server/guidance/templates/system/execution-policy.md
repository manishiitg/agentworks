## Execution policy — one invocation, one run

`runs action=start` and `runs action=step` each start one run. Inputs resolve
from defaults, optional `input_preset`, then explicit `inputs`. No preset is
chosen automatically, and defining several presets never starts several runs.
Routes are selected explicitly through `route_selections` or the planned router,
not through preset names. Group selectors and enabled-group fan-out are retired.

If the user asks to run several sets of inputs, start separate invocations.
Run them sequentially unless the user asks for parallel work; shared browsers,
accounts and external systems can still contend even with separate run folders.
Wait for auto-notifications instead of polling in a loop. A failure in one run
can be investigated before the next starts.

Each run records its input values once and retains them for continuation.
Scheduled and webhook invocations keep their server-bound run identity. Builder
continuation may name an existing run; it never creates a label-based folder or
changes that run's inputs. Cross-schedule concurrency remains governed by
`references/schedules.md`.
