# Decisions and open issues

A running log for people and coding agents working on this repository. Read it
before changing behaviour it covers; add an entry (newest first) when you make
or reverse a decision, and move an open issue to a decision once it is settled.
Each entry says what was decided, why, and where it lives in the code.

## Decisions

### 2026-09-30 — Costs shows prompt character counts only
- Replace the saved prompt text preview with character counts for the latest
  saved system and developer instructions, as requested. The Costs history GET
  opts in with `include_saved_prompt_sizes=1`; it returns size metadata without
  prompt text. The previous prompt-text opt-in is removed.
- Count the full saved instructions as Unicode code points, including leading
  and trailing whitespace; counts are not byte lengths or truncated previews.
  This measures the latest saved version, not a per-run prompt archive. Missing
  prompts remain explicit. Existing conversation access checks still apply.
- Reverses the prompt-text display below. Code: `chat_history_routes.go` and
  `frontend/src/components/providers/CostConversations.tsx`.

### 2026-09-30 — Switching provider mid-chat no longer inherits the old provider's account
- Confida: Pi -> Agy in one chat failed 403 "provider connection does not match
  selected provider" (a hard refresh cleared it). `queryRequestForAgentProfileChat`
  filled a missing account from the conversation's saved one, which belongs to the
  old provider. It is now inherited only while the provider is unchanged; the new
  provider gets its default account. An explicit account is always kept.

### 2026-09-30 — The raw terminal drops keys that close the agent; a closed terminal says how to resume
- Ctrl-C (exits on a second press), Ctrl-D, Ctrl-\\ and Ctrl-Z typed in the raw
  terminal are dropped (`stripCLIExitKeys`, `terminal_live_attach.go`) with a
  one-line note. Esc still interrupts and the chat has a Stop button. Bracketed
  pastes pass whole. tmux prefix keys were never at risk: input goes to the pane
  by `send-keys -H`.
- After the 1h idle reaper closes a terminal the view is read-only. The restore
  endpoint deliberately does not relaunch (tool-registration race); the next chat
  message does. `MainAgentTerminal` now says so with a "Back to chat to resume" button.
- Muse under the lock could not write its endpoint lease (`<data>/muse/runtime`);
  granted write on that folder only (`musecli_landlock.go`).

### 2026-09-30 — Costs chat previews expose the latest saved instructions
- View chat in Costs has a collapsed System prompt section showing the latest
  saved system and developer message separately from the paginated chat turns.
  The authorized history GET opts in with `include_saved_prompts=1` and reads
  the canonical archive instead of a resume snapshot that excludes prompts.
- These are saved instructions, not regenerated prompts or an exact request
  archive: native continuation can replace previous system messages, and
  provider internal instructions are unavailable unless the transcript stores
  them. Missing prompts are explicit. Each role has a 64 KiB UTF-8-safe preview
  with truncation labelled; ordinary chat restore remains compact.
- Existing conversation access checks apply before extraction. No agent starts
  or deployment is performed. Code: `chat_history_routes.go`,
  `frontend/src/components/providers/CostConversations.tsx`.

### 2026-09-30 — People can disconnect their own legacy Gmail account
- The shared-account admin gate also blocked removal of personal connections
  created before owner IDs were recorded. Confida has an ownerless Gmail entry
  whose Google-discovered email matches an enabled, non-admin directory user.
- DELETE now permits the recorded owner, or for an ownerless legacy entry an
  exact email match with the authenticated user's current server-side directory
  record. A recorded owner overrides email; private Code accounts stay strictly
  owner-only. Unknown identities, missing emails and disabled users cannot use
  the legacy fallback. Shared account changes and OAuth client management stay
  admin-only.
- Connection responses include `can_remove`. Both account lists enable removal
  separately from management controls; non-admin removal preserves the shared
  OAuth client registration. Workflow read-only access still disables the UI.
- Code: `gmail_connection_routes.go`, `useWorkflowBots.ts`,
  `GmailNotifications.tsx`, `GoogleAccountList.tsx`. Not deployed: deploys remain
  on hold until the next batch of major fixes.

### 2026-09-30 — Costs links usage to saved conversations
- Providers Costs and each expanded workflow/Crew/Code cost date show fresh
  input, cache reads, cache writes when present, output and the percentage of
  total input served from cache. Total input includes the cache buckets once.
  Headline input cards explicitly say fresh input.
- Conversation aggregates retain actor/workspace/session identity, date,
  execution, model and recorded USD. Providers publishes only conversations
  whose work root passed the existing access filter. Opening chat uses the
  existing authorized, bounded history GET; it does not start an agent.
- Conversation details show recorded turns/agent runs rather than equating
  ledger rows with native model requests. Daily details use that date's subset.
  Old servers/records without conversation attribution keep the totals and
  cannot provide a conversation link.
- Excellence investigation: the two Muse cron conversations recorded
  43,671,464 input (33,867,163 cache reads), 32,226 output, and $1.054609626
  on September 30 UTC. Contributor rates already discounted cache reads:
  $0.9804301 fresh + $0.067734326 cached + $0.0064452 output.
- Open issue: native Muse 1.4.1 changed its developer-context temporary
  sandbox path between scheduled runs. Many first requests reported zero
  cache while later requests in that run reused almost all input. Growing
  history raises input per run. These logs suggest prefix invalidation; the
  provider does not report its cache-miss reason. No sandbox weakening or
  fabricated discount is applied to hide the recorded fresh usage.
- Code: `agent_go/pkg/costledger/ledger.go`, `cost_overview.go`,
  `frontend/src/components/providers/CostConversations.tsx` and
  `CostTokenBreakdown.tsx`.

### 2026-09-30 — Daily cost dates expand independently
- Workflow, Crew and Code cost dialogs keep a set of expanded dates, so
  opening another day preserves already visible details for comparison.
  Collapsing a day affects only that day.
- Code: `frontend/src/components/workflow/costs/useCostsData.ts` and
  `CostsDailySection.tsx`.

### 2026-09-30 — Every ledger cost surface uses normalized input
- Extend the Providers token correction to workflow, Code and Crew cost
  dialogs, activity/execution/phase/model details, daily history and embedded
  report widgets. Input and output replace model-call headline counts there.
- Preserve `input_tokens` and pricing coverage when the client merges scopes
  and executions. SQL all-time workflow totals use the same inclusion flag
  and historical Muse fallback as per-event summaries, so opening a cost
  dialog does not turn normalized input into zero or drop missing-usage data.
- Existing run/phase artifacts already carry input/output separately; their
  USD estimates and immutable raw event/token records are preserved.
- Code: `agent_go/pkg/costledger/sqlite.go`, `frontend/src/utils/costTokens.ts`,
  `frontend/src/utils/costActivityBreakdown.ts`, workflow `costs` components
  and `reportWidgets/reportOperationalMetrics.ts`.

### 2026-09-30 — Costs shows input and output, with cache counted once
- The dedicated Costs summary and its user/work/project/account breakdowns
  show input tokens, output tokens and cached input. Cache is part of input;
  model-call counts remain accounting data rather than headline metrics.
- Ledger aggregates expose canonical `input_tokens`. New observer entries
  retain the provider's `prompt_tokens_include_cache` flag; existing Muse
  entries are inclusive. Raw token fields and recorded USD remain unchanged.
  Other historical entries retain the legacy prompt-plus-cache calculation
  because the original inclusion flag was not stored and cannot be recovered safely.
- The Code tab groups Code workspaces separately from other products.
  MCP service breakdowns show the recorded actor, email and tool-call count
  after work access filtering; absent actor IDs remain unattributed.
- Missing token/cost reports are distinguished from usage without a model
  rate. `missing_usage_call_count` is a subset of unpriced calls; neither
  category contributes an invented zero-dollar estimate.
- Visible project rows include their owner's directory email, when available,
  in the list, summary and search. Ownership is distinct from contributors in
  the existing per-user breakdown; access filtering still precedes lookup.
- Excellence's September 28–30 Muse ledger had $3.78333333 in token estimates:
  184,406,599 input, 531,213 output, 150,648,665 cached input. The old UI added
  cache again (335.60M overall tokens). Contributor pricing and cache discounts
  explain the small recorded dollar estimate; it is not an invoice.
- Code: `agent_go/pkg/costledger`, `agent_go/pkg/costobserver`,
  `agent_go/cmd/server/cost_overview.go`, `frontend/src/components/providers`.


### 2026-09-30 — Code agents are told to keep to their own project on the shared server
- A Code project's agent can install and run things on a server that other people's projects
  share. One project's chat installed a browser IDE, exposed it to the internet through a
  forwarder and browsed the server's folders (excellence). A prompt section, `code-host-safety`
  (`prompt_sections.go`, Code product only), now tells the agent to: work inside its working
  folder and never in `~` (a hidden private folder, so files created there are invisible to the
  user); not look at folders or files outside it; not read environment variables or credentials
  it was not given; not install, start or expose remote-access or hosting tools or bind to public
  interfaces (a local dev server on `127.0.0.1` is fine); and not run harmful or unrelated tools.
- This is a prompt-level guard only. It lowers the chance, it does not enforce anything: the lock,
  a default-deny inbound firewall, an environment allowlist for shell commands and resource
  limits are what enforce it, and none of those is done yet (see Open issues).

### 2026-09-30 — Typed slash commands in the browser terminal are limited to an allowlist
- Slash commands change the CLI's own settings or run large commands, both of which the app
  offers itself. The session-switching ones (`/new`, `/clear`, `/resume`, `/fork`) leave the chat
  reading a session the CLI has left, and any slash command leaves a draft the CLI never records
  (Vaibhav, Confida: `/new`, then every chat send refused). So typing them is blocked, except
  `/usage`, which people need.
- The CLI's slash menu can be driven without typing a name ("/", arrow keys, Enter), so
  `terminal_slash_guard.go` follows the line as typed: once a line starts with `/` it forwards
  what is typed, drops arrow keys and Tab, and lets Enter through only for a full allowlisted
  name. Otherwise it drops the Enter, erases the typed line and shows a one-line note.
  Slash text pasted on an empty line is treated the same way.
- The chat's own commands are pasted by the platform on a different path and are unaffected.
- `AGENTWORKS_TERMINAL_SLASH_COMMANDS`: unset = `usage`; a comma list (`usage,status`); `none`;
  `allow` turns the guard off. Other CLIs may name their usage command differently (Codex:
  `/status`); add them to the list per server once checked.
- Limits: the guard only sees the line as typed in the browser terminal. History recall (Up arrow)
  fills the line without it noticing, so a `/` typed after a recall is treated as a command start.
- Also on main, not deployed: a typed terminal draft no longer blocks a chat send.

### 2026-09-30 — A reply to a live message is held until the message row (all CLIs)
- When a message is sent into a running CLI, its chat row is written when the
  CLI confirms it took the message (`watchLiveInputDurableRecording`), and the
  event store holds the reply's rows until then (`steer_ordering.go`) so the
  reply never lands above the question.
- The hold was skipped whenever the session looked "mid-answer". Answer rows
  that arrive after their turn ended (Cursor's late transcript chunks, native
  transcript catch-up) marked it mid-answer with nothing left to end it, so
  Cursor's first reply line to a message landed above that message (RTS
  rts-pr-reviewer: the line looked missing; it was above the message).
- Now a session is mid-answer only if an answer row arrived in the last 15 s
  (`deferredSteerQuietWindow`) or a tool call is still running. Past that it is
  idle and the reply is held. Trade-off: a CLI silent for over 15 s mid-answer
  with no tool running (very long thinking) can still sort a message ahead of
  the rest of that answer. Tune the window if that shows up.
- NOT DEPLOYED yet (on main, e8340db68). Deploys are on hold until the next
  batch of major fixes.

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
- **Pi bridge:** read/exec on the bridge program named in Pi's private
  `mcp.json` (`piLandlockReads` reads `PI_CODING_AGENT_DIR` from the launch
  environment). Without it Pi started but its platform tools failed with
  "api-bridge: failed: spawn .../mcpbridge EACCES". The launch folder, the
  extension cache and the bridge were three separate grants; a confined Pi
  needs all three.
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
  guard grants, and explicitly granted runtime files. Only **Cursor** also
  receives the shared `/tmp` grant (tested per provider in
  `internal/clisandbox/landlock_tmp_test.go`).
- `/tmp` is writable for Cursor because it keeps sockets at fixed `/tmp` paths
  (`cursor-askpass-*.sock`, and `/tmp/.cursor/<project>` when its home path is
  too long for a socket). Every other CLI uses its private `TMPDIR`: Muse, Pi and
  Agy were started under the real launcher without `/tmp` and made no `/tmp`
  access; Claude and Codex ran confined on RTS before the grant existed.
  Verified 2026-09-30; a CLI that turns out to need `/tmp` is added by name.
- Muse's private `TMPDIR` was first narrowed by a separate patch (provider
  `3428203`, review PRs provider #39 / builder #258); the Cursor-only rule
  above replaced it and covers Muse the same way. A canary test with the real
  launcher shows another CLI's `/tmp` file can be neither read nor overwritten.
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

## Deploy state (2026-09-30, deploys on hold)

- **Live:** RTS `fa635b8` (Cursor store order); Confida (Pi launch folder,
  extension cache, bridge; Pi confirmed working); excellence (Muse hooks,
  resume adoption).
- **On main, not deployed:** the steer-ordering fix (`e8340db68`).
- Deploys wait for the next batch of major fixes. Deploy from a clean
  worktree; the server clones main of all three repos.

## Open issues

- **A Code chat's shell command started processes outside the lock (found 2026-09-30).** They ran
  as the server account with its full environment and reached the internet; shell commands also
  inherit server secrets that are not on the environment denylist (`AUTH_SECRET`,
  `ACCESS_PASSWORD`, `ADMIN_USERS`). Needs: close the way out of the lock, an environment
  allowlist in `buildNativeEnvironment`, a default-deny inbound firewall, per-CLI resource limits,
  and rotation of the exposed secrets. Longer term, a separate Linux account per user.

- **Confinement can be skipped when the launcher is unavailable.**
  `applyCLILandlock` logs and runs the CLI unconfined when `CLILandlockRunner`
  fails its capability check, even with `AGENTWORKS_CLI_LANDLOCK=on`, so
  "confine every CLI" is not fail-closed. Decide: refuse to start the CLI, or
  keep failing open with a loud log.
- **Shared browser paths remain launcher grants.** CLI policies leave
  `BrowserScoped` false, so `landlockSystemWritePaths` grants the shared
  browser socket/temp folders and, when set, shared-profile roots for users,
  workflows and projects. Cursor-only `/tmp` does not remove these.
- **Muse per-launch login copy is not returned.** `musePrepareIsolatedConfig`
  copies `auth.json` into the per-launch config folder and deletes it after the
  launch, so a token Muse refreshes is lost. The "logins are linked, never
  copied" rule above does not cover this Muse path. Likely the next failure
  for Meta-authenticated users.
- **Shared `/tmp` for Cursor.** Cursor's confined sessions can read and write
  `/tmp`: other CLIs' fixed-path temp files, the
  workspace command scratch (`/tmp/aws-<uid>`) and the browser sockets
  (`/tmp/.agent-browser`). Landlock also does not govern `connect()` to
  Unix sockets, so the tmux socket (`/tmp/tmux-<uid>/default`) is reachable
  from a confined CLI whatever its grants. Fixing both needs the private mount
  namespace above.
- **Confida Slack replies about 90 s late.** The gap is before the session is
  saved; `[BOT_TIMING]` and `[BUILDER_RESTORE_TIMING]` logs are in place to
  name the slow step (suspect: `restoreLatestBuilderConversation` reading every
  saved Builder conversation).
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
