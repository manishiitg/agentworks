# Decisions and open issues

A running log for people and coding agents working on this repository. Read it
before changing behaviour it covers; add an entry (newest first) when you make
or reverse a decision, and move an open issue to a decision once it is settled.
Each entry says what was decided, why, and where it lives in the code.

## Decisions

### 2026-09-30 — Every CLI is qualified under the lock in both chat modes before a deploy
- Turning the lock on for everyone exposed that only Claude and Codex had been
  run under it. In one day Muse, Cursor and Pi each failed at start or in the
  middle of a turn. What to check per CLI, on a real server, with the real
  launcher (`video-studio-landlock-runner`) and `strace` when a pane dies:
  1. starts and reaches its prompt; a prompt goes through (hooks, extensions);
  2. resumes a chat that started before the lock (native session adoption);
  3. mid-turn text streams and the finished reply lands (which native store the
     server reads, and in which order);
  4. every per-launch file it writes (markers, sockets, configs) is granted.
- A dead pane's own output is in the agent log ("dead pane output before
  cleanup"); npm's own log is in the private home (`.npm/_logs`).

### 2026-09-30 — Resumed chats adopt their native session into the private home
- A chat started before the lock keeps its native session in the server's or
  account's own home; the confined CLI reads only its private home, so
  `resume <id>` found nothing and the pane died (Muse: "Reading session log",
  then exit). One shared step in `clisandbox.LandlockArgs`
  (`internal/clisandbox/adopt.go`) copies only that session's files, never the
  rest of the home, and keeps a copy that already exists. Muse, Codex and
  Cursor use it; Claude has its own (`claudeAdoptResumedConversation`).
- Cursor chats live under the account's `XDG_CONFIG_HOME` (the server sets one
  on RTS) or `~/.cursor/chats`, and go to `.config/cursor/chats` in the private
  home, where the confined Cursor reads them.

### 2026-09-30 — Cursor reads the confined chat's own store first
- `cursorChatsRoots(home)` puts the given home's `.config/cursor/chats` before
  the server's XDG folder. The other order read a stale copy of the same chat
  (started before the lock) and so nothing streamed mid-turn on RTS; the reply
  only landed at turn end.
- Code: `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_paths.go`.

### 2026-09-30 — What each CLI is granted beyond the common rules
- **Muse:** read access to the folder of its `managed_hooks_path` (the Orca hook
  scripts it runs on every prompt); without it every prompt failed with
  "Prompt blocked by hook ... Permission denied".
- **Pi:** write access to its per-launch folder (`launch-pi.sh` and its
  siblings). Pi's injected `mlp-marker.ts` extension appends every event to
  `markers.jsonl` there. The platform reads that log to know a turn has ended
  and that a live message was really received, so the extension is required,
  not optional. Read-only access made Pi exit at start with "Failed to load
  extension ... EACCES ... markers.jsonl".
- **Pi extension cache:** a confined Pi gets its own `tmp/extensions` folder
  instead of the shared one in the server home (`linkSharedPiExtensionCache`).
  The shared cache is executable code every user's Pi would load, so a writable
  shared copy would let one user plant code in another's; and the confined Pi
  could not write there anyway (npm exit 243). npm reinstalls into the private
  folder in about half a second.
- Rule of thumb: a per-launch file the CLI itself writes needs an explicit
  write grant; a file it only reads needs a read grant. Moving `TMPDIR` off
  `/tmp` removed the accidental grant that made some of these work.

### 2026-09-30 — Terminal mode has no app message composer
- All typing stays in the native CLI; remove the optional composer and its
  expand/collapse control. Keep the saved chat draft for Return to chat.
- Commands use their standalone picker. Attach uploads to the scoped folder
  and pastes absolute file references into the current chat's live tmux input
  without pressing Enter. A failed paste retains the files as chat attachments.
- The upload API returns an additional absolute path so native file references
  remain valid when the CLI's working directory is a project subfolder.
- Code: `frontend/src/components/ChatInput.tsx`,
  `frontend/src/components/NativeTerminalToolbar.tsx`,
  `workspace/handlers/documents.go`.

### 2026-09-30 — Automatic walkthroughs wait for startup to resolve
- Wait for provider onboarding to be cleared and for the initial automation
  manifests to load before opening a tour. Crew and Code use their existing
  project-loading readiness flag. A saved automation initially looks empty
  until its manifest arrives; opening that temporary screen's tour caused a
  flash on reload before the restored screen or Providers replaced it.
- Manual Help & walkthrough remains available during startup, and dismissal
  preferences remain scoped to each screen in browser or Electron storage.
- Code: `frontend/src/components/ModePresetBar.tsx`.

### 2026-09-30 — Coding CLIs run under the Landlock lock for everyone, everywhere
- `AGENTWORKS_CLI_LANDLOCK=on` and `AGENTWORKS_CLI_FULL=on` on RTS, excellence,
  Confida and SparkQuill (service unit / `product.env`). No staged rollout, no
  per-user lists.
- A CLI that fails under the lock is fixed, never exempted (an exemption for
  Muse was built and reverted the same day).
- Code: `agent_go/cmd/server/cli_landlock.go`, launcher
  `workspace/cmd/landlock-runner` + `workspace/security/landlock_runner_linux.go`,
  CLI side `multi-llm-provider-go/internal/clisandbox/landlock.go`.

### 2026-09-30 — What a confined CLI may touch
- Read/write: its working folder, its private home
  (`<workdir>/.sandbox-cache/cli-home/<cli>`), the folders its chat's folder
  guard grants, and explicitly granted runtime files. Providers other than
  Muse also receive the shared `/tmp` grant.
- `/tmp` is writable because Cursor keeps sockets at fixed `/tmp` paths
  (`cursor-askpass-*.sock`, and `/tmp/.cursor/<project>` when its home path is
  too long for a socket). Accepted as low risk; see the open issue below.
- Muse uses its private `TMPDIR` without the blanket shared `/tmp` grant.
  Live echo startup and native resume pass under this narrower policy; real
  Meta authentication and native-tool calls still need an authenticated smoke
  test. See `docs/bugs/muse_landlock_directory_startup.md` for the regression
  probes and their limits.
- `/` is list-only (file and folder names, no file-content reads): Muse opens every folder from
  `/` down to its workspace at start ("Agent Definition filesystem source
  failed: IoError"). Landlock cannot grant one folder without everything below
  it, so names throughout the host are listable wherever Unix permissions
  permit; this rule does not grant access to file contents.
  Agents are also told in their system prompt to stay in their own folder.
- A private mount namespace per CLI (a minimal `/`, a private `/tmp`) was
  considered and deferred as heavier than needed.

### 2026-09-30 — The server keeps its temp files out of `/tmp`
- The agent service runs with `TMPDIR=<state>/agent-tmp` (created at start in
  `agent_go/cmd/root.go`), so launch scripts (which export secrets), per-launch CLI
  configs (Muse login copy, MCP settings with bridge tokens) and hook scripts are
  not in the `/tmp` that CLIs can read.
- `TMUX_TMPDIR=/tmp` keeps the tmux socket where it was, so running chats stay
  reachable across the deploy.

### 2026-09-30 — CLI logins are linked per account, from where each account keeps them
- The private home gets a link to the account's login file, never a copy
  (refresh tokens rotate). The source follows the account's own
  `XDG_CONFIG_HOME` / `CODEX_HOME` / `CLAUDE_CONFIG_DIR`; for the server account,
  the server process's environment (RTS sets `XDG_CONFIG_HOME`, so its Cursor
  login was never found before). `CLISecurityPolicy.CredentialEnv`.
- On RTS Cursor runs on an API key, so it does not depend on the login file.

### 2026-09-30 — Accounts on Crew and Code
- Your own signed-in account is used by default for your own interactive chats
  only; bots, schedules and auto-notifications keep the project's account.
- Creating a Crew or Code never saves a private account on it (others it is
  shared with could not use it). Workflows save an account only when no shared
  one is usable.
- An explicit account change keeps the same chat and restarts the CLI on the
  new account.
- A denied private account fails the turn with a message saying whose it is and
  how to share it; it never falls back to another account.

### Earlier — One chat per Crew and Code
- Each Crew and Code has one canonical chat; there is no "New chat" button
  (Video Studio keeps its own). Reconsidered 2026-09-30 and kept.

### Earlier — Full CLI without the lock is local-only
- `AGENTWORKS_CLI_FULL_UNCONFINED=on` (on by default in
  `run_server_with_logging.sh`) gives the CLI its own shell and file edits on a
  single-user machine; refused in multi-user mode. macOS Seatbelt is deferred
  (PLAT-364 doc).

## Open issues

- **Shared `/tmp` between confined CLIs.** Every CLI on a server runs as the
  same Linux user. Providers other than Muse can read and write `/tmp`:
  other CLIs' temp files, the
  workspace command scratch (`/tmp/aws-<uid>`) and the browser sockets
  (`/tmp/.agent-browser`). Landlock also does not govern `connect()` to
  Unix sockets, so the tmux socket (`/tmp/tmux-<uid>/default`) is reachable
  from a confined CLI whatever its grants. Fixing both needs the private mount
  namespace above.
- **Confida Slack replies about 90 s late.** The gap is before the session is
  saved; `[BOT_TIMING]` and `[BUILDER_RESTORE_TIMING]` logs are in place to
  name the slow step (suspect: `restoreLatestBuilderConversation` reading every
  saved Builder conversation).
- **Cursor chat text only at turn end (checked 2026-09-30).** With the store
  order fixed, mid-turn text should stream; confirm on RTS after the deploy that
  the chat shows Cursor's messages while the turn runs.
- **Deploy check can fail on a stale `claude`.** `deploy/common/install-coding-clis.sh`
  refuses when a CLI resolves outside the managed install. Confida had a stray
  `tools/node/bin/claude` from an interrupted install (removed by hand
  2026-09-30). The check names only the CLI, not the path it found.
- **Muse authenticated smoke test.** Startup and resume pass under the lock;
  a real Meta-authenticated turn with native tools is still to be run.
- **Personal accounts per provider.** The Providers screen says "The
  installation does not allow personal accounts for this provider" when the
  provider is locked to the server's account (RTS Cursor runs on the server's
  API key) and `ALLOW_PERSONAL_PROVIDER_CONNECTIONS` is not set
  (`personalProviderConnectionsLocked`, `provider_connections.go`). It is a
  server setting with no UI switch, and the screen does not say who can change it.
