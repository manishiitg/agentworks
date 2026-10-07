[← browser / browser](index.md)

# PLAT-685: Slot accounts reap their own leftover agent-browser helpers

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | browser |
| Area | browser |
| Summary | Slot accounts clean up their own leftover agent-browser daemons and stale files, as the slot. |

## What happened

Excellence, 2026-10-07: two `agent-browser-linux-x64` daemons owned by the slot account `slot03` had been running
3 days 16 hours (session `agents--project-d04e6ab66889d0bb--browser`, headless, `AGENT_BROWSER_SOCKET_DIR=/tmp/.agent-browser`).
No `.pid` file named either of them: they were orphans by the PLAT-662 rule. The PLAT-662 reaper in the server could not
help: it runs as the service account (`agents`) and only touches that account's processes, and must never get power
over another account's.

Why the slot's own daemons were also invisible to any later pass: every slot command runs in its own user and mount
namespace (slotctl `userns`, the Landlock launcher's private `/tmp`). The daemon outlives the command and stays alone
in that namespace. The shared `/tmp/.agent-browser` is bound into each one, so its `.pid` and socket files are the
host's, but the reaper skipped every daemon in another mount namespace.

## Fix

- The reaper rules moved from `agent_go/pkg/browser/helper_reaper.go` into `workspace/browserreap` (same orphan, ended-chat
  and stale-file rules; the server keeps the ended-chat decision, which needs its live-chat list).
- A daemon in another mount namespace is now listed when its socket folder is the very folder this process sees at that
  path (`/proc/<pid>/root/<socket dir>` is the same inode), which is the case for the bound shared folder. Otherwise it is
  still skipped. This also lets the server reap its own orphans started from sandboxed shell commands.
- A `.pid` that names another account's live process (EPERM) now counts as live, so its session's files are kept.
- The Landlock launcher (`video-studio-landlock-runner`, already the program slotctl may run as a slot, rebuilt by every
  deploy) has a subcommand `reap-browser-helpers`: as the calling account, end its own orphaned daemons and remove its own
  day-old files with no live daemon (`browserreap.ReapOwnLeftovers`). It never closes a daemon a `.pid` file still names,
  so a slot's live job keeps its browser; it refuses to run as root.
- The server's reaper loop (start and every 10 minutes, main instance only) calls `security.ReapSlotBrowserHelpers`: for
  every assigned slot in the slot table it runs that subcommand as the slot through `sudo -n -u <slot> slotctl exec`
  (cwd: the slot's run folder, HOME: the slot's home) and logs each action as `[BROWSER_HELPER_REAPER] <slot>: ...`.
  The server only asks; the slot touches only processes and files its own UID owns. No sudoers, slotctl or allow-list
  change is needed.

Checked live on Excellence (read-only, as slot03): the slot can read its daemons' `/proc/<pid>/environ`, and
`/proc/<pid>/root/tmp/.agent-browser` and `/tmp/.agent-browser` are the same device and inode.

## Verification

`TestPlanReapOrphansEndedChatsAndStaleFiles` (moved to `workspace/browserreap`) pins the orphan, ended-chat and stale-file
decisions. Build, vet and lint ran on GitHub (`scripts/verify-remote.sh`).

## Left (owner, live)

Needs a deploy of a release with this change on Excellence, RTS and Confida (any host with slots). After it, on Excellence:
`ps -eo user,pid,etime,args | grep agent-browser-` shows no slot daemon older than about 10 minutes with no job using it,
and the agent log shows `[BROWSER_HELPER_REAPER] slot03: terminated orphan agent-browser daemon ...` for the two
3-day-old `slot03` daemons.
