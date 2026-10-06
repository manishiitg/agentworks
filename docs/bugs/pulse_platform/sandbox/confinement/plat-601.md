[← sandbox / confinement](index.md)

# PLAT-601: Auto-notify trigger code runs unconfined on the host

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | sandbox |
| Area | confinement |
| Summary | trigger_and_auto_notify runs model-written Python on the server host, outside the CLI sandbox and slot accounts |

## What happened

Review 2026-10-06 (confirmed by reading the code): `executeTriggerPython` (`agent_go/cmd/server/background_code_tools.go:236`)
writes the model's Python to a temp file and runs `exec.CommandContext(python3, "-I", script)` as the server's own OS
user, with a stripped environment and the workspace as working directory. It does not use the Landlock/Seatbelt
confinement or the per-user slot accounts that coding CLIs and `execute_shell_command` use, and nothing limits how many
run at once (each may wait up to 24h). `docs/core/background_code_auto_notification.md` promises the same user,
workspace and permissions as the originating turn; the code does not enforce that. Available in Builder (Goals) and
writable Crew chats. Shipped 2026-09-23, so it is likely on deployed servers. On a multi-user server (Excellence) the
code can read other users' workspaces.

## Fix (not built)

Run the trigger through the same sandboxed executor as `execute_shell_command` (folder guard, Landlock/Seatbelt, slot
account), cap concurrent triggers per user, or disable the tool on multi-user servers until it is. Live proof: a
trigger that tries to read `/etc/passwd` or another user's workspace on Excellence is refused.

