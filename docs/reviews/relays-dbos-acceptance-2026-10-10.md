# Relays DBOS integration acceptance — 2026-10-10

The actual Relays server, workspace sandbox, scheduler ledger and frontend were
run locally on ports 18771, 18772 and 51734. The existing developer stack was
left running separately. This was a local acceptance run, not a deployment.

Published Relay: **DBOS · Live order verification**, ID `wf_8d67b498`, version
`v1`, release hash
`2a17ea5a1f501f3d932054a3d1acdeb7f89f36a98870bab4ae43a576e60d6f2a`.
Created, configured, published and invoked through the actual authenticated HTTP
APIs. Its first real Codex CLI agent called the authored Python `lookup_order`
tool to obtain an invocation-specific random verification code. A safe wait
step provided a recovery window; a second real Codex agent reviewed the saved
order. No model or bridge fixture was used in these two runs. The lookup was a
local test service with a receipt file, not a third-party business API.

| Fault | Canonical run ID | Result |
| --- | --- | --- |
| SIGKILL admitted Python executor after lookup checkpoint | `c67ecf28-a072-5102-bddb-09ead108080b` | Completed on attempt 2; lookup checkpoint reused; one tool receipt; both agents returned the original code |
| SIGKILL backend after lookup checkpoint, restart against the same workspace and ledger | `74d022c4-77a2-5e3a-9ffb-528a0cc585a4` | Old executor expired through its coordinator lease; same invocation and run folder recovered on attempt 2; one lookup receipt; original code preserved |
| Repeat Python SIGKILL after the mailbox timing fix | `249fdfa4-c1dd-5f2b-b6e3-29f84c2bd705` | Completed on attempt 2 with the saved lookup and one tool receipt; result verified in the actual Runs pane |

The browser verified the actual Graph, published version selector, Runs output,
attempt counter, reused-checkpoint badge, Python tool receipt and final JSON.
Identity → General showed the enabled Crash recovery setting.

Automated verification passed:

- `go test -race ./pkg/relaypython ./pkg/schedulerstate ./cmd/relay-dbos-demo -count=1`
  with DBOS 3.2.0 installed and `RELAY_DBOS_PYTHON` set.
  Includes a forced process exit during an in-flight mailbox read, preserving
  recoverable interruption instead of incorrectly reporting cancellation.
- Focused actual server/sandbox DBOS, Python Relay, capacity and publication
  access regressions. Publishing rejects readers and insufficient token scopes.
- `go vet` for the adapter, ledger and standalone lab.
- Frontend Runs/Graph, recovery setting, Relay intro and input sidebar tests.
- Production TypeScript sources checked successfully. The unfiltered project
  check has two existing unrelated test errors in
  `WorkflowLLMConfigurationPanel.test.tsx` (missing `workspacePath`).

Limits: opt-in deterministic Python, one host, per-invocation SQLite, three
process attempts and original one-hour deadline. In-flight effects remain
uncertain unless every possible effect is explicitly safe to repeat. Application
errors, stopped runs and unsafe uncertain operations are not automatically
retried. Docker image changes were inspected but images were not built here.
Distributed Postgres workers and external business-service acceptance remain
separate work.


## Native DBOS authoring and execution logs

The same actual Relay was published as v2 using only `from dbos import DBOS`,
native @DBOS.workflow / @DBOS.step and the thin agentworks module. No # @relay
comments or custom workflow/step decorators appear in this source. v2 hash:
`10a76ff518d0bb05df37255bd9911b637add4cb3df1053c945cb0c2479d6c276`.

| Fault | Canonical run ID | Result |
| --- | --- | --- |
| Native Python SIGKILL after verify_order checkpoint | `bbea76b1-b549-5cc9-8e0d-09c13d34114f` | Completed on attempt 2; verify_order reused; real Codex agents and lookup tool; lookup receipt once; original code returned |
| Native backend SIGKILL/restart | `7a9d18e2-3e4e-5d4e-be82-03327c1f131f` | Completed on attempt 2 in the original folder; DBOS steps verify_order, DBOS.sleep and review_order; two reused checkpoints; one lookup receipt |

The first restart test exposed a cached .webhook-result.json from the temporary
interrupted state. The canonical ledger and DBOS had completed, but the API still
returned the stale interruption. Cached terminal snapshots now require matching
canonical run ID, state and completion timestamp. A regression exercises an
interrupted snapshot followed by successful recovery and another read.

The browser verified the AST-derived source overview, native Code, and actual
Execution Logs → v2 → DBOS steps. It displays authoritative DBOS step IDs and
timing, attempts and reused checkpoints, with linked agent/tool receipts.
Expandable DBOS.logger events include workflow, step and attempt metadata. Agent
files remain available for existing artifacts and legacy runs.

Automated verification additionally passed native checkpoint recovery, uncertain
agent/tool refusal, DBOS automatic retries preserving the uncertain-call guard,
AST validation without imports, native publishing/recovery through the actual
workspace sandbox, live revocation, and 14 frontend graph/log/release-selection
tests. Go race checks and go vet passed. The full TypeScript project still has
only the two unrelated missing-workspacePath test errors noted above.

New Relays now use native DBOS by default. Existing Context-based programs remain
compatible. In-flight ordinary native service steps can replay; service writes
need stable idempotency keys and service-side deduplication. Uncertain agent/MCP
bridge calls stop by default. Native platform calls currently require a step of
the root workflow; child-workflow platform calls are unsupported. Imported or
dynamic steps may be absent from the static source overview, while the run graph
comes from actual DBOS history. Current SQLite execution remains single-host and
is not a distributed production deployment.

A fresh backend restart with the snapshot fix passed as canonical run
`060f15a1-e38d-5e41-94bb-83ae21e9ae49`: completed API response on attempt 2,
original verification code, one lookup receipt, and correlated native logs.


## Live browser recovery with real Muse agents

The actual local Relay was published as v3 with both agent calls configured for
Muse CLI and invoked using synthetic order muse-order-8842. Release hash:
`8d91d8910da4d7b82553137210db6802ba7c70690c7603b4cee6a8cf16f7939a`.
Canonical run: `1329e4d2-9342-5dce-9b2e-9125f2b8eef4`, folder
`iteration-1-hook`. The signed-in Muse CLI ran the real lookup and review agents;
no model fixture or response stub was used. The Python lookup is a local test
service with a receipt file, not an external business service.

The browser displayed attempt 1 and live DBOS.logger events for agent startup,
the Python tool call and the saved checkpoint. Only the admitted Relay Python
PID was killed after the lookup checkpoint. Without a browser refresh, the same
run updated to attempt 2, with verify_order and DBOS.sleep checkpoints reused.
The second real Muse agent reviewed the saved order. The completed API result
preserved the exact original random verification code; the lookup receipt file
contained exactly one line. The actual Execution Logs pane was left open on
v3 / iteration-1-hook, showing completed / attempt 2 and both Muse steps.

Newest log events now render first so live progress is visible above startup
messages. The four focused execution-timeline and release-selection tests pass.
Screenshots of before crash, recovery in progress and completion are saved under
cache/relays-app as muse-before-crash.jpg, muse-recovering.jpg and
muse-recovered-completed.jpg. The result and bounded verification script are in
that same local acceptance directory.

## Crash while real Muse is running

Published v4 of the same local Relay, hash
`9240615b5bc8c94d2ffde47e1f3e0853f76bd9052d6dce64fc3af02d41b18b01`.
Canonical run `293ffabe-4773-5d1f-9f0f-b236e7f09766`, folder
`iteration-1-hook`, uses the actual signed-in Muse CLI. Its local Python lookup
tool writes and fsyncs a receipt, then waits 120 seconds before returning. This
controlled pause allows a crash after a real tool action but before the agent
result and DBOS step checkpoint. No real business service is changed.

The browser showed attempt 1, the tool-start event at 18:42:53, and no completed
DBOS step. SIGKILL was sent only to this Relay's Python PID 57381 at 18:43:09.
The supervisor recovered the same workflow on attempt 2. The durable platform
intent guard refused the uncertain agent call with `Uncertain platform call
requires reconciliation; automatic retry is disabled`. Canonical API status is
failed; relay_durability.json records reconciliation. The DBOS step verify_order
failed, review_order never ran, and no relay_result.json exists.

Verified exactly one tool receipt and one native call intent. The original IPC
attempt contains the lookup agent request; the recovered IPC attempt has no
request files, proving that no second platform call was admitted. The authored
pre-call log appears again on attempt 2 because DBOS re-enters verify_order; it
does not mean Muse was invoked again. The original agent closed and its exact
Muse tmux session no longer exists.

Execution Logs now shows Needs reconciliation with the actionable reason and
keeps the full traceback in expandable details. Five focused timeline and
release-selection tests passed. The browser is left on v4 / iteration-1-hook.
Local evidence is saved in cache/relays-app/muse-inflight-demo.json,
muse-inflight-result.json, muse-inflight-before-crash.jpg and
muse-inflight-reconciliation.jpg; muse-inflight-demo.py provides the bounded
test and verification commands. This validates safe refusal of an unfinished
agent call, not resumption of Muse's internal turn or automatic reconciliation.

## Automatic Muse restart with durable tool results

Native `agent(..., recovery="restart")` now journals each Python tool operation
by tool name and normalized JSON arguments within that agent call. It writes a
durable intent before execution and a durable result before responding. A fresh
agent session gets the saved result for repeated identical requests. A tool can
provide `@tool(recover=lookup_receipt)` to recover the original result of a pending
operation without performing the action again. All pending tools must resolve
before a new agent session starts; missing or unsuccessful recovery lookups stop
for reconciliation. Uncertain tool errors terminate the agent instead of being
returned to the model as ordinary tool errors. A completed agent result is also
saved before returning to its outer DBOS step.

Restart mode rejects attached MCP servers, skills and replay_safe. Generic
platform code execution is disabled; read-only tool inventory helpers remain.
The protection applies to declared Python tools with identical arguments, not
arbitrary native CLI activity or semantically similar actions with different
arguments. Remote writes still require stable business operation IDs and
service-side idempotency or a reliable receipt lookup. The adapter does not
resume Muse's internal reasoning turn.

Actual local Relay v7, hash
`61c5ff25cbc390aa4be246ddfa99589b15705c2cb17c49c4341ea44167541f68`,
passed as canonical run `87471394-98c2-5aa7-9464-99b0861341cc`, folder
`iteration-1-hook`. Both lookup and review used the signed-in real Muse CLI.
The synthetic local lookup service wrote and fsynced one action receipt and its
original response, then paused for 120 seconds. After the browser showed the
tool in flight with no completed DBOS step, only Relay Python PID 66034 was
killed. Recovery used the service-result lookup to resolve the pending tool,
started a fresh Muse lookup session, and returned the saved result when that
session called lookup_order. The run completed automatically on attempt 2,
including review, preserving the exact original random verification code and
accepted=true. The receipt file contains one line; the tool body never returned
from its interrupted pause. IPC records show two lookup agent admissions and
one review admission, while the durable tool journal contains one completed
operation. The browser displays completed / attempt 2 and the recovered/reused
tool log events.

Live testing also found that bridge HTTP callback contexts could outlive the
agent's cancelled context. Python callbacks are now bound to both lifetimes.
The old v7 callback's final mailbox poll was 0.67 seconds before SIGKILL, with no
polling after the crash. Cancellation and capability-admission server tests
pass, including the cancellation race check. The full runtime package passed
with the race detector, including completed-tool reuse after process death,
pending-tool recovery, refusal without recovery, and existing native recovery
guards. Actual workspace/server native checkpoint recovery and go vet passed.

Earlier v5 and v6 runs successfully restarted the lookup agent with one action,
but failed on the separate review JSON-output contract. The final test adds the
JSON request explicitly to the review user message and keeps output validation
strict. Their immutable release/run artifacts and saved local state remain for
inspection.

Evidence is in cache/relays-app/muse-auto-recovery.py,
muse-auto-recovery.json, muse-auto-recovery-result.json,
muse-auto-before-crash.jpg, muse-auto-restarting.jpg and
muse-auto-recovered-completed.jpg. The browser is left on v7 / iteration-1-hook.

## Isolated pull request validation

The PR was isolated from unrelated workspace edits and rebased onto current main.
The four focused frontend files passed all 13 tests, and the full app TypeScript
check passed on that base. Pre-commit security checks and a redacted Gitleaks scan
of the PR commit passed. The earlier TypeScript test failures above describe the
original acceptance checkout; they are absent from the updated PR base.
