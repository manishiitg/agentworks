[← code / terminal](index.md)

# PLAT-595: Keyboard paste is intercepted or sent as a control key in Code terminals

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | code |
| Area | terminal |
| Summary | Ctrl+V reached the terminal as a control byte and Ctrl+Shift+V used a permission-sensitive clipboard API instead of the browser paste event. |

## What happened

Owner reported keyboard paste failing in Code's terminal. The shell panel handled
Ctrl+Shift+V by cancelling the browser event and reading navigator.clipboard,
whose rejection was silently ignored. Ctrl+V was not intercepted and xterm sent
its control character to the shell. The live agent terminal also left Ctrl+V to
xterm's keyboard translator.

## Fix

Let Ctrl+V, Ctrl+Shift+V, Cmd+V and Shift+Insert bypass xterm key translation,
without cancelling the browser paste event. Reuse this detection in the shell
panel and interactive agent terminal. Existing paste handlers still perform the
transfer; no additional clipboard reader or second paste path is added. Keep
Ctrl+C interrupts and terminal control modes intact. The shell menu advertises
Ctrl+V on Windows/Linux and Cmd+V on Mac; the menu Paste action is unchanged.

Verification: real xterm regression verifies shortcut default handling stays
uncancelled, no Ctrl+V byte is emitted, and a multiline Unicode paste arrives
once. Existing terminal panel/shortcut suites and TypeScript compilation pass.
A local browser check of the actual CodeShellPanel with a recording WebSocket
transport confirmed native Cmd+V delivered multiline Unicode text exactly once.
Windows/Linux native keyboard testing was not available locally.

## Left

Confirm the owner's reported terminal interactively on Excellence; native Windows/Linux keyboard testing was not available locally.

## Deployment evidence — 2026-10-06

Excellence release `agents-2a169cf4-20261006121601` contains builder revision
`2a169cf4b3`. Frontend build/catalog/bundle checks, public health checks, and
the full slot self-test passed (156 passed, zero failed, 16 skipped).
