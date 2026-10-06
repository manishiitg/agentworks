[← platform / coding-agent-bridge](index.md)

# PLAT-392 — Codex resumed chats cancelled after confinement migration

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixed in the shared provider, deployed to SparkQuill and verified with a real resumed Sol conversation. |

| Coordination | Value |
|---|---|
| State | fixed and deployed to SparkQuill; shared provider `648234b` |
| Date | 2026-10-03 |
| Owner | coding-agent-bridge |

## Failure

SparkQuill's existing parent conversation failed after the CLI confinement change.
Codex 0.160 exited with `No saved session found with ID …`, even though the
thread's rollout still existed in the installation account's home. The dead-pane
watchdog cancelled the turn, so the UI reported `Response cancelled`.

The session adopter assumed the ID immediately followed `resume`. Interactive
Codex puts profile, model and config options before the ID, so the adopter read
`--profile` and skipped migration. Only the thread selected by the trusted resume
handle should be adopted; no other account sessions may become visible.

A real short-conversation regression then exposed another startup issue: Codex
0.160 retains its old `Resuming session` banner in scrollback after rendering a
new ready header. The readiness check interpreted that historical banner as
current and waited until cancellation.

## Fix and validation

Shared provider `7441120` parses option values before the explicit resume ID;
`648234b` recognizes the newer ready header before an older resume banner.
Regression tests cover actual interactive argv, structured resume, missing IDs,
copying only the selected thread, and retaining the active-resume guard.

The full clisandbox package passed on the SparkQuill Linux host. A real Sol
conversation completed, restarted into a different private home, and correctly
recalled its previous-turn code (18.27 seconds across both turns). The Codex
adapter package and lint passed locally. An unrelated installed-Codex macOS
sandbox test fails on the unchanged provider baseline as well.

The affected existing rollout was copied into its private CLI home atomically,
without overwriting an existing private copy or changing its source. The builder
pins the shared fix. SparkQuill release `sparkquill-7fc2ae97-20261003181010`
runs provider `648234b`; all three services, the shared provider/account APIs,
parent/child defaults, and the restored rollout passed live checks. The public
login page returns HTTP 200. Other deployments must update their provider build
to receive these shared fixes.

## Register notes

[PLAT-392](plat-392.md), fixed in the shared
provider, deployed to SparkQuill and verified with a real resumed Sol conversation.
The release and the affected restored conversation passed live checks. Only the selected thread is adopted into its
private home, and an old resume banner cannot block a newly ready pane.
