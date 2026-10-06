[← platform / security-sandbox](index.md)

# PLAT-185 — a scripted step's sandbox cannot read a sibling step's log folder despite a correctly-built, correctly-timed `additional_read_paths` grant

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | P1 — this specific instance silently defeats a fabrication |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — real incident confirmed live via direct filesystem proof; root mechanism inside the sandbox NOT yet identified despite exhaustive code tracing |
| Last synchronized | `2026-08-23` |

- **Priority:** P1 — this specific instance silently defeats a fabrication
  check (a workflow-authored safety net that exists to catch a step
  claiming browser actions it never made). The check fails open instead of
  closed, so the exact condition a fabricating run would want ("nobody can
  verify it") currently reads as a pass.
- **Owner:** scripted-step sandbox read-path enforcement — grant computation
  in `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`
  (`setupExecutionFolderGuard`, `appendAdditionalWorkflowReadPaths`) and
  `controller_scripted.go` (`execScriptedScript`, `resolveScriptedShellGuard`);
  enforcement in the separate `workspace` module
  (`workspace/handlers/shell.go`, `workspace/security/isolator.go`).
- **Related:** Shares a symptom shape with PLAT-322 (a
  live incident confirmed real, root call-path not fully identified).

## Symptom, live

Workflow `confida-login`'s scripted step `validate-browser-evidence`
(`learnings/validate-browser-evidence/main.py`) reads a sibling step's
(`execute-browser-and-capture-apis`) tool-call conversation logs to check
whether a browser-automation run that claims `executed_count > 0` actually
made real `agent_browser` tool calls, or fabricated its result (e.g. wrote
DB rows directly via shell instead of driving the browser — a real incident
already fixed once this way on 2026-08-23, see the script's own code
comment). The step's config grants `additional_read_paths: ["runs"]`
specifically so it can reach that sibling step's log folder.

Every real run to date reports `"browser_tool_call_check":
"logs_unreadable_from_this_sandbox"` — the step's own sandboxed read of
`runs/iteration-N/<group>/logs/execute-browser-and-capture-apis/execution/`
finds nothing, so the check cannot verify anything and (by current design)
passes without penalty.

## Confirmed NOT the cause — each ruled out with direct evidence, not assumption

1. **Not a wrong hardcoded path.** `main.py` hardcodes `"iteration-0"` as
   the run-folder segment, which is fragile (iteration folders `81`, `82`,
   `83`, `84`, `85` also exist on disk for this workflow) but was *correct*
   for the specific cycle checked below — its `browser_evidence_gate.json`
   lives at `runs/iteration-0/confida-staging/execution/validate-browser-evidence/`,
   confirming that cycle genuinely ran under `iteration-0`.
2. **Not a timing/ordering race.** For cycle `qa-confida-staging-20260821T145419Z`
   (the most recent real run, `browser_evidence_gate.json` completed
   `2026-08-23T12:21:57`), the three real conversation files —
   `runs/iteration-0/confida-staging/logs/execute-browser-and-capture-apis/execution/execution-attempt-1-iteration-{1,2,3}-conversation.json`
   — were fully written at `12:12:37`, `12:13:35`, and `12:13:49`: 8+
   minutes before `validate-browser-evidence` finished. They existed, fully
   written, well before the read that reported them missing.
3. **Not a glob-pattern bug.** `main.py`'s glob
   (`logs_dir.glob("execution-attempt-*-conversation.json")`) correctly
   matches the real filenames — verified directly: `python3 -c
   "import pathlib; print(sorted(pathlib.Path('runs/iteration-0/.../execution').glob('execution-attempt-*-conversation.json')))"`
   from a plain (unsandboxed) shell returns all three files.
4. **Not a `DB_PATH`-style env-injection gap.** The scripted fast path
   (`execScriptedScript`, `controller_scripted.go:764`, commit `e2e8b5c7`)
   sets `DB_PATH` unconditionally from the exact same `hcpo.GetWorkspacePath()`
   value used to build the sandbox's read grants — and `db/db.sqlite`
   access demonstrably works for this same script (it queries and updates
   `cycle_locks`/`api_contract_observations` every run through the identical
   resolution pipeline). If the base-path plumbing were wrong, DB access
   would fail too; it doesn't.
5. **Not a stale/pre-computed sandbox profile.** `appendAdditionalWorkflowReadPaths`
   (`controller_agent_factory.go:502-514`) resolves the declared `"runs"`
   grant to the *entire* `filepath.Join(baseWorkspacePath, "runs")` tree —
   workflow-wide, not scoped to the current step — and the isolator's
   `generateSandboxProfile()` emits `(allow file-read* (subpath "<path>"))`,
   which macOS `sandbox-exec` evaluates dynamically at syscall time against
   the real filesystem, not against a point-in-time snapshot. Files created
   after the profile is generated are still covered.
6. **Path canonicalization looks correctly built.** The isolator's
   `canonicalPath()` (`workspace/security/isolator.go:61-86`) resolves
   symlinks (e.g. macOS `/var`, `/tmp` → `/private/...`) including for
   ancestor paths that don't exist yet at profile-generation time.

So: the grant is computed workflow-wide, generated as a dynamic subpath
rule, and the exact target files existed at the exact expected path well
before the denied read. Every layer that could be read and reasoned about
statically looks correctly built. The actual failure point inside the live
sandbox enforcement has not been isolated.

## What has NOT been done yet — the real next step

No live instrumentation exists comparing, on an actual failing run, (a) the
sandbox's real resolved `ReadPaths`/generated profile text against (b) the
exact literal path the script's `execute_shell_command`/read call attempts.
Static tracing cannot go further than this ticket already has — every
remaining candidate (a subtle path-string mismatch invisible to a code
read, a `sandbox-exec` platform quirk specific to this OS/version, some
other enforcement layer this trace missed) requires side-by-side logging
from a live failing invocation to distinguish.

## Suggested first repro/diagnostic step

Temporarily log, on the next real `validate-browser-evidence` run:
1. The exact `ReadPaths` list `resolveScriptedShellGuard` computed for that
   invocation (already partially logged — see the `🐍 [scripted_code]`
   info line in `execScriptedScript`, `controller_scripted.go` — confirm it
   includes the sibling step's logs path).
2. The exact literal path/command the script's shell read attempted and
   its raw stderr (not swallowed by Python's `OSError`-catching `is_dir()`/
   `glob()` — those hide `PermissionError` and `FileNotFoundError`
   identically; `main.py` should be changed to use `os.listdir()` in a
   try/except so a real run's own output states which one occurred, rather
   than requiring log correlation after the fact).

## Explicitly out of scope for this ticket

- Fixing `main.py`'s own gaps (fail-open on "unverifiable" instead of
  fail-closed; the hardcoded `"iteration-0"`) — those are real, separately
  actionable, and workflow-owned (not platform) code. Tracked as follow-up
  work in this conversation, not filed as a platform ticket since they are
  correct-but-fragile authoring choices in one workflow's own script, not a
  platform defect.
- Injecting a `RUN_FOLDER`-style env var so scripted steps never need to
  guess their own iteration folder — a reasonable platform hardening, but
  independent of and not a fix for the sandbox-read mystery above.
- Actually converting `validate-browser-evidence` to agentic execution mode
  (proposed as a possible workaround) — note for whoever picks this up:
  both scripted and agentic paths call the identical
  `setupExecutionFolderGuard(...)` with identical arguments
  (`controller_scripted.go:798`, `controller_agent_factory.go:1222`), so
  the read-path *grant* does not change between modes. A mode switch only
  helps if the agentic step also gains a tool that enforces reads through a
  different mechanism than `execute_shell_command`'s OS-level sandbox (e.g.
  a native `read_workspace_file`/`list_directory` MCP tool going through
  the Go workspace API directly) — untested, unconfirmed.

## Verification

None yet — this ticket exists to record a confirmed-real, fully-traced-but-
unsolved incident so the next session with live repro access (a real
failing run to attach instrumentation to) doesn't have to re-derive
everything in "Confirmed NOT the cause" above from scratch.
