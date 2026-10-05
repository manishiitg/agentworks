[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-552 — Stopping a workflow did not stop its Codex step: the real Codex process kept running

| Coordination | Value |
|---|---|
| State | fixed on main (provider `46b3afd`, builder pin bumped 2026-10-06); not deployed; live-checked locally with the real Codex wrapper |
| Date | 2026-10-05 |
| Owner | scheduler-runs |

## Source

Owner, Upwork chat: "Stopped the job-search workflow and its active search step." — "it didn't really stop the background workflow run".

## What happened (verified)

- 23:34:10: the chat's `run_full_workflow` started the `job-search` group; step 5 ("search, find and shortlist") ran Codex in structured mode (`codex exec --json`).
- 23:35:30: `stop_all_executions` cancelled both registered executions ("cancelled all 2 running executions"); 23:36:30 the chat cancelled the step again.
- The step's own Codex rollout shows browser actions every few seconds until 23:38:09 (`turn_aborted`), when the backend went down. The cancel never stopped it.

## Cause (verified, reproduced live)

The run context was cancelled correctly all the way down. The Codex adapter started `codex` with `exec.CommandContext` and `Setpgid`, but with Go's default cancel, which sends `SIGKILL` to the one started pid. `codex` on PATH is the npm Node wrapper (`@openai/codex/bin/codex.js`), which starts the real binary as a child and forwards only SIGINT/SIGTERM/SIGHUP. So the wrapper died and the real Codex kept working, holding the output pipe open, so the call did not even return.

## Done

- Provider `procshutdown.KillGroupOnCancel`: on cancel, `SIGKILL` the whole process group. Used by the Codex, Claude and Cursor structured adapters (Pi already did this). `cursor-agent` is also a launcher script.
- Tests: `TestKillGroupOnCancelStopsTheLaunchersChild` (real shell launcher and child; asserts the child survives the old cancel and dies with the new one); live `TestCodexCLIRealStructuredCancelLeavesNoCodexProcess` (`RUN_CODEX_CLI_REAL_E2E=1`, real `codex` wrapper): passes with the fix; without it, the call did not return within 30 s after cancel.
- Builder pins provider `46b3afd` (no other provider commits in between).

## Left

- Agy and Muse exec adapters do not start their CLI in its own process group, so a cancel kills only the started process; check whether their binaries are launchers before changing their process setup.
- Owner check: run a workflow with a Codex step, stop it from the chat, confirm the browser activity stops within seconds.
- Deploy needs the owner's go.
