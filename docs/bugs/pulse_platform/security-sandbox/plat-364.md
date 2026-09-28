[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-364 — Coding CLIs and the tmux socket are outside the sandbox

| Coordination | Value |
|---|---|
| State | Open: design proposed; step 0 done and deployed on RTS (e59220636) |
| Date | 2026-09-28 |
| Owner | security-sandbox |
| Related | [PLAT-362](plat-362.md) D1 (shared `/tmp`, tmux socket) and its native-read note; [remote workspace server plan](../../../core/remote_workspace_server_plan.md) (agents run on the laptop); GitHub #235 (agy review, M6) |

## Problem

Every agent runs as the same OS user. Only the platform shell tool
(`execute_shell_command`) runs under Landlock; the coding CLIs themselves
(Claude Code, Codex, Cursor, Muse, Pi in tmux or a structured transport) run
as plain server processes.

1. **Native reads are not confined.** In "Native agent tools" (hybrid) mode,
   the CLI's own read, search, glob, skill and subagent tools can read anything
   the service user can:
   - other users' workflows, Crews, transcripts and databases;
   - other CLIs' homes, which hold their login keys and session files;
   - `/proc/self/environ`.

   These tools only read (writes and execution go through the sandboxed platform
   tools), so the risk is disclosure. A prompt-injected page or file can make
   an agent read one of these and pass it on in a message, report or MCP call.
   agy in hybrid mode has the same gap (#235 M6).
2. **The tmux socket is reachable from the sandbox.** Verified on RTS
   2026-09-28, after `/tmp` was removed from the Landlock grant: a Landlocked
   command can still run `tmux -S /tmp/tmux-999/default ls`. Landlock does not
   control `connect()` on pathname Unix sockets. So a sandboxed shell can
   `capture-pane` or `send-keys` into other users' CLI sessions: read their
   screens and type into their agents. **This is the most serious open item.**

## Done (step 0)

e59220636, deployed on RTS 2026-09-28.
- Landlock no longer grants `/tmp`, except `/tmp/.agent-browser`.
- `HOME` moved from the shared `/tmp` to `<workflow|Crew>/.sandbox-cache/home`.
- The shared `/tmp` credentials and dotfiles were removed.
- The opt-in e2e test is `TestPrivateTmpBetweenCrewsE2E`.

This closes the file side of PLAT-362 D1, but not the tmux socket (item 2).

## Proposed

1. **tmux socket (first).** Options, in order of preference:
   - (a) Run each user's (or each session's) CLIs on their own tmux server whose
     socket sits in a folder only that user's processes are launched with.
     This does not stop a same-UID `connect()` by itself, so combine it with
     (b).
   - (b) A seccomp filter in the Landlock runner that refuses
     `connect()`/`sendmsg()` on `AF_UNIX` except to allowlisted sockets
     (Docker rootless, browser sockets). The address is a pointer, so seccomp
     cannot check the path directly; it needs a user-notify supervisor, or a
     blanket `AF_UNIX` deny with the browser and Docker sockets proxied in.
   - (c) Per-user OS accounts for CLIs, which is the real isolation boundary
     and the largest change.

   Check the Landlock ABI on RTS: newer ABIs add Unix-socket scoping, but only
   for abstract sockets.
2. **Run the coding CLIs under the Landlock runner.**
   - **Allow:** the workflow or Crew folder; that CLI's own per-user home (for
     example `~/.claude`, `~/.codex`, `~/.cursor`, `~/.config/muse`, or the
     per-user CLI runtime folder); its install, read-only; system folders.
   - **Inherited:** children (the MCP bridge, subagents) inherit the limits.
   - **Derive the grants from `strace -f -e trace=file`** of a real turn per CLI
     on RTS.
   - **Prerequisite:** a CLI home must be per user wherever it is shared today,
     or granting it leaks between users. agy's single `~/.gemini` is one
     example.
3. **macOS / desktop.** Landlock is Linux only. When CLIs run on a laptop
   (desktop app, and the remote-workspace plan where agents always run
   locally), the same confinement needs `sandbox-exec` (Seatbelt), as the shell
   tool already uses. For a single user this is lower priority, but a laptop
   that drives a shared server workflow still reads only its own disk.
4. **Stopgap, optional.** A Claude Code `PreToolUse` read hook (and Cursor
   `failClosed` hooks) that refuses native reads outside the allowed folders.
   It is per CLI, and Codex and Muse have no equivalent. Or turn hybrid mode
   off on shared servers until step 2 lands.

## Acceptance (live, on RTS, per CLI)

- A native read of another user's Crew folder, another CLI home,
  `/proc/self/environ` or `/tmp` is refused.
- A sandboxed shell cannot list, capture or type into another session's tmux
  pane.
- A normal turn, login, resume, MCP bridge, browser and skills still work.
