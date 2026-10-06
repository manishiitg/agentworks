[← platform / security-sandbox](index.md)

# PLAT-364 — Coding CLIs and the tmux socket are outside the sandbox

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | covers what the private `/tmp` change (e59220636, deployed on RTS) left open. |

> **See also [PLAT-371](plat-371.md):** the instruction files and cleanup the CLIs write into a shared project folder (and the destructive startup cleanup) are a separate data-loss fix; it does not change the confinement plan here.

| Coordination | Value |
|---|---|
| State | tmux socket fixed and deployed on RTS (ab6bb0b4f); CLI confinement planned (see Plan, 2026-09-29): lock proven on RTS |
| Date | 2026-09-28 |
| Owner | security-sandbox |
| Related | [PLAT-362](plat-362.md) D1 (shared `/tmp`, tmux socket) and its native-read note; [remote workspace server plan](../../../../core/remote_workspace_server_plan.md) (agents run on the laptop); GitHub #235 (agy review, M6) |

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

## Done (step 1): tmux socket, ab6bb0b4f, deployed on RTS 2026-09-28

- **Private `/tmp`:** each sandboxed command gets its own. The workspace
  service starts the Landlock launcher in new user and mount namespaces; it
  holds the host's AppArmor userns exception (`video-studio-userns`).
- **What the launcher does:**
  - mounts an empty tmpfs on `/tmp`;
  - binds back only the policy's `/tmp` paths and `/tmp/.agent-browser`;
  - clears its mount capability, then applies Landlock.
- **Result:** the tmux socket does not exist for the command. `/proc/<pid>/root`
  is no way around it, because Landlock denies access to processes outside
  its domain.
- **Fallback:** hosts that refuse namespaces keep the host `/tmp` and report
  it in sandbox health. `AGENTWORKS_SANDBOX_PRIVATE_TMP_DISABLED=true` turns
  it off.
- **Health on RTS:** "filesystem ABI 8; launcher preflight passed; private /tmp".
- **Browser regression fixed in the same commit.** It came from
  e59220636: sandboxed Chrome could not create shared memory in
  `/tmp/aw-browser-<uid>`.
- **Tests:** opt-in e2e tests `TestPrivateTmpBetweenCrewsE2E` and
  `TestPrivateTmpBrowserE2E`, run from the live release on RTS.
- **QA:** GitHub #236.

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

## Plan (2026-09-29): lock every CLI, then Full CLI on Code

### What we know now

- **The read leak is real, on both servers.** "Native agent tools" is on by
  default, and each CLI's own read tool is not bound to its folder.
  - Mac, Muse 1.4.1: `read_file` read a sibling folder's file and `/etc/hosts`.
  - RTS, Claude: `Read` returned another folder's file.
  - A model may refuse a request that *sounds* like another user's file (it did
    on excellence), but nothing stops a neutrally worded or prompt-injected
    request.
- **No CLI is sandboxed today.**
  - Live Muse processes on excellence: `NoNewPrivs: 0`,
    `HOME=/srv/agents/home` for everyone.
  - Each process only *starts* in its Code/Crew/workflow folder.
  - The shared home holds every user's CLI history and the login.
- **The lock works (RTS, 2026-09-29, scratch folders, live chats untouched).**
  Claude run with `video-studio-landlock-runner`, write grant = its folder plus
  a private home:

  | Probe | Today | Under the lock |
  |---|---|---|
  | own folder | read | read |
  | other folder | read | `EACCES: permission denied` |
  | `/etc/hostname` | read | read (system files are read-only by design) |
  | shell: other folder / real `~/.claude` | allowed | Permission denied |

  - Claude needed nothing beyond the launcher's system baseline.
  - Its login (`CLAUDE_CODE_OAUTH_TOKEN`) came from the environment.
  - The launcher `exec`s the command, so tmux still sees the CLI as the pane
    process.
- **Host support.**
  - Both servers already have the launcher.
  - Excellence: kernel 6.8, unprivileged user namespaces allowed.
  - RTS restricts user namespaces (`apparmor_restrict_unprivileged_userns=1`).
    Its `video-studio-userns` AppArmor profile covers only
    `video-studio-workspace`, so a CLI started in a tmux pane gets Landlock but
    **no private `/tmp`** (the tmux socket stays reachable) until that profile
    also covers the launcher. That is a root/deploy change.
- **Shared CLI launch.**
  - Claude, Cursor, Muse, Pi and agy build their launch command through
    multi-llm-provider-go `internal/shelllaunch.CommandWithScopedEnv`, the one
    place to add the launcher.
  - Codex launches separately.

### Decisions (user, 2026-09-29)

- **Native agent tools stay on.** Turning them off is not acceptable; the
  current risk is accepted until the lock ships.
- **Full CLI goes to Code first.** It is an extra setting on Code's Agent tools
  switch (Off / Native agent tools / Full CLI), off by default. It widens later.
- **No approvals for native writes inside the folder.** The lock is the
  boundary; backups and versions still snapshot the folder.
- **Keep both tool sets in Full CLI.**
  - The CLI's own Bash, Write and Edit are added.
  - Our `execute_shell_command` and patch/write tools stay; they carry
    secrets, protected-file checks and the remote workspace.
  - Hiding our generic tools in Code is a possible later tweak, not part of
    this plan.
- **Shared accounts must work,** including accounts added with a CLI login
  (`claude login`, `cursor-agent login`) and then shared.

### Phase 1: lock every CLI (closes today's read leak)

Every coding CLI starts under the launcher, in every mode, for every chat type.

- **Grants:**

  | Access | Paths |
  |---|---|
  | Write | the chat's folder (Code, Crew, workflow; plus its `.sandbox-cache`) and a **private CLI home** per folder: `<folder>/.sandbox-cache/cli-home/<cli>` holding the CLI's config, sessions and caches |
  | Read-only | the launcher's system baseline, the CLI install, skills folders the chat is given |
  | Nothing | other users' folders, the shared account home, the server's data |

- **Per chat type:**
  - Code and Crew: their folder, plus Crew co-owner/shared-root and attached
    places.
  - Builder: the workflow folder.
  - Plain chats: their chat folder.
  - Workflow steps: the step's folder guard, as today.
- **Launch:** `CommandWithScopedEnv` wraps the argv as
  `runner --config <policy> -- <absolute CLI path> …`. The policy file comes
  from the server, like the shell tool's. Codex gets the same wrap in its own
  launcher. The launcher needs an absolute program path (`claude` alone fails
  with ENOENT).
- **Private `/tmp`:**
  - Excellence: the launcher enters its own user and mount namespaces from
    inside the pane (it cannot use the server's clone flags there).
  - RTS: extend `video-studio-userns` to the launcher path through the deploy
    config.
  - Until then, report "Landlock without private /tmp" in sandbox health.
- **Server-side readers follow the move.** Transcript tailing, completion,
  resume, the Muse question watcher and Codex rollout sync read the private
  home instead of the shared one. The first turn of an existing chat starts a
  fresh CLI session carrying the recent dialogue, as a tools-switch change does.
- **Probes:** provider-usage checks (`/tmp/agentworks-provider-usage-*`) get
  their own scratch folder under the launcher, or stay exempt.
- **Grant lists:** from `strace -f -e trace=file` of a real turn per CLI on
  RTS. Auto-updaters stay off (they write to the install folder).
- **Order:** Claude on RTS, Muse on excellence, then Cursor, Codex, Pi, agy.
- **Acceptance, live per CLI:**
  - A native read and a shell read of another folder, the shared home and the
    tmux socket are refused.
  - A normal turn, resume, the MCP bridge, skills, subagents, browser and
    package installs still work.

### Phase 2: CLI-login accounts under the lock

A CLI-login credential lives in the account home and refreshes itself; Claude
and Codex rotate refresh tokens.

- **Rule:** one credential, everything else private.
  - Each private home gets a window to **that one file only**, never a copy,
    so a refresh anywhere is seen everywhere, as with today's shared home.
  - The window is a link plus a Landlock grant on the file if the CLI rewrites
    it in place, or a single-file bind mount (needs the namespaces) if it
    writes a new file and renames it.
  - A spike on RTS decides per CLI which writer it is.
- **Env-token accounts need no window.** Example: RTS Claude and Cursor today.
- **Acceptance:** two chats on one shared CLI-login account run and refresh
  concurrently without logging each other out, and neither can list the
  other's sessions.

### Phase 3: login proxy (shared logins cannot be copied)

Any process can read its own environment and the files it is given, so a
locked CLI can still read its login (today: Claude `Read` on
`/proc/self/environ`; with Full CLI, the shell).

- **Fix:** point the CLI at a server-side proxy (`ANTHROPIC_BASE_URL` for
  Claude; Codex has a base URL too; Cursor and Muse to be checked).
  - The proxy adds the real credential.
  - The CLI holds only a per-session key that works nowhere else.
- **It also gives:** per-user usage and a per-session cutoff.
- **Until it ships:** sharing an account means its users could copy its login.
  Say so in the share dialog.

### Phase 4: Full CLI on Code

The Code switch gains **Full CLI**, available only where Phase 1's lock is
active (Linux servers). Elsewhere it falls back to Native agent tools and the
switch says why.

- **What each CLI gets:**

  | CLI | Full CLI |
  |---|---|
  | Claude | Bash, Write, Edit, MultiEdit |
  | Codex | `workspace-write` sandbox |
  | Cursor | write and shell hooks lifted |
  | Muse | shell and write allowed |

- **Our tools stay** (see Decisions).
- **Network stays open.** Landlock is file-only, and the CLI needs the model
  API and package registries.
- **Acceptance, live:**
  - Full CLI edits and installs inside the folder.
  - It cannot write or read outside.
  - It cannot reach another session's tmux pane.
  - Our tools still work alongside.

### Later

- macOS/desktop: Seatbelt, the same grants.
- Widen Full CLI past Code.
- Network egress control, if needed.

## Full CLI on a local machine, and macOS Seatbelt (deferred), 2026-09-30

- **Local Full CLI (built; on by default in `agent_go/run_server_with_logging.sh`, set
  `AGENTWORKS_CLI_FULL_UNCONFINED=off` to keep hybrid).** Full CLI needs the Linux Landlock launcher, so it never applied on a
  Mac. `AGENTWORKS_CLI_FULL_UNCONFINED=on` now turns it on without a lock for a person's own
  single-user machine: Claude gets its own Bash, Write and Edit with no permission prompts,
  running with the person's own rights, so only its working directory limits it. The server
  refuses it when `MULTI_USER_MODE=true` (`cliFullUnconfinedAllowed`), and it only upgrades a
  chat that already has Native agent tools on. mcpagent mode `full_unconfined`
  (`fullCLIEnabled`), wrapper `UpgradeCodingAgentToolsToFullUnconfined`. A local test tool, not
  a boundary: an injected page can make the agent run anything the person can.
  Codex (mcpagent `3789b0b`): Full gives it the `workspace-write` sandbox. The same commit fixes
  hybrid Codex, which never got its shell or subagents: a per-turn bridge-only shell disable in
  `conversation.go` was unioned with the hybrid flags. Muse has no Full mode yet.
- **macOS Seatbelt confinement (deferred).** The faithful version on a Mac is the same
  folder-only lock through `sandbox-exec`, which the platform shell tool already uses. It is
  deferred: single-user local has no cross-user risk, and the remaining risk is prompt injection
  reaching `~/.ssh` or other projects. Do it before running Full CLI on a laptop day to day, or
  when a laptop drives a shared server's workflows (remote workspace plan). Needs a Seatbelt
  profile per CLI derived from a real turn (as for Landlock), the same private CLI home, and
  a certification pass per CLI.


### AGY local Full CLI, 2026-09-30

AGY now retains the platform's `full_unconfined` mode instead of downgrading it
to `hybrid`. Its native tool gate allows the full toolset, including edits, shell
and subagents, with the private MCP bridge still available. The same single-user
opt-in applies. See [AGY Full CLI](../../../../design/agy_full_native_tools.md).

The rollout is **local only**. Do not use AGY on RTS or excellence for now. This
change does not certify AGY's Linux Landlock behavior or shared-server account
isolation. Local unconfined mode runs with the host user's permissions.

### Switches removed; servers fail closed, 2026-10-03

`AGENTWORKS_CLI_LANDLOCK`, `AGENTWORKS_CLI_FULL`, `AGENTWORKS_CLI_FULL_UNCONFINED` and
`AGENTWORKS_TERMINAL_UNCONFINED` are gone. A person's own Mac runs Full CLI unconfined; Linux
always confines with Full CLI inside the lock; a chat whose lock cannot be applied runs
`mcp_only`, never unconfined. The notes above that mention the switches are history. See
DECISIONS 2026-10-03. Next: macOS Seatbelt, Claude first.

### macOS Seatbelt for Claude, 2026-10-03

Built: Claude Code on a person's own Mac starts under sandbox-exec with Full CLI, using the folder
guard's grants plus its blocked paths (denied inside granted folders, which Landlock cannot do).
It keeps its real home for the Keychain login. Codex, Cursor, Muse, Pi and AGY stay unconfined on a
Mac until certified. Live certification on a Mac is the next step. See DECISIONS 2026-10-03.

## Register notes

[PLAT-364](plat-364.md) covers what the
private `/tmp` change (e59220636, deployed on RTS) left open.
- **Native reads:** coding CLIs run unsandboxed, so hybrid-mode native reads
  can see other users' files, CLI logins and `/proc/self/environ`.
- **tmux socket:** a Landlocked shell can still reach it (verified on RTS), so
  it can read or type into other users' CLI sessions. This is the most serious
  item.

tmux socket fixed and deployed on RTS (ab6bb0b4f): each sandboxed command
gets a private `/tmp` in its own namespace. QA is #236. Still open: run the
coding CLIs under the Landlock runner with per-user CLI homes, then Seatbelt
on macOS.
