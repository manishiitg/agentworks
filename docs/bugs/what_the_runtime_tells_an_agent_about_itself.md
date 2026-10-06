# Bug Report: What the Runtime Tells an Agent About Itself

This document is organized as an **index of individual defect tickets**
(#1–#9 below), in the spirit of `docs/bugs/README.md`'s one-file-per-defect
convention. For now every ticket lives as a section inside this single file
rather than its own file — that split has not yet been coordinated with other
contributors to this archive. When it is, each numbered section can move to
its own `docs/bugs/<slug>.md` without changing its content; this file then
becomes a thin index pointing at them. Do not create those per-ticket files
yet.

## Index

Eight agent-contract defects were implemented and their targeted tests passed on
2026-08-04. An independent code review later that day accepted fixes #1 and #8,
accepted the core direction of #2, #4, and #7 with follow-ups, and found material
gaps in #3, #5, and #6. The per-issue review notes below are part of the current
status; this document must not be read as saying all eight are fully closed.

**Read the Status column, not the Commit column.** A commit SHA means work was
done, not that the defect is closed — the review verdict is what decides that.
On 2026-08-05 #5 was re-encountered in production and re-diagnosed from scratch,
because this table showed it with an author and a SHA while the word
*incomplete* sat two hundred lines below in prose. Status values are `closed`
(review accepted), `partial` (direction accepted, named follow-ups outstanding),
and `open` (material gaps; treat as unfixed).

| # | Status | Where | Author | Commit |
|---|---|---|---|---|
| 1 | **closed** | `mcpagent/agent/codeexec/registry.go` — denial named a cause it cannot know | Claude Code | `a2c225d` |
| 2 | **partial** | `mcpagent/agent/codeexec/shell.go` — test fixture posing as the platform shell | Claude Code | `a2c225d` |
| 3 | **open** | `interactive_workshop_manager.go` — grants missing the tools the prompts promise. Review: *incomplete / safety regression* | Claude Code | `7eef64150` |
| 4 | **partial** | `pkg/workspace/advanced_tools.go` — tool description asserted the wrong cwd | Claude Code | `7eef64150` |
| 5 | **partial** | `pkg/workspace/execute_shell_command.go` — no output cap; unreadable spill files. Re-encountered 2026-08-05 via `read_skill`; see [tool_result_spill_outside_workspace.md](tool_result_spill_outside_workspace.md) | Claude Code | `7eef64150` |
| 6 | **open** | `cmd/server/pulse_worklist.go` — a mandated pre-check answered as a fault. Review: *incomplete* | Claude Code | `6f4737cc9` |
| 7 | **partial** | `pkg/agentwrapper/llm_agent.go` — deliberate re-registration became fatal | Claude Code | `b4402bcef` |
| 8 | **closed** | `mcpagent/agent/codeexec/registry.go` — a removed tool read as a withheld one | Claude Code | see #8 below |
| 9 | **partial** | `cmd/server/pulse_worklist.go` + `pulse_finding_lifecycle.go` — one finding, four sequential rejections | Claude Code | `fdd54c089` |
| — | **open** | [Follow-up](#follow-up-the-global-header-can-hide-the-current-running-workflow): global header can hide the current running workflow | Claude Code | not fixed |

Everything in this file to date was authored by **Claude Code**. Other
contributors to this archive — for example **Codex** — should add their own
name to the Author column and to any ticket they write, rather than leaving it
implied.

Still open, recorded below: the unbounded `stdoutBuf` in the workspace handler,
the empty `tool=` field on some CLI payload markers, and the global activity
header hiding the currently selected workflow's running state. The last item is
a user-facing runtime-visibility follow-up, not one of the eight agent-contract
defects above.

## How these were found

`grep '\[TOOL_ERROR\]' agent_go/logs/server_debug.log` — the marker added by
[tool_failures_invisible_in_backend_logs.md](tool_failures_invisible_in_backend_logs.md).
One retained window, 2026-08-04 03:41–09:19 (5h37m), **137 markers**. #7 came
from schedule.log instead — it kills the agent before any tool runs, so it leaves
no tool marker at all:

```text
40  <unattributed CLI payload failure>
19  query_workflow_db            SQL syntax — agent error, correctly rejected
18  record_pulse_result
14  execute_shell_command        oversize payload
 9  execute_shell_command
 8  record_pulse_worklist        schema mismatch, repeated within one run
 6  get_pulse_state
 6  diff_patch_workspace_file
 5  get_api_spec
 4  update_schedule
 4  agent_browser                JS eval — agent error, correctly rejected
```

That the whole scan took one grep is the point of the marker. Everything below
was invisible before it existed.

## The shared shape

`docs/bugs/README.md` already names the family: **one fact, two sources, and
nothing checking they agree.** Four of these eight sharpen it into something more
specific and more damaging:

> **The runtime told the agent a cause the code had evidence against — and in two
> cases evidence it had just produced.**

An agent that receives a wrong error does not stop. It acts on the hint. When the
hint names a *switchable* condition — a mode, an identity, a path — the agent
spends its next calls trying to switch it, and the calls cannot succeed.

---

## 1. A denial that named a mode the registry has never heard of

**Symptom.** A Pulse Fixer stage called `update_schedule` four times and was
refused each time with:

```text
tool "update_schedule" is not available in the current workshop mode
```

It then wrote into its own report:

> *"The Pulse Fixer session is not Workshop mode."*
> *"update_schedule refused: 'not available in the current workshop mode'. No manual workflow.json edit was made; sha256 unchanged"*

and abandoned the edit.

**Root cause.** `CallCustomToolWithSession` returned that string for *any*
allow-list miss. The list it reads is a generic per-session tool-access policy
written by `Agent.setToolAccess` and `turn_session.go`; `codeexec` has no concept
of workshop mode. The message asserted a cause the code cannot know, and named
one the model believes it can change.

**Fix.** `toolNotAllowedError` states the true condition, says plainly that
retrying or switching anything will not help, gives the next action, and names
the allowed surface (sorted, capped at 30). It mirrors
`Agent.unavailableToolsError`, which learned this lesson on 2026-08-01 for the
`get_api_spec` path but never covered the bridge path.

**Independent code review, 2026-08-04 — accepted.** The denial now describes
the actual allow-list state without inventing a workshop mode, and the registry
lookup path supports the distinction used by #8. No material issue was found in
this fix.

---

## 2. A test fixture that read as the platform's shell

**Symptom.** An audit of shell output limits found `maxOutputBytes = 100 * 1024`
in `agent/codeexec/shell.go` and concluded the platform capped shell output at
100KB. It does not. The live path was **uncapped**, and a coding CLI rejected
twelve results of 67,930–130,046 characters that same morning.

**Root cause.** `codeexec.ExecuteShellCommand` had **zero production callers** —
every reference was one of seven mcpagent bridge e2e tests, which need a
registrable `execute_shell_command` so the bridge has something real to call. It
sat in `codeexec` beside the live tool registry, exported, with a doc comment
reading like platform infrastructure.

The real path is a separate deployable service:

```text
agent → execute_shell_command
      → workspace.NewAdvancedExecutor        (pkg/workspace/tools.go)
      → Client.ExecuteShellCommand           (HTTP)
      → POST /api/execute
      → workspace/handlers/shell.go          (own go.mod, own Dockerfile)
      → security.Isolator                    (sandbox-exec / bind mounts)
      → exec.Command
```

The split is correct and must stay: shell execution belongs in the service that
owns the docs root and the sandbox. The fixture has neither.

**Fix.** Moved to `agent/codeexec/shellfixture` with a package comment recording
why, plus `TestNoProductionCodeImportsTheShellFixture`, which parses every
non-test file in the module and fails if any imports it. Verified failing on a
planted import.

`ShellCommandParams` and `ShellCommandDescription` stayed in `codeexec` — they
are production, used by `coding_agents_bridge.go:277` when a host has not
registered its own definition. `codeexec.BuildSafeEnvironment` was dead and is
gone; the tests use the identically-named one in package `agent`.

**Worth keeping:** a test-only helper in a production package is not a naming
nit. It is a fact that will be believed by the next person auditing that subject.

**Independent code review, 2026-08-04 — direction accepted; enforcement is
module-local.** Moving the fixture out of the production-looking path resolves
the misleading ownership signal. However,
`TestNoProductionCodeImportsTheShellFixture` scans non-test files only inside the
`mcpagent` module. Because `shellfixture` remains an exported Go package, a
downstream production module can still import it and the guard will not fail.
Either make the fixture structurally test-only, or add an architecture check
covering the known downstream modules. This is an enforcement gap, not evidence
of a current production caller.

---

## 3. Grants that withheld what the prompts promise

Two separate drifts, both found by pulling on #1.

**`get_api_spec` was withheld from every stage agent.** The allow-list gates the
code-execution bridge and is checked *before* `get_api_spec` reaches its
virtual-tool partition, so none of `goalAdvisorCommonMutation`,
`FinalizerProposal`, `FinalizerApproved`, `ReadOnly`, or `pulseFixerStage` could
discover which tools it had. That is what made #1 unrecoverable: the Fixer had no
way to check its own surface, so a denial could only be guessed at.

**The Fixer was told to repair schedulers without schedule tools.**
`pulse-fixer-practices.md` gives it a *"Scheduler and lifecycle repair"* section.
`pulseFixerStageToolAgentAllowedToolNames()` granted none of the schedule tools.
The instruction was unfollowable, and it burned four calls discovering that.

**Fix.** `get_api_spec` added to all five surfaces — pure discovery, granting
nothing the list does not already grant. `list_schedules`, `get_schedule_runs`,
`update_schedule`, `trigger_schedule` added to the Fixer; `create_schedule` and
`delete_schedule` deliberately withheld, since reshaping the run surface is a
Workshop decision, not a bounded repair.

Two drift guards in `workshop_allow_list_test.go`, verified failing without the
grants. They join the existing `list_llm_capabilities` guard, which was written
for the same drift in the same file.

**Independent code review, 2026-08-04 — incomplete / safety regression.**
Granting `get_api_spec`, schedule discovery, and bounded schedule updates matches
the prompts. Granting `trigger_schedule` does not: it immediately executes a
workflow and can cause external side effects, while the Fixer practices require
approval for changes that alter such effects. The new test currently enshrines
the unsafe grant. Remove `trigger_schedule` from the automatic Fixer surface or
route it through an explicit approval gate; keep `list_schedules`,
`get_schedule_runs`, and `update_schedule` if scheduler repair remains in scope.

---

## 4. A tool description that asserted the wrong working directory

**Symptom.** Six calls across three agents failed with:

```text
sh: line 0: cd: Workflow/build-in-public: No such file or directory
```

issued from **inside** `Workflow/build-in-public`.

**Root cause.** The `execute_shell_command` description said:

> *"…with the working directory set to the workspace docs root. Both relative
> paths (resolved against the docs root) and absolute paths under the docs root
> are accepted."*

Untrue. `SetSessionWorkingDir` points a workshop tool-agent at its workflow
folder and a step at its run execution folder. The log says so verbatim:

```text
🔒 Workshop tool-agent session ... cwd="Workflow/build-in-public"
```

and 30 more lines at `Workflow/rtslatency/runs/iteration-0/dev/execution`, 16 at
`Workflow/upwork/runs/iteration-0/job-search/execution`. The docs-root claim was
never true for any of them.

The failure is also **indistinguishable from a genuinely missing folder**, so the
agent could not tell which it had.

**Fix.** Description corrected to say the cwd is session-specific and to run
`pwd` rather than assume. More importantly, `shellWorkingDirectoryHint` appends
the effective cwd to stderr **on failure only** — so the truth travels with the
failure even if the description drifts again. Successful commands pay nothing.

**Independent code review, 2026-08-04 — core fix accepted; description still
overpromises.** The effective-cwd hint is correct for commands that execute and
return a failure. The description still says *any* failed command reports its
directory, but Folder Guard, validation, and other pre-execution failures return
before that hint is appended. It also says absolute paths under the docs root are
accepted, although a session's granted paths can deny them. Reword both claims
conditionally: executed non-zero commands report cwd, and docs-root paths are
accepted only when allowed by the current session grants.

---

## 5. Oversize output, and the spill file the agent was told to read

This is one chain, and it produced two separate findings in the scan.

**Symptom.** Proven byte-for-byte from one pair of log lines:

```text
09:05:53  result = 103,685 chars → CLI truncates, writes
          ~/.claude/projects/<slug>/<session>/tool-results/
              mcp-api-bridge-execute_shell_command-1785814553588.txt
          and instructs: "Use offset and limit parameters to read specific portions"
09:05:59  agent reads that exact file → access denied: absolute host path
```

Same file id, six seconds apart. Repeated at 08:09→08:11 and 08:28→08:35. The
guard then advised *"use workspace-relative paths (e.g.
'Workflow/myproject/file.txt')"* — advice that cannot work, because the file does
not exist in the workspace and never will.

**Root cause, part one — nothing capped anything.**
`workspace/handlers/shell.go` returns `stdoutBuf.String()` whole. mcpagent's
100KB guards the fixture from #2. Twelve rejections in 5h37m; **three were
byte-identical retries**, because the agent was told only that output was too
large.

**Root cause, part two — the recovery target is outside every workspace root.**
The CLI's own instruction points into its project directory, which the folder
guard correctly refuses.

**Fix.** `capShellResultForAgent` bounds the agent-facing result at 48,000
characters (`SHELL_MAX_AGENT_OUTPUT_BYTES`), sized against the data: the smallest
rejected payload was 67,930 characters *"across 1 line"*, and single-line content
tokenizes at roughly 2.7 chars/token — about 25k tokens, right at the cap. 48,000
is ~18k tokens in that worst case.

Three properties that matter:

- **Head and tail both survive** (2/3, 1/3). Keeping only the head turns "the
  command worked" into "inconclusive" — the exit line is at the end.
- **stderr keeps a reserved 8,000 characters.** It holds the reason for a
  failure; a huge stdout must not evict it.
- **The marker forbids the retry**: *"Do NOT re-run this command unchanged — it
  will be truncated identically."* Then it names grep, head/tail, `sed -n`,
  jq/awk.

It is applied in the **tool executor**, not `Client.ExecuteShellCommand`:
`controller_scripted.go:912` calls the client directly and parses stdout as
schema-validated JSON, so scripted steps must keep every byte.

Separately, `codingCLIToolResultSpillDenial` recognizes a spill path (requires
both `/tool-results/` and a CLI home directory) and replaces the impossible
advice with the only recovery that works. **The boundary is unchanged** — the
spill holds output this tool produced, so re-running narrower costs one call and
does not widen the sandbox.

**Independent code review, 2026-08-04 — incomplete.** The head/tail policy and
agent-only placement are sound, but the 48,000-byte guarantee is applied to
`stdout + stderr` *before* `marshalResult` JSON-encodes the result. Quotes,
backslashes, control characters, and nested JSON expand during encoding, so the
actual CLI payload can still exceed the intended cap. Enforce the budget against
the final serialized payload (or derive stream budgets from repeated
`json.Marshal` measurements), and add quote/backslash-heavy and nested-JSON
tests. A smaller edge case also remains: when an operator configures a cap below
the truncation marker length, the helper emits the whole marker and exceeds that
configured cap.

**2026-08-05 — re-encountered, partly fixed, still partial.** This chain
recurred in production through `read_skill` rather than `execute_shell_command`:
three batched reference docs returned 67,971 characters, spilled, and blocked a
workflow upgrade preflight so the scheduled workflow never started. Full
write-up: [tool_result_spill_outside_workspace.md](tool_result_spill_outside_workspace.md).

Closed since: the cap is now enforced on the SERIALIZED payload (the encoding
gap named above — measured at up to 6x for HTML-ish content, which is what these
workflows produce most), the marker edge case now fits its own budget,
`read_skill` accepts one file per call, and no doc in the reference bundle can
exceed a single result on its own.

Still open, and why this stays **partial**: the cap lives on one call site
(`pkg/workspace/tools.go`) rather than at a shared chokepoint, so a new tool that
returns large output reopens the same failure. `MAX_MCP_OUTPUT_TOKENS` is also
still unpinned, so the effective limit can be changed remotely by the CLI's own
gate.

---

## 6. A mandated pre-check answered as a fault

**Symptom.** Eight `no saved Pulse review yet` across three sessions, every one
on an identity minted seconds earlier (`2026-08-04T04-10-06.562Z` issued at
09:40:06 IST, first lookup 09:41:30).

**Root cause.** The review stage prompt (`scheduler.go:2780`) instructs:

> *"Reconcile the complete active retained backlog, awaiting-verification work,
> and any already-saved SQLite result **before discovery**. If the evidence is
> already sufficient, do not launch a duplicate reviewer."*

On a fresh run that check **must** miss — the caller is the thing that will write
the row. The reply offered:

> *"…either it is still running … **or this identity pair is wrong.** Use the
> review_run_id and module exactly as reported"*

`ValidatePulseReviewIdentity` runs two lines above and had already passed: the
format parses and the module is in the canonical registry. The function offered
as an explanation the one thing it had just disproved.

**Fix.** Lead with the normal case, tell the reviewer to proceed with discovery
and record its result, keep the genuine waiting-on-another-stage case, and state
outright that the identity is well-formed so no other id should be tried. The
test now fails if `identity pair is wrong` returns.

**Independent code review, 2026-08-04 — incomplete.** The prose is better, but
the expected fresh-run miss still returns `fmt.Errorf`. It therefore remains a
red failed tool call, produces the tool-error marker, and can still trigger
failure handling or retries. `sql.ErrNoRows` for this mandated pre-check should
return a successful structured state such as
`{"found":false,"status":"not_saved_yet"}`. Reserve tool errors for invalid
identity, malformed input, and database failures; update the test to assert a
successful not-found result rather than merely checking the wording of an error.

---

## 7. A deliberate re-registration that became fatal

**Symptom.** The Chief of Staff (Org Pulse) daily pass failed outright on two
consecutive scheduled runs — 2026-08-03 09:01:00 and 2026-08-04 09:00:18:

```text
[SCHEDULER] builtin-org-pulse failed in 4785ms: multi-agent step 1/3 failed:
Failed to finalize agent definition: finalize immutable agent definition:
duplicate direct tool name "delegate"
```

Not a degraded pass — the agent never constructed, so step 1 of 3 never ran.

**Root cause.** `server.go` registers delegation tools **twice on purpose**:

- `server.go:4700` — the initial registration.
- `server.go:5052` — deliberately re-run, commented *"Registrations update
  execution routing immediately. Restore the delegation wrappers once after the
  final registration."*

The second call exists because the generic custom-tool pass rebuilds the registry
and would otherwise replace the async `delegate` wrapper with a blocking
fallback. It depends on **last-write-wins**, which is what the legacy map keyed
by tool name provided.

`LLMAgentWrapper.RegisterCustomToolWithTimeout` accumulates
`definition.Tools.Direct` as a slice, and mcpagent's `finalizeDefinition`
rejects a duplicate name (`agent/definition.go:453`). So an intentional,
correct re-registration became a fatal error.

The collision is dated: `2119967ff` (2026-08-02) introduced the slice form; the
first failure is the next scheduled run.

**Fix.** Re-registering a name replaces the earlier entry. The wrapper's whole
job is converting legacy incremental assembly into one immutable definition, so
it is the layer that must absorb the semantic difference — mcpagent stays strict.
Replacements are logged, because last-write-wins is right for re-assembly while
two genuinely different tools claiming one name is a bug worth seeing.

`base_agent.ApplyTool` already deduped and was never affected.

**A wrong first answer, recorded because it is the tempting one.** The first
diagnosis was that the daily pass resumes the previous run's thread
(`maybeResumeLatestMultiAgentThread`) and re-registers onto a carried-over
wrapper. That is wrong: `server.go:4254` constructs `llmAgent` fresh per call, so
nothing carries over. The duplicate is entirely within one assembly pass. The
plausible story about state surviving across runs cost time that reading the two
call sites would not have.

**Independent code review, 2026-08-04 — functional fix accepted; implementation
comment and regression boundary need correction.** Last-write-wins replacement
in the compatibility wrapper matches the legacy map behavior while leaving
mcpagent finalization strict. However, the comment in
`pkg/agentwrapper/llm_agent.go` still claims the Chief of Staff resumes onto a
wrapper that already carries delegation tools, contradicting the correct root
cause recorded immediately above. Correct that comment and its test commentary.
Also add a regression test that calls definition finalization after replacement;
the current slice-length checks are useful but do not exercise the boundary that
originally failed.

---

## Follow-up: the global header can hide the current running workflow

**Status: open / observing; not claimed fixed.**

On 2026-08-04 the RTS Latency main agent was visibly active and its terminal was
streaming, but RTS Latency did not appear as a running pill in the global activity
header. The header showed other work (`build-in-public`, Upwork, and an idle Org
Pulse schedule) while the current workflow selector showed only the plain
`rts-latency` name.

This part is deterministic in the frontend: `GlobalActivityMonitor.tsx` removes
the current session before building its pills:

```ts
const filtered = activeSessions.filter(
  session => session.session_id !== currentSessionId,
)
```

That avoids rendering the same workflow twice, but the current workflow selector
does not carry the monitor's spinner/clock/needs-input status. The result is that
the one workflow the user is looking at can be the one whose live state is least
visible. `@active` and the header can therefore answer slightly different
questions.

There may also have been an earlier session-registration delay: the user reported
RTS Latency absent before the live inspection, while it was later observable as
active. That second cause is not proven, so it must not be folded into the
deterministic current-session filter or marked repaired.

The durable fix should be one of:

1. keep excluding the current session from monitor pills, but render the same
   status icon and tooltip on the current workflow selector; or
2. include the current session in the monitor and visually deduplicate the plain
   selector.

Until one is implemented and exercised with a real current workflow plus at
least two other active sessions, keep this item open.

---

## 8. Why the Pulse tools started failing the night the surface shrank

**Not a defect of its own — the case that explains most of the counts above, and
the reason #1 and #3 mattered.**

The Pulse agent surface was rewritten five times on 2026-08-03 — 13:15, 14:23,
18:56, **20:15**, 23:26 — with `0d56c1b18` at 20:15 consolidating it **from eight
tools to four**. Among the names it removed:

```text
get_pulse_module_state          get_pulse_finding_backlog
start_pulse_fix_attempt         mark_pulse_module_result
mark_pulse_final_command_result
```

The consolidation is correct and should stay. Its own commit message explains
why it happened: the surface *"was larger than the number of concepts in it and
gave the agent no rule to derive from, so it guessed across the gaps"* — naming
`close_pulse_fix_attempt`, `complete_pulse_fix_attempt`, `consume_human_input`,
`resolve_human_input` and `update_human_input` as invented, none of them real.

**The guessing did not stop. It moved to new names.** On 2026-08-04 one Pulse
Gate session (`schedule-cron--51af4f19_…`) called six tools in a row:

```text
mark_final_command        mark_pulse_command (x2)
pulse_command_state       record_pulse_command
set_pulse_command_state   update_final_command_state
```

**None of the six exist anywhere in the codebase.** Every one is a fuzzy
reconstruction of the removed `mark_pulse_final_command_result`. The same session
produced 6 of the 8 `record_pulse_worklist` schema failures — `decision`,
`selected`, `evidence` as a string instead of an array.

**Why it could not recover.** Three things, each of which is a finding above:

1. **A removed tool and a withheld tool were indistinguishable.** The allow-list
   check runs *before* name resolution, so a name that exists nowhere returned
   `not available in the current workshop mode` (#1) — which reads as a
   permissions problem and invites trying a variant, which is exactly what
   happened six times. **Fixed here**: `toolNameExists` probes every registry
   partition — session custom, session virtual, global custom, global virtual,
   and MCP routing — and an unknown name now returns

   ```text
   tool_not_found: no tool named "mark_pulse_final_command_result" is registered
   for this session, under any name partition. It does not exist — it was never
   registered, or it was removed or renamed. Guessing a variant of this name will
   NOT work; every variant fails the same way. Use a name from the list below, or
   do the task without it. Allowed here: ...
   ```

   A registered-but-withheld tool keeps the `tool_not_allowed:` wording, so the
   two cases are no longer one message.
2. **It could not look up the real surface.** `get_api_spec` was withheld from
   every stage surface (#3).
3. **The declared schema was never the problem.** `record_pulse_worklist`
   declares `required: [module, due, reason]` with a full description. The agent
   simply never got to see it.

**What was checked and cleared.** The guidance templates and skills contain
**zero** references to any of the five removed tool names — the docs were updated
correctly with the consolidation. The stale names come from the model's own
context, which points at resumed Pulse threads carrying earlier turns where those
tools worked. That last step is consistent with the scheduled-session resume
behaviour but is **not proven**.

**The general rule.** Shrinking a tool surface is a change agents cannot observe.
Removing a tool is only half the work; the other half is making the removal
legible at the moment an agent reaches for the old name. Until then, a
consolidation motivated by guessing produces a fresh generation of guesses.

**What remains after the three fixes.** Discovery is closed: `get_api_spec` is
granted, an unknown name says so, and a denial lists the real surface. What was
*not* closed here is repetition within a single tool's own validation — see #9,
which analyses `record_pulse_result` (18 failures, the largest single count in
the scan) and fixes the part of that repetition caused by the validator itself.

**Independent code review, 2026-08-04 — accepted.** The registry path now
distinguishes a name that does not exist from a registered name withheld by the
session allow-list. No new material issue was found in this fix. The separate
schema-repetition and `record_pulse_result` investigations remain open exactly as
described above.

---

## 9. `record_pulse_result`: four rejections to learn four facts about one finding

**The `record_pulse_result` analysis promised above.** 18 failures, the largest
single tool in the scan. Most are agents correctly told their disposition was
malformed — a real `__bogus__` probe call, an invalid `external_owner`, missing
`changed_files`. One finding, though, shows a distinct and fixable pattern.

**Symptom.** Finding `PUL-70B1057E` took **four sequential rejections over 24
minutes** (09:40–10:04, session `workshop-pulse-fixer-…-51af4f19-…`) to record:

```text
10:02:19  reviewer verification inconclusive for finding "PUL-70B1057E"
          requires disposition "changed_unverified", got "awaiting_run"
10:03:11  finding "PUL-70B1057E" disposition must carry the reviewer's
          structured inconclusive proof with identical expected and observed evidence
10:03:25  finding "PUL-70B1057E" inconclusive disposition next_check must
          match the reviewer boundary "the first default/reddit run after 2026-08-04T02:00Z..."
10:04:23  changed_unverified finding "PUL-70B1057E" requires changed_files
          (got changed_files=missing): the exact workspace-relative files this fix changed
```

**Root cause.** `record_pulse_result` validates one finding's disposition
through two stateless functions — `validateFindingDisposition` (structural
shape: does this disposition value have the fields it requires) and
`validateReviewerVerificationDispositions` (cross-check: does the submitted
disposition actually match what the saved reviewer verdict says happened) —
and both returned on the **first** violation they hit. Every check in both
functions reads an independent field of the same disposition object; none of
them depends on an earlier one having passed. The four errors above are not
four separate mistakes discovered in sequence — they were all true at
09:40:44, on the very first attempt, and the validator revealed them one at a
time anyway.

This is the same anti-pattern the codebase had already fixed once, a few
functions away in the same file: the `result=changed` check merges
`changed_files`/`verification`/`finding_dispositions` into one rejection
naming the whole required set, specifically *"so a single retry can satisfy
all three."* That fix never reached these two functions.

**Fix.** Both functions now collect every violation and return one combined,
numbered message when there is more than one — and read exactly as before
when there is exactly one, so no existing rejection wording changed.
`FormatPulseDispositionProblems` (exported from `step_based_workflow`) backs
both, so structural and cross-check violations share one shape. Four tests,
including both new tests verified failing against the old
first-violation-only behavior before the fix landed.

**What this does not fix.** The write path still runs DB-dependent checks
(does the concern exist, is the referenced decision still open, does the
attempt belong to this module) interleaved with the actual writes, inside the
same per-disposition loop — a batch of five dispositions where #3 is invalid
still aborts the whole batch, discarding #1, #2, #4, and #5's valid writes.
Splitting "validate everything" from "write everything" there is a larger,
transaction-sensitive change and is not attempted here.

---

## Open items

**OPEN — the workspace handler's `stdoutBuf` is still unbounded.** A runaway command can
balloon server memory before anything downstream sees it. A generous guard would
close it, but silent truncation there would corrupt a scripted step, so it needs
its own decision. Availability concern, not an agent-facing one.

**OPEN — 35 of the sampled 90 `[TOOL_ERROR] CLI tool payload failure` markers carry an empty
`tool=`.** The tool name is only recoverable by regexing the inner JSON envelope.
The marker made these findable but not attributable, which is what made the counts
at the top of this document laborious to produce.

**OPEN / agent-contract follow-ups from the scan:** `record_pulse_worklist` schema
mismatches (8, plus 2 in `record_pulse_impact`) — `decisions[0] contains unknown
field "decision"`, then `"selected"`, then `evidence: must be an array of
strings, got a string`, all within single runs; this is
[steps_never_learn_from_their_own_validation_failures.md](steps_never_learn_from_their_own_validation_failures.md)
in a new place.

**INVESTIGATED, closed without a code change — `diff_patch_workspace_file`
"could not find matching context lines" (6).** The production handler
(`workspace/handlers/diff_patch.go`) runs a four-layer fallback before
surfacing this: auto-correct the diff → strict patch → exact-content fallback
scan → repeat both with the original, uncorrected diff. Only after all four
fail does the agent see this message, and the code explicitly refuses to
guess rather than risk corrupting a structured file. The one occurrence with
full evidence — a 150KB file, `reddit-scan-patterns.md` — succeeded after 4
recovery calls, which is a real but bounded large-file recovery cost, not a
runtime telling the agent something false. Confirmed working as intended;
left unchanged.

**PARTIALLY ADDRESSED, exact trigger unconfirmed — `custom tool get_api_spec
is not registered for session msgseq-iteration-0-job-search-…-step-4` (3).**
A registration failure distinct from the allow-list one in #3 — this session
had a real virtual-tool registry entry, but `get_api_spec` genuinely was not
in it, which is correct behavior for a non-code-execution-mode session
(mcpagent's `agent.go:1790` excludes it there on purpose; native tool-calling
sessions already get every schema declared to the model directly). Could not
confirm this exact session hit that state via a guidance leak — its log
evidence was rotated away by the server restart, and `workflow-tools.md`
(the doc with the closest-matching defect) is gated to `workshop` mode while
this session ran in `run` mode. Fixed the same-shape defect found along the
way regardless: `workflow-tools.md`'s opening `get_api_spec` instruction was
unscoped, and `workshop` mode supports both CLI and native providers, so a
native-provider workshop session reading it would hit exactly this error.
Commit `676c525d0` (Claude Code). The specific job-search session's root
cause remains open.

Correctly rejected and not defects: `query_workflow_db` SQL syntax (19),
`agent_browser` JS eval (4), shell quoting `unexpected EOF`, and
`mutate_workflow_db` denied for missing `db_access=read-write` (1).

## Verification

| Fix | Tests |
|---|---|
| 1 | 3 in `registry_test.go`, incl. asserting the word "workshop" never appears |
| 2 | import guard, verified failing on a planted import in `registry.go` |
| 3 | 2 drift guards, verified failing without the grants |
| 4 | 4, incl. both newline cases and the two silence cases |
| 5 | 7, incl. the four real rejected sizes asserted under the cap; 2 for the spill denial |
| 6 | rewritten assertion, fails if the disproved cause returns |
| 7 | 3 in `llm_agent_tool_registration_test.go`: replacement, surrounding-tool preservation, and post-finalize rejection; verified failing without the fix ("definition carries 2 direct tools [delegate delegate]") |
| 8 | 2 — an unknown name must read as gone, a withheld one must still read as denied |

Re-run on 2026-08-04:

```text
mcpagent: go test ./agent/codeexec/... -count=1                         PASS
builder:  go test ./pkg/agentwrapper ./pkg/workspace ./cmd/server \
          -run 'ReRegister|WorkshopStageTool|PulseFixerStageTool|Shell|PulseStateViews' -count=1  PASS
```

## Related

- [custom_tool_category_as_agent_addressing.md](custom_tool_category_as_agent_addressing.md) — the first
  version of #1, on the `get_api_spec` path.
- [tool_failures_invisible_in_backend_logs.md](tool_failures_invisible_in_backend_logs.md) — the marker
  that made this scan a single grep.
- [workflow_step_shell_working_directory.md](workflow_step_shell_working_directory.md) — the same cwd
  subject, from the harness side rather than the description side.
- [steps_never_learn_from_their_own_validation_failures.md](steps_never_learn_from_their_own_validation_failures.md)
  — the pattern behind the open `record_pulse_worklist` item.
- [pulse_platform/index.md](pulse_platform/index.md) — a
  sibling index-plus-tickets document (PLAT-NNN), already following the
  structure this file is adopting. PLAT-018, fixed `c0ce81d86` (Claude Code),
  is the dashboard-stage twin of #3/#8 here: a prompt that never named the
  sanctioned tool, so the agent reached for `mutate_workflow_db` and was
  correctly denied.
