[← platform / coding-agent-bridge](index.md)

# PLAT-417 — Muse on a confined host: runtime folder, refused messages, probe folders

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixed on `main`, not deployed: registry_io warning (own runtime folder), "another run is still starting" (retry), probe folder leak (sweep as the slot user). |

| Coordination | Value |
|---|---|
| State | fixed on `main` (provider `70f1264`), not deployed; one item open |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-390 (Muse `--yolo`), PLAT-394 |

Note: the two provider commits `bbcd60f` and `70f1264` name this work PLAT-411 by
mistake (that number is the Relay ticket).

## From Excellence testing (reported by ai-work-0b)

| Finding | State |
|---|---|
| `local session messaging unavailable: registry_io ... Permission denied` at every start: a Landlock-confined Muse had no `XDG_RUNTIME_DIR` and fell back to `/tmp/tbh-<uid>-rt`, which it cannot use | fixed: it gets `<private home>/run` behind a short `/tmp/mrt-<hash>` link (unix socket paths are capped near 104 bytes), owned by the slot user, stable across turns |
| `Message not sent — another run is still starting` after a resume or new chat; the text stays in Muse's input | fixed: the notice is recognised (the refusal also changes the pane, so it counted as submitted); wait (growing, about a minute), clear the input, retype; no submit attempt used |
| `muse-workspace-probe-*` created at every start and never removed (572 on Excellence, 1,286 on a developer's Mac) | fixed: a small `sh` prelude in the launch (tmux and structured lanes) removes ones older than two minutes and then execs Muse. It runs as the user who runs Muse, so on a slot it is the slot user who owns them (the server could not delete them) |
| `failed to read auth file: Permission denied` mid-run | **open**: not reproducible on demand; needs a strace of a Muse that prints it |

## Verified

macOS, live, both lanes: `TestMuseCLIRealFullNative` (tmux and structured) pass;
planted old probe folders are removed and a backlog of 1,288 became 2.
On a slot (Excellence): to check after the deploy (`/tmp/mrt-*` resolves for the
slot user and a start has no registry_io warning; New chat plus an immediate
message; the probe sweep on the next start).

## Left

- The auth-file error: when it next shows, `strace -f -e trace=file`, failed only,
  on that Muse and read the denied path (hypotheses: a child muse resolving its
  config from HOME rather than XDG_CONFIG_HOME, or the per-turn
  `agentworks-muse-config-*` folder removed under a long-lived Muse).

## Register notes

[PLAT-417](plat-417.md), fixed on `main`, not
deployed: registry_io warning (own runtime folder), "another run is still
starting" (retry), probe folder leak (sweep as the slot user). The mid-run
auth-file error is open.
