# Testing workflow changes alongside a running AgentWorks

> **Update 2026-09-30:** the probe below reproduces the *old* shared-folder collision (a CLI adapter overwriting and deleting `AGENTS.md` / `CLAUDE.md` and skills). Adapters now use a marked, session-counted block that keeps a project's own files ([PLAT-371](../bugs/pulse_platform/platform/security-sandbox/plat-371.md), [design](../design/project_instruction_files.md)); the workflow private-folder tests here are unchanged.

Use the existing `Workflow/testing` as the source fixture, but run tests against
an isolated copy. Never point a second server or test CLI at the live
`workspace-docs` tree. A different port alone does not isolate workspace files,
provider instructions, credentials, browser sessions or native CLI state.

## Standard sequence

1. Capture the relevant source workflow files and their hashes. Copy only the
   inputs the test needs into a fresh directory under `.local/workflow-tests/`.
   Do not copy secrets, databases, browser profiles, native conversations,
   approval queues, backups or scheduled-run state by default.
2. Write a deterministic reproduction using the actual code path. Use distinct
   markers and controlled interleavings instead of asking a model to explain
   whether it thinks it has the right prompt. State whether a passing command
   means the bug was reproduced or the fix was verified.
3. Test the proposed mechanism with a control. Verify shared workflow data still
   resolves correctly, and test both session startup orders and cleanup while
   the other session remains active.
4. Keep test logs and a JSON receipt with source hashes, test scope, command
   outcome and any untested boundaries. Confirm source inputs are unchanged.
5. After implementation, add the real server/session integration test. Only then
   perform live CLI/UI confirmation in a separate AgentWorks instance. A
   projection-only test cannot establish permissions, native resume or real
   mode switching.

## PLAT-296: prompt and skill collision probe

From the repository root:

```sh
python3 scripts/test-workflow-isolation.py
```

This command uses the real Codex and Claude adapter projection functions via
Go test overlays. The provider checkout is not patched. Both simulated session
lifetimes share one disposable `Workflow/testing` folder in the collision case.
The control gives the two sessions private instruction directories while both
still reference the same shared testing plan. No agent executes the plan.

The source `testing` workflow currently contains browser/secret references and
an older contract. The fixture therefore copies only `planning/plan.json` and
`planning/step_config.json`, with a sanitized manifest and a DO-NOT-EXECUTE note.
It is evidence for file projection tests, **not a runnable clone of every
original workflow capability**. Do not execute its retained plan or run its
contract migrations as part of this probe.

Covered for each provider:

- Owner starts first, then Run; and the reverse order.
- Prompt, skill body and supporting-reference collisions.
- First session cleanup while the second still needs its prompt.
- Both remove-on-cleanup and restore-prior-file modes.
- Private-directory controls and shared workflow file access.

The default expects the known collision. A passing result means
**reproduction confirmed**, not PLAT-296 fixed. For a negative check:

```sh
python3 scripts/test-workflow-isolation.py --expect isolated --providers codexcli
```

That command requires the shared-directory helper calls to remain isolated and
currently fails, proving the probe does not report the known bug as fixed.
It is not the eventual acceptance gate if the implementation instead changes
server session-directory selection: that solution needs a server-level test of
the actual selected directories in addition to the private-directory control.

Use `--provider-repo /absolute/path/to/multi-llm-provider-go` to test another
provider checkout. Dependencies must already be cached: the runner sets
`GOPROXY=off`, uses read-only module resolution, does not source the live `.env`,
and forwards no API credentials. It launches no CLI, browser, server or tmux
session and consumes no model tokens. Go may update its ordinary build cache.

Every invocation creates a new artifact directory and prints it. Retain its
`receipt.json` and provider logs with the ticket. No automatic deletion or
process cleanup reaches outside the test directory.

## PLAT-296: verify server-selected runtime folders

```sh
python3 scripts/test-workflow-isolation.py --server-isolation
```

This is the repeatable implementation check. It first tests the production
workflow-chat directory selector and native-handle restore guard. It exports
those selected directories, then runs the real Codex and Claude projection
and cleanup tests against them. The shared-directory cases remain a positive
reproduction of the original bug; the private cases must all pass using the
server's selection, including preservation of shared workflow instructions.
`server-runtime-directories.json` records the mapping beside the logs and receipt.

Selection checks cover separate users, chats, providers and modes; stable
same-session selection across construction; rejection of old shared-directory
or other-chat native handles (including Codex's project-directory override);
and failure without a private state root. Filesystem checks reject symlinked
runtime storage, path obstructions and storage inside workspace documents.
This tests server functions and adapters, not the HTTP query lifecycle or an
actual restarted CLI. It makes zero model calls.

Workflow chats now add a `project/` directory link to the authoritative workflow
inside their private mode runtimes. Native paths use that prefix; bridge paths
remain workflow-relative. Existing private runtime identities and compatible
native resume are preserved. Tests include all six providers, including Agy.

The implementation is enabled by default for workflow chats, using an absolute
configured state root or the durable application default.
`AGENTWORKS_ISOLATE_WORKFLOW_CLI=false` is the explicit Builder rollback. Run
always isolates so cwd's automatic write grant cannot promote workflow data.
The instance launcher enables isolation by default and exposes
`--no-isolate-workflow-cli` for Builder negative-control testing.
Inspect configuration without launching services:

```sh
bash scripts/run-local-instance.sh --instance plat296 --dry-run
```

Private files live under `<state-root>/cli-runtimes/v1/<identity-hash>` with
0700 directory permissions. The identity includes authenticated user, canonical
workflow path, chat session, provider and resolved mode. The workflow bridge
still uses the shared workflow; a short prompt instruction supplies its absolute
root for native file/shell tools. Existing filesystem/tool authorization remains
independent. These folders are not OS-level security sandboxes.

Same-session native resume is allowed only for matching private paths. A legacy
shared-folder handle or an explicit resume into a different chat falls back to
application chat history in a fresh native conversation. With isolation enabled, workflow
terminal attachment waits for the next query to rebuild the current runtime;
simply opening saved history does not attach its old pane. Runtime directories
remain on disk for continuation; no cross-session cleanup or automatic garbage
collection is introduced. Do not toggle the rollback on a server with active chats.

## Separate live instance, when ready

The existing `scripts/run-local-instance.sh` supports separate ports, workspace,
logs, browser namespace and process ownership. Its fake-runner regression test is:

```sh
bash scripts/test-local-instance.sh
```

Before a live test:

- Use a separate code checkout or source snapshot, including the intended local
  dependency revisions. Do not share writable Vite output or build destinations
  with the live development checkout.
- For this ticket, pass `--isolate-workflow-cli` to the isolated launcher.
- Use a fresh instance state root and unused ports; the launcher refuses occupied
  ports. Give the instance a visible test name. Do not copy the live `.env`.
- Create independent test Owner and Run accounts in that instance's own database.
  Point both chats at its single `Workflow/testing` copy.
- Build a harmless marker-only test route. Do not execute the original testing
  plan merely because its name is “testing”. Leave schedules, Pulse, backup,
  publication, browser access and external integrations off.
- Verify provider-specific native config/session isolation before launching real
  CLIs. The instance launcher alone does **not** prove this boundary. Do not
  reuse live conversation handles or global provider cleanup commands.
- For this local PLAT-296 test, retain the user's existing Codex login by using
  the normal `CODEX_HOME` (for example, `/Users/mipl/.codex`). Do not give Codex
  a fresh authentication directory merely to isolate workflow instructions:
  PLAT-296 isolates those through private working directories. Session MCP
  profiles have unique names, and native chats must have distinct identities.
  A fresh provider login is only needed when explicitly testing independent
  credentials. The first live attempt used a fresh Codex home and consequently
  waited at sign-in; that was a test setup issue, not a missing terminal login.
- Exercise overlapping chats, stop/cancel, mode switch, server restart/resume,
  and server-enforced Run permissions. Record outcomes rather than relying on
  what the model says its mode is.
- Stop only the instance-owned launcher/processes. Never use global `pkill`,
  `killall`, shared tmux cleanup, or a restart of the production development app.

A launcher dry run or a passing private-directory control is not proof that this
live matrix passed. Keep those results separate in the ticket.
