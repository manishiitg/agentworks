# Decisions and open issues

A running log for people and coding agents working on this repository. Read it
before changing behaviour it covers; add an entry (newest first) when you make
or reverse a decision, and move an open issue to a decision once it is settled.
Each entry says what was decided, why, and where it lives in the code.

Design references for the linked runtime decisions:

- [Crew Run/Builder roles and private project links](design/project_instruction_files.md#crew-linked-runtimes).
- [Workflow Run/Builder project links, permissions and resume](design/workflow_shared_folder_plan.md).
- [Workflow step output links and artifact lifetime](design/project_instruction_files.md#workflow-step-outputs).

## Decisions

### 2026-10-03 — Terminal: wheel scrolls the history, coloured output, plain "command not found"

- **Found (user).** The wheel did nothing; the tmux status bar showed at the bottom; `ls`/`grep` were one colour; `nvm install 24` (nvm not installed) printed Ubuntu's Python
  "command-not-found has crashed" report, because its database cannot be opened inside the sandbox.
- **Done.** tmux starts with `mouse on`, `history-limit 50000`, `status off` (the browser has no scrollback of its own, tmux draws the screen). A sandboxed shell sets colour
  aliases (GNU) or `CLICOLOR` (BSD), defines a plain `command_not_found_handle`, and sources the person's own `~/.bashrc` once (the private home, where `nvm` puts itself).
  Tested: Mac (sandboxed, unconfined, wheel), Linux non-slot, and as a user's own account on Excellence and Confida.
- **Open.** Whether the nvm installer itself works inside the sandbox is not yet tested as a slot user.
### 2026-10-03 — Coding CLI confinement has no switches: the platform decides, and servers fail closed

- **User decision.** Test with Full native tools everywhere, never run a server
  without the lock, and drop the environment switches so local runs and servers
  cannot drift on a forgotten setting.
- `AGENTWORKS_CLI_LANDLOCK`, `AGENTWORKS_CLI_FULL`, `AGENTWORKS_CLI_FULL_UNCONFINED`
  and `AGENTWORKS_TERMINAL_UNCONFINED` are removed, with their `users:` rollout
  lists. A chat with Native agent tools now runs:
  - on a person's own Mac (macOS, not `MULTI_USER_MODE`): Full CLI unconfined, and
    Code's terminal unconfined (the workspace service also requires native mode
    and no slots);
  - on Linux, every server included and whether or not `MULTI_USER_MODE` is set:
    Full CLI inside the Landlock lock;
  - when the lock cannot be applied (no working launcher, no working folder, the
    policy cannot attach), or on a multi-user Mac: `mcp_only`, bridge tools only,
    logged as `[CLI_LANDLOCK] SECURITY`. It never falls back to unconfined.
- Before this, a host whose launcher failed its preflight ran CLIs unconfined
  with one log line, and a server that forgot the switch ran them unconfined.
  RTS confined every CLI session in its current log (Cursor and Claude, Full
  inside the lock), so nothing changes there.
- No emergency off switch on servers: a CLI that cannot run inside the lock
  stays usable bridge-only instead of being exempted.
- Next: macOS Seatbelt (Claude first) so a Mac is confined like a server.
- Tests: `TestDecideCLIConfinement`, `TestRestrictCodingAgentToolsToMCPOnly`,
  `TestInteractiveShellUnconfinedIsLocalOnly`.

### 2026-10-03 — In a sandboxed terminal an empty `cd` returns to the project folder

- **Found (user).** An empty `cd` went to "some root folder" with no way to tell where it was or how to get back: in a sandboxed terminal `$HOME` is the shell's private
  home inside the project (`.sandbox-cache/home`), which bash showed as `~`.
- **Done.** The terminal's `PROMPT_COMMAND` defines `cd` so that no argument (or `~`) goes to the folder the terminal started in (`AGENTWORKS_START_DIR`); `cd -`,
  `cd <path>` and `cd ..` are unchanged. The prompt names the folder (`${PWD##*/}`), so the private home reads `home`, not `~`. An unconfined terminal (the person's own
  machine, real home) keeps the normal `cd`. Tested on a Mac (sandboxed and unconfined) and as a user's own account on Excellence and Confida.

### 2026-10-03 — Code's terminal: Homebrew colours and a short prompt

- **Decision (user).** The terminal was plain white on black (it only set a background; xterm's default text is white), and on a server the prompt was
  `user@host:/srv/agents/data/docs/_users/<id>/Chats/Code/projects/<project>/code$`, wider than the screen.
- **Colours.** A Homebrew scheme (macOS Terminal's classic profile: black, bright green `#28fe14`, a green cursor, a full 16-colour palette) is the default;
  a palette button in the toolbar switches to Classic (the coding-tool terminals' look) and the choice is remembered. Homebrew's blues are lightened: the
  original dark blue is unreadable on black, and `ls` prints directories in it.
- **Prompt.** The shell sets `PROMPT_COMMAND` so the prompt is just the current folder's name in bold (`code $`); bash runs it before every prompt, so it holds
  whatever `/etc/bash.bashrc` or a profile sets `PS1` to. A shell already running keeps its old prompt until Stop and Start.
- **Checked.** Frontend tests (palette, readability of the blues, wiring); on a Mac the sandboxed and the unconfined shell show `a $`; on Excellence a
  user's own-account shell reports the short `PS1`. One non-slot Linux e2e run failed on a loaded server and did not fail again in five re-runs (those tests
  use fixed waits).

### 2026-10-03 — Browser teaching presents reusable skills and supports tabs

- **Decision (user).** Keep browser internals out of the ordinary product flow.
  Move Start browser into the top header, compact local settings and place
  Chrome connection details under Advanced. Server uses the workspace browser.
- **Teach UI.** The helper reviews the private manifest. The panel refreshes it
  automatically and shows goal, inputs, expected result, readiness, Try task and
  Save skill. Raw actions, locator warnings, step removal, guidance editing and
  artifact paths are no longer shown. A failed try stays unsaved and offers a
  request for helper adjustment; successful-test receipt checks remain required.
- **Tabs.** Allow manual creation, selection and closing while teaching. Bind
  each recorded page to a fresh target during replay; resolve site-created
  popups by their mapped opener and refuse ambiguity. Existing unrelated Chrome
  tabs are not automatically recorded. Closed targets leave capture listeners;
  new attachments preserve Pause. Keep at least one tab open in the viewer.
- **Verification.** Real Chrome covers opening a tab, switching back, a popup,
  closing it, repeated replay with fresh IDs and privacy for tabs selected while
  paused. Control and trusted-launch flags remain required for tab commands.
- **Deployment.** RTS deployment was requested for user testing. Release and
  server runtime verification follow the existing guarded deployment script.
  RTS upgrades an older agent-browser in its service account tool prefix to
  0.38.2, the minimum version providing the qualified teaching primitives.
- **Guide.** See [Browser](core/browser.md) for storage conventions and remaining
  unsupported interactions; the reusable file belongs to its workflow/project.

### 2026-10-03 — Deploy notices in Slack are back on by default (reverses the opt-in earlier the same day)

- **Decision (user).** `deploy.sh` posts "deploying" and "finished" (or the "finished with a problem" warning) to the Slack channel again. Silence one run with
  `DEPLOY_SLACK_NOTIFY=0` (or `false`, `no`, `off`). The webhook is read from `DEPLOY_SLACK_WEBHOOK_URL` or `~/.config/agentworks/deploy-slack-webhook`
  as before; with none set nothing is sent. The false "problem" notices that made the opt-in attractive came from the early health probe, fixed on
  2026-10-01 (deploys now wait for the agent), so a healthy release no longer reports a problem.
- **Checked** against a local fake receiver (the real channel was not posted to): default success 2 messages, default failing run 2 messages (start + warning),
  `=0` and `=off` none, `=1` 2; the deploy's exit code is kept in every case. Both the start and finish notice respect the switch.

### 2026-10-03 — A better-looking terminal in Code: xterm.js plus its official add-ons, themed like the coding-tool terminals

- **Decision (user):** a better designed terminal, using open source out of the box. We already use xterm.js (the engine behind VS Code, Hyper,
  Tabby and JupyterLab); ttyd/wetty/GoTTY would add a second server and bypass the slot and sandbox setup, so they were not used.
- **Done.** `CodeShellPanel` now uses the coding-tool terminals' theme and font (`RAW_XTERM_THEMES`, `RAW_XTERM_FONT_FAMILY`), and adds the official
  add-ons: WebGL rendering (falls back by itself), clickable links (http/https only, in a new tab), search (Ctrl/Cmd+F, highlights, Enter / Shift+Enter),
  Unicode 11. A toolbar offers search, copy, paste, clear, text size (10-22, remembered), full screen, a status dot and a quiet automatic reconnect
  (4 tries, 1-8 s) before it asks for a click. New packages: `@xterm/addon-webgl`, `-web-links`, `-search`, `-unicode11` (the start script's
  `npm install` picks them up locally; servers build with `npm ci`).
- **Checked.** Unit tests for the helpers and the wiring; the real panel rendered in a browser against a fake connection (colors, toolbar, search
  highlight). Not yet checked against a real shell in a browser.

### 2026-10-03 — Builder chooses Gmail senders; email fetch and access disclosure

- **User decision.** Owners can accept Real Training OR notification senders and
  alternative subject/body phrases through Builder. This extends the previous
  owner-only sender decision; omitted/cleared sender lists retain that default.
- **Rules.** Exact email and exact `@domain` entries match with OR, never display
  names, wildcards, subdomain suffixes or unauthenticated From headers. Existing
  keyword arrays remain AND; new `*_contains_any` arrays use OR. Groups combine
  with AND. Only interactive owners configure; allowed senders run the saved
  owner scope and workflow binding without receiving configuration authority.
- **Notifications.** Explicit sender lists may opt into automated notifications.
  Auto-replies, bounces, spam and trash remain blocked. Admission, queued work
  and final response recheck authorization; removing a sender can prevent a final
  email response even when the task has already started.
- **UI.** Incoming email stays read-only across Crew, Workflow and Code. Replace
  delivery-status refresh with Fetch emails, which sends a read-and-summarize
  request to the target's chat/Builder using its saved mailbox and rules. It can
  read Gmail without Pub/Sub and does not execute or replay the saved trigger.
  Google Change access opens/closes from its header and keeps unsaved choices
  while collapsed. The collapsed header shows the unsaved change count.
- **Implementation.** `pkg/gmailinbound` and server trigger authorization/tools;
  `GmailInboundPanel`, `GoogleAccountConnect`; shared Builder skill and owner guide.
  Backend admission/authentication regressions and UI action/state tests cover
  the behavior. Source changes only; this task does not deploy to RTS.

### 2026-10-03 — Granted folders outside the workspace pass the folder-guard write-path check (amends 2026-09-30)
- The 2026-09-30 boundary check rejected every absolute write path outside the
  workspace with HTTP 400. Local sessions legitimately carry such grants
  (Downloads, a project folder), so every shell command from those sessions
  failed with "Invalid folder guard write path" — including bare `pwd` — and
  scheduled Pulses and Code chats could do nothing.
- The hole that check closed was directories created anywhere as the service
  account. An absolute path outside the workspace is now accepted only when it
  already exists as a directory, and it is never created or resolved through
  anything. A missing outside path, a relative path, any `..` segment, a file,
  and anything lexically inside the workspace (symlink redirects) still fail
  with 400 and create nothing; the 400-and-nothing-created handler test is
  unchanged.
- Tests: `TestIsExistingHostGrant` in `shell_guard_writepath_test.go`.

### 2026-10-03 — Code's terminal follows the coding agents' sandbox switch (local: your own machine; server: confined)

- **Decision (user):** the terminal should have the same settings as the coding agents, locally and on servers.
- **Local (done).** On a person's own machine the coding agents run with full native tools unconfined, real home and rights
  (`AGENTWORKS_CLI_FULL_UNCONFINED`, on by default in the start script, refused by the agent server on a multi-user server). The terminal now
  follows that same switch: the agent server sends `unconfined`, and the workspace service honours it only when `AGENTWORKS_TERMINAL_UNCONFINED=on`
  (the start script derives it from the switch above), `NATIVE_WORKSPACE=true` and per-user accounts are off. So `git`, `codex`, `claude` find their
  normal config in the real home. Tests: `TestInteractiveShellUnconfinedIsLocalOnly` (six cases incl. servers) and a Mac end-to-end check that the
  unconfined shell has the readable real home while a non-requested one stays sandboxed.
- **Server (unchanged, deliberately).** The terminal keeps the strict Landlock sandbox and runs as the person's own account, which is stronger than the
  chat coding tools get today (they run as the shared platform account except for the one rollout user). Aligning the server terminal *down* to the chat
  tools' rollout would weaken it; making the chat tools match the terminal is the open "widen CLI-as-slot" item.

### 2026-10-03 — The local terminal had the real home folder, which the sandbox forbids

- **Found (user, local terminal).** `bash: /Users/mipl/.bash_profile: Operation not permitted`, `git` unable to read `~/.gitconfig`, `codex` unable
  to read `~/.codex/config.toml`. In native mode (the local app) the sandboxed command keeps the real `HOME` so host tools find their config
  (`privateSandboxHome`), but Code's strict sandbox forbids reading it. The terminal now gets a private home inside the project
  (`<project>/.sandbox-cache/home`) when it does not run as a slot; slots and servers already had one. `claude` and `codex` are not on the
  sandbox's PATH and have no login there: the coding agents run through the chat, not the terminal.
- **Test.** `interactive_shell_darwin_test.go` now runs natively and fails with exactly these errors without the fix.
- **Terminal icon** changed to the plain `>_` (`Terminal`) in the toolbar and the panel header.

### 2026-10-03 — Qualify Cursor's RTS account through the installed CLI

- Tested RTS / Video Studio on its AWS EC2 host (the deployment named RTS in
  `deploy.sh`), using the service's existing Cursor API key through SSM. SSH
  timed out. No deployment, service restart, CLI update or saved-account change
  was performed. Every test used the service UID, a disposable HOME/workspace,
  read-only Ask mode and a no-tools echo prompt; temporary folders were removed.
- Installed Cursor CLI is `2026.10.01-e373342`, matching the version served by
  Cursor's official installer at verification time on October 3.
- `--list-models` succeeds. It exposes Grok 4.6 variants and GLM 5.2 High/Max,
  but neither GLM 5.3 nor Flash. Both `glm-5.3` and `glm-5.3-flash` exit 1 with
  `Cannot use this model`. Documentation/catalog presence is not proof of
  availability for this deployment's key.
- Both `grok-4.6` and `cursor-grok-4.6-high` return `CURSOR_TEST_OK`, exit 0, and
  report successful result/usage events. Their native runtime labels are
  respectively `Grok 4.6 High Fast` and `Grok 4.6 High`; the short canonical ID
  therefore selects Fast for this account. These probes qualify CLI inference,
  not the app's retained sessions, native tool execution or full coding loop.
- Source for release comparison: [Cursor installer](https://cursor.com/install).

### 2026-10-03 — Browser startup, scope settings and structured teaching implemented

- **Decision (user).** Implement the browser ownership, manual sign-in and teaching
  design discussed above. Ordinary workflow `none`/missing modes now migrate to
  `auto`; server deployments with CDP disabled use managed Chrome even for old
  CDP settings. SparkQuill child and Dominion restrictions remain enforced.
- **Done.** Shared browser panels can start/reuse the scope's browser before an
  agent runs. Project `.browser-settings.json` is canonical. Managed scope
  profiles persist by default without enabling the legacy global shared browser;
  explicit existing ephemeral/profile environment overrides retain their intent.
- **Teaching.** The private CDP recorder captures genuine DOM actions in the
  existing selected Chrome, navigation and eligible visual evidence. Exclusive
  manual control also blocks agent CDP actions. Login precedes teaching; sensitive
  fields and paused edits are excluded. Disconnect/timeout yields interrupted
  evidence, not a tested skill. The helper reviews a draft in the scope; the user
  reviews parameters, guidance and a page outcome before real-action replay.
- **Reuse.** Publication requires a service-held successful-test fingerprint.
  Workflows link a learning reference from `_global/SKILL.md`; projects save under
  `skills/`, Crew/Code select the skill, and product prompts point to the scope's
  tested-procedure index. No per-user browser/learning store was introduced.
- **Server handoff.** Browser shell requests carry the trusted account identity
  and a scope guard. Startup lists tabs instead of calling URL-less `open`, which
  resets the page in the qualified runtime. Local CDP startup takes the shared port lock too. Viewer/teaching commands retain the
  selected runtime launch flags; attached CDP never acquires managed profile
  flags. Chrome IPC uses its private scope directory beyond command cleanup.
  Docker Compose shares a persistent profile volume and the same absolute profile
  base between services, avoiding different container-home defaults.
  Teaching files inherit the workspace group so Linux account slots can review
  the draft and read published skills without opening another account's scope.
- **Verified locally.** Authenticated workspace-service startup through replay
  and publication, repeated Start retaining the signed-in page, real Chrome
  capture/replay, semantic targeting, parameter
  input, sign-in persistence on restart, JPEG evidence, repeated sessions and
  password/paused-navigation privacy; Go race checks, API/control tests and the
  production frontend build. Runtime proof uses agent-browser 0.38.2. Deployment
  updates/restarts are not performed by this source change.
- **Baseline test limits.** Broader product checks still fail on
  `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID`,
  `TestSalesCrewCatalogHasInstallableRoles`, and `TestCodePreparedSystemPrompt`
  (missing Claude deployment token). All three also fail in a separate clean
  `origin/main` worktree; they are not introduced by the browser change.
- **Open coverage.** Multi-tab replay, cross-process frames, shadow DOM, canvas,
  native dialogs/uploads and downloaded-artifact outcome validation need further
  qualification. Page text/URL checks do not prove a download. CDP recorder
  connections currently require loopback; host-Chrome/container attachment needs
  qualification. Broad retention/quota administration remains follow-up work.
- **Guide.** [Browser](core/browser.md) is the consolidated reference, updated with
  implemented behavior, runtime requirements and exact remaining limitations.


### 2026-10-03 — Google service permission cards and resumed Claude quota notices

- Google account setup uses the existing Google Workspace brand marks, service
  cards and explicit Add/Remove controls across products. Existing accounts show
  all six services' saved agent permissions; Change access keeps the saved value
  visible alongside unsaved selections. Cancel restores new-account defaults.
- Permissions remain account-owner/admin controlled. Removing Gmail agent access
  leaves server notification sending available; removing a service restricts
  agent access and does not claim to revoke the Google OAuth token. Changes keep
  the connection ID and its OAuth client, then request Google consent as before.
- RTS investigation: at 2026-10-03 08:25 UTC the resumed SDE Code session reported
  quota exhaustion while its native transcript recorded successful tool calls.
  Its only native quota notice was from October 1. Resume redraw made historical
  scrollback appear new before a structured usage statusline was available.
  The provider now considers quota notices only after the latest nonempty user
  prompt, preserving fresh walls, empty-composer cases and other fatal statuses.
  Provider fix: `e38d33f`; regression tests live in
  `claudecode_resumed_quota_test.go` in the provider repo.
- Validation: Google permission component/integration tests, production frontend
  build and dark/light/narrow visual checks; Claude adapter tests. Live RTS still
  needs a deployment of these changes; this investigation did not restart it.

### 2026-10-03 — Cursor offers GLM and Grok choices plus the CLI's live list

- Add Cursor's officially documented `glm-5.3`, `glm-5.3-flash`, and `grok-4.6`
  to the curated catalog alongside Auto, Composer 2.5 and Grok 4.7, with model
  metadata/pricing from the official pages. Parameterized selectors retain the
  selected model and its pricing family.
- Crew/Code Models merges the Cursor CLI's live model list into the curated
  manifest catalog, deduplicating IDs and preserving known metadata. Extra CLI
  model IDs remain exact when saved, and list failures retain curated choices.
- Availability and release date: Cursor documents both GLM models and Grok 4.6;
  no primary source established an exact GLM launch date. Local CLI list access
  was signed out, so live account access has not been certified. Codex's OpenAI
  model list does not document GLM; adding it would require an explicitly
  configured compatible Responses gateway/provider and credentials.
- Sources: [GLM 5.3](https://cursor.com/docs/models/glm-5-3),
  [GLM 5.3 Flash](https://cursor.com/docs/models/glm-5-3-flash),
  [Grok 4.6](https://cursor.com/docs/models/grok-4-6),
  [Codex models](https://learn.chatgpt.com/docs/models?surface=cli).

### 2026-10-03 — My local product-list change wrote an invalid runtime config (empty value)

- **Found (user, local: Code still missing from the switcher).** The generated `frontend/public/runtime-config.js` had
  `enabledProductSurfaces: ,`: the variable was defined in the middle of `run_server_with_logging.sh`, after the early path (`--only-frontend`)
  had already written the file. An empty value is a syntax error, so the whole config was ignored. It is now defined at the top of the
  script, and I tested the real writer function (output parsed by node, with and without the override).

### 2026-10-03 — The terminal did not start on a Mac with the strict sandbox (two causes)

- **Found (user, local):** the Terminal tab showed "disconnected"; the server log said "error connecting to /tmp/.agentworks-shells/.../tmux.sock
  (Operation not permitted)". My Mac check had used the lenient sandbox; Code uses the strict one. Two causes, both Mac-only:
  (1) the strict profile did not allow terminal devices, so tmux could not create its pty and its server exited ("fork failed: Operation not
  permitted" when run by hand); (2) `/tmp` is a link to `/private/tmp`, the strict profile grants the real folder and will not follow the link,
  so a socket named through `/tmp` was refused. Now the profile allows pseudo-ttys only when a terminal is requested (`AllowPTY`), and the
  shells folder is named by its real path (`interactiveShellRootPath`). Linux is unaffected (its tests pass on Excellence).
- **Test:** `interactive_shell_darwin_test.go` (strict guard, real socket path, stop). The earlier Linux attach/escape checks also pass on
  a Mac with the strict guard when the test's other project is outside `/var/folders` (which the strict profile grants as scratch).

### 2026-10-03 — Project reasoning controls and creation account names

- Crew/Code Identity → Models renders the product profile's reasoning-effort
  choices alongside models, including Muse's medium/high/xhigh/max options.
  The selected effort is saved with the project and retained on model changes.
  Muse defaults to max in both profiles; an admin-managed account does not lock
  model or reasoning settings. Antigravity continues to encode effort in model IDs.
- New project “Runs on” uses the same Admin-managed account label as Providers,
  and offers installed CLIs with ready accounts only. With none ready, it directs
  the person to Providers without suggesting a signed-out provider.
- Code: `WorkModelsPanel`, `RunsOnPicker`, shared readiness/account-label helpers.

### 2026-10-03 — The terminal starts on a Mac (tmux by full path)

- **Found (user asked whether the terminal shows locally).** On macOS the shell did not start: the sandbox's trimmed PATH lacks Homebrew's
  folder, so `tmux` was "not found". The workspace service now runs tmux by its full path (`/usr/bin/tmux`, else the one on the service's
  PATH), for the start and the attach. Checked on a Mac: the shell starts, writes only inside its project, and cannot read or write
  another project; typing through the attach works. The Linux tests (Excellence, slot and non-slot) still pass. Inside a Mac shell
  `tmux` itself is not on PATH (same trimmed PATH as the agent's shell tool).

### 2026-10-03 — Google account sign-in and compact layout across products

- **Decided.** The shared Email panel selects the platform Google app flow for
  Code, Crew, workflow and relay targets. It no longer depends on a Code-only
  product check or rejects owner-prefixed Code paths. Local and deployed
  instances use the same flow; deployments without a Google app retain the
  legacy client-upload fallback.
- **Permissions.** Code accounts remain private to their owner; shared accounts
  remain admin-managed. The unified form respects existing read-only permission
  checks and describes the correct account scope. Default-account selection is
  preserved in the compact account menu.
- **Existing accounts.** Change access updates and reauthorizes the existing
  connection ID with its existing OAuth client, preserving trigger references
  and legacy clients. Send-only access is not silently promoted to read access.
  Change-access events are scoped to the displayed workspace.
- **Validation.** Component tests cover all target paths, prefixed Code, shared
  readers, legacy reconnection, workspace isolation and missing-app fallback.
  The owner requested an RTS deployment of latest main for Gmail testing;
  Pub/Sub provisioning remains a separate prerequisite, checked during rollout.

### 2026-10-03 — Project model choices require a ready account; remove older Codex choices

- Refine the earlier installation-only rule: Workflow/product Setup and Crew/Code
  Identity → Models offer installed coding providers with a configured, usable
  admin-managed or personal account in the current project/product scope.
  A signed-out admin-managed account does not hide a working personal account.
  Providers needing setup remain available in Providers management.
- Saved unavailable selections remain visible for diagnosis as disabled values;
  opening settings never substitutes another provider/account. Account choices
  omit unconfigured accounts except a saved selection that needs attention.
- Remove GPT-5.5 and GPT-5.4 from the Codex CLI selectable catalog and automatic
  published role models. Retain metadata/runtime handling for saved sessions.
- Code: `readyCodingProviders`, `WorkflowLLMConfigurationPanel`, `WorkModelsPanel`,
  `LLMRoleSelector`, `published_llm_store`, and the provider's `codexcli_models`.

### 2026-10-03 — Deploy notices in Slack are opt-in

- **Decision (user).** `deploy.sh` no longer posts to the Slack channel by default (it posted "deploying" and "finished" for every deploy,
  including false "problem" notices). Set `DEPLOY_SLACK_NOTIFY=1` for a run to get them back; the webhook settings are unchanged
  (`DEPLOY_SLACK_WEBHOOK_URL` or `~/.config/agentworks/deploy-slack-webhook`). Change: aa70c8802.

### 2026-10-03 — The Terminal button did not show, and would have opened nothing (two pieces lost in the port)

- **Found (user, after the Excellence deploy):** no Terminal option in Code. Two pieces of the 2026-09-28 code were lost when it was ported
  onto today's files: `WorkSurface` never passed `showShell` to the toolbar (which hides the button by default), and the pane imported
  the panel but never rendered it. My tests had checked the label text and the server side, not that the button was wired up.
- **Done.** Both restored. Source tests pin the toolbar prop and the pane's render line; a render test checks the button appears for a Code
  the caller owns and nowhere else. Still not clicked through in a real browser.

### 2026-10-03 — The Models screen's usage check asks about the project's own account, and is shown to everyone the server allows

- **Question (user).** Providers got the usage-access fixes; does Models (Setup → Models in a Code/Crew) check usage with the same security?
- **Answer.** The server side is the same: both screens call `POST /api/provider-setup/sessions` with action `usage`
  (`handleStartProviderSetup`): a user account's owner and admins get a terminal; someone the account is shared with, or anyone the
  server account is available to, gets read-only text collected by the server (never a terminal); anyone else gets 403. Tested in
  `provider_accounts_e2e_test.go`.
- **Gaps found in the Models screen (UI only), fixed.** (1) The button was hidden unless admin (`canCheckUsage`), so ordinary users never
  saw usage even where the server allows it. (2) It never sent the project's connection id, so it always asked about the **server's own
  account**, not the account the project uses. It now sends the project's connection (`checkProviderUsage(provider, connectionId)`),
  is shown to everyone, opens the terminal for those the server gives one and shows read-only text for the rest.

### 2026-10-03 — Browser ownership and teaching plan; guides consolidated

- **Agreed direction (user).** A browser belongs to a workflow or product project
  (Crew/Code etc.), not to a person. Local supports managed browser/CDP; server
  uses managed Chrome. Normal setup should not ask users to disable browsing.
  Deliberate product restrictions, including SparkQuill child, remain enforced.
- **Current source.** `browser_conversation_isolation.go` already resolves per
  workflow/project. Persistent profiles require deployment configuration. Mode
  defaults/settings still differ across products; workflow `none` remains active.
- **Proposed, not implemented.** Let a user start/reuse the scoped browser and
  sign in before an agent runs. Teach records the existing browser's structured
  DOM actions plus lifecycle/visual evidence, then drafts a scope-owned procedure
  for reviewed parameters, replay and outcome validation. Login stays outside
  teaching; interruption and unsupported capture remain explicit.
- **Open.** Implement start/sign-in, canonical project settings and legacy-mode
  migration; qualify the private recorder attachment and sensitive-input handling;
  build teaching/replay. This commit changes documentation only, not defaults,
  permissions, browser startup or deployment configuration.
- **Guide.** [One browser reference](core/browser.md) now includes automation,
  live viewing/control, diagnostic capture, authoring and the staged teaching plan.
  Superseded guides were removed and indexes updated. Historical rollout notes
  do not establish today's server status. External Grok reconstruction evidence
  is labeled unofficial, with its unverified learning internals stated.

### 2026-10-03 — Local runs offer Code in the product switcher

- **Found (user, local).** The start script's runtime config never set `enabledProductSurfaces`, so the frontend used its own default
  (`agentworks`, `relays`, `work`) and Code never showed locally.
- **Done.** `agent_go/run_server_with_logging.sh` now writes `enabledProductSurfaces` with Code included; override with
  `AGENTWORKS_ENABLED_PRODUCT_SURFACES='["agentworks","work"]'`.
- **The 401 on Crew in the same session** came from a frontend started before the local checkout was updated: it still called the workspace
  service directly (`/api/documents...` on port 18744), which now needs the server's token. Current code goes through the agent's `/api/wp`.
  Restarting the local frontend/desktop app after an update clears it.


### 2026-10-03 — Providers page uses the main header's Back and Antigravity's icon

- Embedded Providers uses the application header's Back control; remove the
  duplicate arrow beside Available providers. Standalone modal Providers keeps
  its Close control. Leaving the embedded page still retains any guided terminal.
- Add Antigravity's official favicon to both frontend and server static provider
  assets and map `agy-cli` to it instead of the generic terminal icon.

### 2026-10-03 — Installation provider accounts are called Admin-managed accounts

- Use “Admin-managed account” for the installation's provider account in Providers,
  workflow/product model setup, account pickers, product defaults and account cost
  reports. The name distinguishes administrative ownership from personal account
  sharing without implying that everyone may use it. Existing availability text
  continues to say who has access.
- This is a display-name change only. Account IDs (`global:<provider>`), ownership,
  availability and credential resolution stay unchanged.

### 2026-10-03 — Setup model choices show installed coding providers only

- Workflow and product Setup → Models, including per-role choices and the global
  model configuration modal, offer only enabled, non-deprecated coding CLIs whose
  provider manifest reports `runtime_available: true`. An installed CLI remains
  visible when it needs sign-in; authentication is not an installation check.
- Published models and product profile catalogs cannot reintroduce absent CLIs.
  A saved role on an absent provider stays visible as a disabled current value,
  so opening setup does not silently change existing configuration. The Providers
  management screen retains its installation/setup catalog.
- Code: `providerCatalogFilter`, `WorkflowLLMConfigurationPanel`,
  `LLMConfigurationModal`, `LLMRoleSelector`, and `WorkModelsPanel`.

### 2026-10-03 — A terminal in Code, run as the person's own Linux account (reverses 2026-09-28)

- **Decision (user, 2026-10-03).** Code gets a Terminal tab again: a real shell on the server in the Code's folder. The
  2026-09-28 removal ("the user decided it wasn't needed") is reversed on the user's request; what changed is that every
  person now has their own Linux account (slot), so a raw shell no longer runs as the shared service account.
- **Design.** The old panel (commits 7affa8a90 / dc8cdb8c4) was ported, not reverted (the code had moved on). The agent
  server authorizes the owner (Code is owner-only, so the terminal is too), builds the Code's Folder Guard and stamps the
  user on every call; the workspace service starts a tmux server in the same Landlock sandbox as the shell tool (private
  /tmp, private /dev/pts) and attaches from inside it. **Where slots are on, all of it runs as the caller's slot**:
  `slots.WrapCommandFile` leaves the request in a file in the slot's run folder so the terminal stays the command's stdin
  (the stdin form of `WrapCommand` cannot), the tmux files live in `<slot run folder>/shells/<id>` (group-shared with the
  service; tmux makes its socket owner-only, so it is `chmod 0660` after start), and **a person without a slot gets no
  terminal** (403) instead of a shell as the service account. Hosts without slots keep the old sandboxed behaviour.
- **Scratch folders** the platform creates for a sandboxed command (`.tmp`, `.cache`, ...) are now group-writable: a slot
  could not create a temp file in its own TMPDIR (the private-terminal launcher failed on this).
- **Not on RTS yet.** A raw shell can reach the instance role through IMDS; that exposure is still open there.
- **Known limit.** Code's sandbox is strict, so this works. A *non-strict* guard as a slot still fails on a host where the platform's
  Gmail tool config folder is service-only (Excellence: `stat .../gog: permission denied`); that affects non-strict workflow
  shells too and is tracked as the open gog-config gap.
- **Tests.** Real-sandbox shell tests on a Linux host (`interactive_shell_e2e_linux_test.go`, including private PTY) and
  `interactive_shell_slot_e2e_linux_test.go`, run on Confida: the shell is the user's slot, has a pty, cannot read the
  service `.env` or list other people's folders. Sweep of orphaned shells: `interactive_shell_sweep_test.go`.

### 2026-10-03 — Re-running the slot setup for Excellence took Confida's slot table away again

- **Found.** `provision-slots.sh init` for the default product resets `/etc/agentworks` to 0750 root:agents. Confida's
  service reads its table through that folder (`o+x` on it, added after the 2026-10-01 incident), so after Excellence's
  init (done during the 2026-10-02 slot-program refresh) Confida's shells would have failed with "slot table
  unavailable". Found while testing the terminal on Confida; `chmod 0751 /etc/agentworks` fixed it by hand.
- **Done.** The script now sets `o+x` on `/etc/agentworks` unconditionally after creating the folder.

### 2026-10-03 — Incoming email has an Ask AI action; Excellence Google app restored

- **UI.** Incoming email's read-only card offers the shared Ask AI button in
  both Email and Triggers, including when deployment setup is missing. It sends
  Gmail setup guidance to the target's interactive chat; Crew/Code use their
  project chat callback and workflow targets use Builder. Consent and trigger
  configuration remain in Builder tools, with no direct pane mutations.
- **Excellence diagnosis and configuration.** The live agent's Google app was
  absent under its current HOME `/srv/agents/home`; Gmail inbound environment
  settings were also absent. Imported the matching downloaded web OAuth client
  (project `excellence-jobs-b45cc`, callback on the Excellence domain) using the
  running binary's `server set-mcp-app` command and the live service environment,
  as the service account. Verified the sealed file is service-owned and 0600.
  No code deployment or restart. Google account consent remains a human step.
- **Open rollout requirement.** Google sign-in app configuration does not enable
  Gmail inbound delivery: Pub/Sub, topic mapping and receiver authentication
  still need operator setup on Excellence before triggers can be enabled.

### 2026-10-03 — Old releases were never pruned: stale `.deploying` markers pinned them

- **Found.** Confida kept 15 releases (14 GB) and Excellence 5, because the pruner keeps any release with a
  `.deploying` marker. The rootless deploy removes the marker only on its very last line, so a deploy that exited after the
  release went live but before that line (the false "exit 7" health probe, fixed 2026-10-01) left it behind.
  14 of 15 Confida releases and 4 of 5 Excellence releases carried one. All were in fact finished and not in use.
- **Done.** Removed the stale markers by hand and pruned with `--keep` for the two newest previous releases (rollback
  copies): Confida 15 -> 3 (10 GB), Excellence 5 -> 3. Also cleared both Go build caches (7 GB + 5 GB, rebuilt by the next
  deploy) and set `/etc/logrotate.d/agentworks` (100 MB, 3 copies) for the product logs. The pruner now ignores a
  `.deploying` marker older than 6 hours (test: `test_a_stale_deploying_marker_does_not_pin_a_release`).
- **Context.** The shared Hetzner disk hit 100% on 2026-10-02 (issue #260); free space is now 71 GB.

### 2026-10-02 — Muse "MCP stdio connection is closed": a slow tool call killed the bridge for good

- **Found (Mayur, Code on excellence; same pattern in a second session).** A shell command ran 5 minutes; Muse recorded
  `timed_out` at exactly 5:00 (its own tool-call limit) and the very next call failed with "api-bridge: MCP stdio
  connection is closed". The Muse process had no `mcpbridge` child any more and never reconnects, so every file write
  and shell call failed for the rest of that session. Checked across all Muse sessions: the two sessions that ever
  saw "connection is closed" both had a `timed_out` call ~6 s before the first failure. No OOM, no deploy, no platform
  kill (nothing in the platform kills `mcpbridge`).
- **Why it was a race.** The bridge's own HTTP limit is also 5 minutes (`DefaultBridgeHTTPTimeout`), and Muse has no
  documented/configurable MCP timeout (Claude and Codex get a 90-minute one through their config), so Muse's timer won.
- **Done.** `mcpbridge` honours `MCP_BRIDGE_MAX_CALL_SECONDS` (mcpagent e9af395) and the Muse adapter sets it to 270 s on
  its bridge entry (provider), so a slow call returns an ordinary TIMEOUT tool error (with the advice to run long jobs in
  the background) before Muse's limit fires. Takes effect for Muse sessions started after the deploy.
- **Still open.** A Muse session whose bridge has died is not detected and restarted automatically: it stays broken until
  its tmux session is closed (done by hand for Mayur on 2026-10-02). The platform could check for the bridge child
  before delivering a message and relaunch Muse (resume) when it is missing.

### 2026-10-01 — Open review findings (reference skill, image bridge, gateway test)

- **Open.** Workflow-chat queries with tagged references fail entirely when the
  `work-workflow-files` reference skill cannot be loaded or attached
  (`agent_go/cmd/server/server.go`, no-profile branch). The previous inline
  guidance had no external dependency, so a skill-registry or network hiccup
  could not break queries. Suggested: fall back to a minimal inline pointer
  instead of failing the query. Found reviewing 97fa26caa; not implemented.
- **Open.** The MCP image bridge caps each image at 20 MiB but not the image
  count (`mcpagent` `cmd/mcpbridge`, `executor`). A hostile or buggy tool can
  exhaust bridge memory through the uncapped HTTP body read and fill disk via
  the persist loop. Suggested: accept roughly the first 10 images and note the
  truncation. Found reviewing 09ea79c; not implemented.
- **Open.** The AWS gateway login-bypass test never executes:
  `deploy/aws-ec2/server` has no `go.mod`, so neither `gmail_gateway_test.go`
  nor the older `auth-gateway_test.go` runs anywhere. The bypass itself
  (exact path, POST-only) was reviewed and is correct. Suggested: wire the
  directory into a module/CI or cover the predicate from `agent_go`. Found
  reviewing 58b5ea934; not implemented.
- **Open (minor).** `PushVerifier` holds its mutex across the 10s signing-key
  fetch (`agent_go/pkg/gmailinbound/oidc.go`), stalling concurrent
  verifications during rotation. Suggested: fetch outside the lock with a
  double-checked refresh. Found reviewing 58b5ea934; not implemented.
- **Open (minor).** Relay dispatch failures, including release-integrity
  errors, still return 400 with internal text (`agent_go/cmd/server/relay_runs_api.go`).
  Concurrency/store failures already map to 503; integrity failures should be
  5xx too. Publishing while the Builder edits can also mix file versions (the
  per-workspace publish mutex serializes only concurrent publishes). Found
  re-reviewing the Relay MVP on main; not implemented. The wider N3 gap (runs
  writing into their release folder) stays as recorded in the 2026-10-01 Relay
  release review entry.

### 2026-10-01 — Builder connects Gmail and configures narrowing inbox filters

- **Decided.** Keep history-based incremental delivery. A newest-20 mailbox scan
  could lose an eligible trigger behind unrelated mail. Bounded continuation,
  explicit recovery-age limits/skip warnings and thread-context fetching remain
  a separate future change; they are not claimed implemented.
- **Done.** `manage_gmail_trigger(action=connect)` prepares a Google consent link
  through existing account handlers and configured OAuth clients. It does not
  enable a trigger. Code private-account ownership and shared-account admin
  permissions remain. Platform-app links use the registered shared callback.
  Builder guidance discovers IDs, requests human consent, verifies it, selects
  exact saved workflow routes, and returns the address/readiness/filter summary.
- **Filters.** Optional subject/body substrings, attachment presence and new
  threads only narrow authenticated owner mail. All keywords/conditions use AND.
  Replacement/clear semantics are explicit; omitted filters survive updates and
  disable. Builder can also update a paused workflow binding without enabling
  it; changed bindings are validated, while disabling stale bindings still works.
  Filtered mail is durable, visible with a reason, not replayed after
  filter changes, and follows content retention. Thread admission is serialized.
  Queued work rechecks current filters; running work/final responses continue.
- **UI and rollout.** Panes display filters read-only. Owner/operator docs are
  corrected for workflow reply runs and actual sync behavior. No deployment,
  cloud provisioning or live RTS certification is performed here.

### 2026-10-01 — Live browser stuck on "Browser restarted — reconnecting…" (RTS, Code project)

- **Found.** The browser of a Code project is started by the coding CLI inside its sandbox. The sandbox's private
  `/tmp` is the workspace's shared tmp folder, so the browser's socket folder and `.stream` file land in
  `<docs>/tmp/.agent-browser/o/<owner>/` on the host. The platform looked only in the host `/tmp`, found no stream, the
  live view's socket closed and the page reconnected forever. The browser itself ran fine (port listening, daemon up).
- **Done.** `browserconfig.SandboxSocketDir/SandboxSocketDirs` and their use in the live-stream lookup and the
  session cleanup / pid lookup (`WORKSPACE_DOCS_PATH`/`DOCS_DIR`). Test: `browserconfig/sandbox_socket_test.go`.
- **Not verified in a browser** until deployed to RTS; the `.stream` file and listening port were seen on the host.

### 2026-10-01 — Builder configures Gmail triggers; panes show read-only state

- **Decided.** Incoming Gmail setup belongs to Builder tools (`get_gmail_trigger`,
  `manage_gmail_trigger`), not a manual UI form. Email and Triggers show the same
  persisted address, target binding, readiness and activity, with only copy/refresh.
  Public management POST is removed; the shared Google-authenticated ingress remains.
- **Workflow binding.** A Gmail trigger is a `kind=gmail` webhook schedule without
  a public per-trigger endpoint or secret. Builder discovers saved routes/groups
  and stores the exact target. Each incoming message executes that binding through
  the existing durable trigger pipeline and isolated run folders, including email
  replies. Crew/Code retain continuing email chats. Legacy unbound workflow email
  chats remain until explicitly configured with Builder. One address per owner/target
  is retained, and disabling preserves it. Deleted/invalid bindings fail closed.
- **Guidance and authority.** Shared Crew/Code skill and workflow `gmail-inbound`
  reference teach account/read-consent setup, route selection, operator Pub/Sub
  prerequisites and verification. Route changes require an interactive owner;
  email, bot, scheduled and external-token callers cannot configure their routing.
- **Open.** Live RTS/Google testing still requires operator provisioning and deployment;
  none is performed here. This retains the private single-server SQLite queue and
  owner-only sender policy. Workflow trigger replies start new runs rather than
  continuing a conversational assistant, because the owner chose deterministic routing.

### 2026-10-01 — Deploys reported "exit 7" although the release was fine

- **Cause.** The deploy's last step probed the agent's local port once, right after the restart (after a drain the
  agent needs a few seconds to listen): curl exit 7 = connection refused, deploy "failed" with the new release
  already live and healthy. Seen on most deploys.
- **Done.** `rootless-linux/build-and-activate.sh` waits up to 2 minutes for the agent and workspace health
  endpoints before the checks. A real failure still fails the deploy, after the wait.

### 2026-10-01 — Opening a Relay from activity or the global tab opener landed on Goals

- **Reported** (Confida, two users): opening Relays opens Goals. The switcher and the Relay list are correct; two
  navigation paths forced the Goals surface for every workflow tab (`openGlobalTab`, and the activity-session
  fallback). Not reproduced in a browser: the fix is by reading the code, so confirm on Confida after the deploy.
- **Done.** `workflowSurfaceForPreset` (a Relay maps to Relays, else Goals) is used by both. Test:
  `workflowSurfaceForPreset.test.ts`. Still forcing Goals by design: the Quick Switcher and Schedules open
  the workflow afterwards, which sets the right surface.

### 2026-10-01 — Gmail incoming email uses a shared, opt-in receiver across deployments

- **Decided.** Link each owned Crew, Workflow, or Code to a connected Gmail mailbox and assign a stable
  plus-tagged address. A Gmail conversation creates an isolated app chat; replies continue it. V1 accepts
  only the owner's directory email with Gmail sender authentication (or the mailbox's own Sent message).
  Shared readers cannot configure routes; Code retains its private Google account scope. Optional final
  responses use notification sending, with Auto-Submitted and the route address in Reply-To.
- **Implementation.** `pkg/gmailinbound` persists cursors, notifications and deduplicated deliveries in private
  server SQLite state. Four sync workers and two delivery workers serve all mailboxes through existing gog
  credentials. Watch renewal is daily, reconciliation is every five minutes, and expired cursors recover with
  an overlapping scan. A restart marks in-flight work uncertain instead of repeating external side effects.
  Agent execution uses the existing internal conversation dispatcher, project bindings and access checks.
- **Rollout.** Configuration maps named OAuth clients to topics, plus an exact HTTPS audience and verified
  Pub/Sub service-account email. One topic per OAuth project and one subscription per deployment lets a
  mailbox serve multiple deployments. RTS is first (AWS `./deploy.sh rts`); Hetzner uses the same gateway
  code. No cloud provisioning, deployment, or service restart is included in this change. Operator guide:
  `docs/gmail-inbound.md`.
- **Open.** Live Google/RTS testing still needs the actual OAuth project ID, topic/IAM/subscription setup,
  reconnect/read consent and real incoming mail. External senders/delegated sender policies, arbitrary
  vanity addresses, distributed worker leases, manual delivery replay UI and guaranteed exactly-once tool
  side effects are outside V1. Queue limits and 30-day body retention are documented in the operator guide.

### 2026-10-01 — Docker for slotted users: a private rootless Docker per slot (all deployments)

- **Found.** A command run as a slot inherited the platform account's `DOCKER_HOST` (its rootless socket, in a folder
  only that account can open) and had no Docker of its own: "permission denied while trying to connect to the docker
  API". Sessions whose coding CLI runs as the platform account still had Docker, one shared daemon in which every
  user could see and stop everyone's containers.
- **Decided.** Docker is part of the shared slot setup, not a host's one-off: `provision-slots.sh docker [slotNN ...]`
  gives each slot (all assigned ones by default) its own rootless daemon (user unit, linger, subuid/subgid, a socket
  in the slot's own `/run/user/<uid>`), sets `slot_docker` in the slotctl config, and `assign` enables it for each new
  slot automatically once the host uses it (`init` keeps the flag). The platform then replaces `DOCKER_HOST` with
  the slot's own socket for commands run as a slot (`slots.WithSlotDocker`, builder; `slotfs.WithSlotDocker`, provider,
  for CLIs run as a slot); a host without `slot_docker` is unchanged. Users cannot see or stop each other's containers.
- **Costs to know per host.** Each slot keeps its own images and volumes under its home (disk) and a running daemon
  is roughly 100-150 MB (memory): a small host (RTS, 4 GB) should enable it only for the slots that need it
  (`docker slot03`). Rootless Docker needs unprivileged user namespaces (the script refuses on a host whose AppArmor
  restricts them unless `FORCE_DOCKER=1`) and `docker-ce-rootless-extras`/`uidmap`. Published ports are host-wide:
  two users publishing the same host port still collide (the port-limit plan is separate).
- **Not applied yet.** The shared Hetzner host (excellence, Confida) is at 97% disk (15 GB free) with 59 GB of unused
  images in the root Docker daemon (other developers' accounts); applying per-slot Docker waits for that headroom.
  Tests: `slots/docker_env_linux_test.go`, `internal/slotfs` (provider).

### 2026-10-01 — Preserve MCP images through the coding CLI bridge

- RTS/Manish's `sde private` Code chat used Claude and Jam's video tools;
  the agent reported text describing frames but could not see their pixels.
  The executor's text-only conversion discarded MCP image blocks and the
  stdio bridge reconstructed only a text result. Native file tools cannot
  open an image which was never delivered or saved.
- Keep the legacy HTTP `result` text and add optional `images` carrying the
  original MIME type/base64 payload. The bridge returns valid images as native
  MCP image blocks and saves exact bytes as 0600 files under its parent-selected
  session `tool_output_folder`. Return paths for native image/file tools or
  `read_image`; do not dump base64 into the text transcript. This uses the
  existing sandbox/output-directory grant and adds no folder authority.
- Bound inline images to 512 KiB total to preserve the stdio response budget;
  larger images use the exact saved file. Local files are capped at 20 MiB per
  image. Invalid payloads and save failures are reported explicitly; valid
  inline images survive a local-file save failure. Source: mcpagent
  `executor`, `cmd/mcpbridge`. A real executor → HTTP → stdio MCP test covers
  Jam-shaped mixed and image-only results, exact bytes and private file modes.
- The fix must be deployed and the CLI's bridge restarted before a retained
  session gets it. No RTS deployment or live conversation restart was done
  during this investigation.

### 2026-10-01 — Tagged project procedures belong in the reference skill

- Keep the dynamic prompt to exact typed tags/folder paths, effective runtime
  authority and a pointer to the attached reference skill. Detailed path
  resolution, inspection, schedules, Dashboard links, durable attachments and
  function-call procedures live in one shared `work-workflow-files` template.
  Product rendering gives Code its `code-workflow-files` name; workflow chats
  receive the canonical guide when they have references. Feature constraints
  continue to state the always-on authorization boundaries.
- Remove claims that any owner's Crew files are shared and advice to read a
  referenced Crew's blocked `builder/`. A tag and a callable function do not
  override privacy. Reference files follow actual grants; referenced Crew
  `db/` is read-only and `builder/` is blocked. Schedule definitions are in
  `workflow.json`; a caller requests live schedule status or the target's
  Dashboard link through that target's `ask`, rather than using tools bound
  to the caller's own project.
- Code: `instructions.go`, `server.go`, feature metadata and the shared skill.
  Tests keep typed labels/exact roots, verify product-specific skill pointers,
  and preserve the feature tool surfaces and invocation constraints.

### 2026-10-01 — Slotted commands were not stopped by a timeout, cancel or kill

- **Found (tested on Confida, then on a server with the real code path).** The platform stops a shell command by
  signalling its process group (a hard kill). A slotted command's processes belong to another Linux account, so the
  signal stopped only `sudo`: after the kill the command (and anything it started) was still running (2 `sleep`
  processes before the kill, 2 after). Timeouts, cancelled chats and "stop process" left slotted commands running.
- **Decided and done.** (1) `slotctl exec` starts the program in its own process group and, on SIGTERM/SIGINT/SIGHUP,
  signals the whole group and kills it after `slots.StopGrace` (2 s). (2) The wrapped command's cancel is a graceful
  SIGTERM that `sudo` relays (a hard kill of `sudo` cannot be relayed), with a last-resort delay. (3)
  `killShellCommandProcessGroup` does the same for a wrapped command (`slots.IsWrapped`). The "stop process" path
  (`terminateProcessGroup`) already sends SIGTERM first and now works through (1). Normal exit does not kill
  background processes a command started (as before). `slotctl` is root-installed from the release by
  `provision-slots.sh init`, so each host needs that re-run after the deploy that carries this.
- **Tests.** `slots/exec_linux_test.go: TestRunExecStopSignalReachesTheWholeProcessGroup` (run on a Linux host).

### 2026-10-01 — Citymall AI gateway works through Pi's existing Chat Completions transport

- Live gateway calls passed chat, streaming, inline vision and image generation;
  isolated installed Pi CLI calls returned a Hindi greeting and read a test file
  through the native tool with the explicit off-to-none mapping. Stage a non-secret
  Pi custom-provider template (`products/citymall/pi-models.json`), with the key
  supplied as `CITYMALL_API_KEY` from Citymall's own protected environment.
- **Open:** function tools require `reasoning_effort=none`; the default fails.
  Map Pi's off thinking level explicitly to `none` in the template; without that
  mapping the CLI omits the field and native tool calls still fail.
  The existing Azure adapter chooses Responses for every GPT-5 name and this
  gateway's Responses request failed with HTTP 500. Private Pi session model
  configuration/scoped credentials and a separate image-generation adapter still
  need application integration and authenticated qualification before launch.
  Model-list routes return 404/500; only the two supplied, successfully called
  models are confirmed. Full key-accessible inventory needs the gateway's enabled
  deployment list/API specification, not guessed model names.

### 2026-10-01 — Citymall dedicated host: prepare an isolated service account first

- The supplied EC2 host (`52.66.201.227`, Ubuntu 26.04, 2 CPUs/4 GB RAM) is fresh.
  Prepare `/srv/citymall` under an unprivileged `citymall` account with a persistent
  user manager, workspace directories, native/Python/browser prerequisites and
  Confida's checksum-pinned Node runtime. Generate new persistent secrets; never
  borrow another customer's logins, credentials or data. Code:
  `deploy/rootless-linux/setup-citymall-host.sh`; runbook: `citymall.md` beside it.
- **Open:** domain, enabled products, sign-in configuration and initial admin
  need to be chosen before adding a deploy target and activating the application.
  Base preparation does not start a public site or application services. Also
  size the first build for the 4 GB host and verify its namespace sandbox before
  activation; the shared deployment defaults assume a larger machine.

### 2026-10-01 — Slotted shell commands lost their per-call environment (401 from the tools gateway)

- **Incident (Confida, Vaibhav's workflow session).** A shell command run as a slot only got the base environment
  plus `MCP_API_URL`; `MCP_API_TOKEN`/`MCP_AUTH` (so the HTTP tools gateway answered 401), every `SECRET_*`, `VAR_*`,
  `STEP_*`, `DB_PATH`, `WORKFLOW_*` and `PYTHONPATH` were missing. Cause: the handler appended those values to the
  command's environment *after* the isolator had wrapped it for the slot, and the wrap writes the environment into
  the request at that moment. Fix: `security.Isolator.ExtraEnv` carries the filtered per-call values and they are merged
  (`MergeExtraEnv`) before `WrapCommand`; non-slot commands are unchanged. Tests: `security/extra_env_test.go`,
  `security/isolator_slot_env_linux_test.go`. Affects every slotted workflow shell on any host (excellence, RTS,
  Confida) until deployed.
- **By design.** `planning/` is mounted read-only in a workflow shell so plan changes go through the authenticated tools.
- **Browser (RTS "Browser restarted - reconnecting" loop; Confida "Permission denied ... google-chrome").** The
  `agent-browser` CLI starts its own daemon and Chrome from inside the command and the platform manages them (profile
  folders `browser-profile*` are service-owned 0700, the live view, restarts, killing). As a slot the CLI could not
  write the profile folders (so `daemonPID=0`, no Chrome, live view 502) and the platform could not stop a browser
  owned by another account. Decided: a command that is exactly one `agent-browser` invocation runs as the service
  account, with the folder guard, as before slots (`handlers/browser_command.go`, `isStandaloneBrowserCommand`: no
  unquoted shell operator, no command substitution, first word exactly `agent-browser`); everything else still runs
  as the slot, so appending `agent-browser` to another command does not leave the slot. Residual: a browser command
  has the service account's access inside the Landlock folder guard (the same as before slots), and the browser
  daemon reads `file://` as the service. Making browsers run as the slot would need slot-writable profile folders and
  a platform that can manage processes it does not own; not done.

### 2026-10-01 — RTS resized to t3.medium (2 vCPU / 4 GB) for performance testing

- **Decided and done.** The RTS instance went from `t3.large` to `t3.medium` through the stack (change set
  `rts-resize-t3-medium`, in place, same disks and Elastic IP; a few minutes of downtime). `t3.medium` was added to the
  template's allowed sizes. The stack said `t3.xlarge` while the instance had been resized by hand to `t3.large`
  earlier, so the stack parameter now matches reality again. The template's first-boot script differs from the deployed
  one (rootless Docker was added later); it only runs on a new instance, so nothing re-ran.
- **Why not c5.large.** `t3` is burstable: once CPU credits run out it is throttled, so sustained performance numbers
  drift. A fixed-performance size (`c5.large`, also 2 vCPU / 4 GB) gives steadier results if the tests run long; the
  template does not list it yet.
- **Deploys.** The on-box build cap was 6 GB; it is now `${RTS_BUILD_MEMORY_MAX:-3G}` (`deploy.sh`), with swap
  (4 GB) absorbing the rest, so deploys are slower rather than killed. Set `RTS_BUILD_MEMORY_MAX=6G` after a larger resize.

### 2026-10-01 — Slots: shared folders need a shared group (workflow shell was failing for slotted users)

- **Incident.** With slots on, every shell command a workflow or Relay ran as the user's slot failed with
  `slotctl: could not start: fork/exec ...: permission denied` (Go reports a failed chdir this way): the shared
  folders (`Workflow/`, `Downloads/`, `skills/`, `subagents/`, `tmp/`) belong to the service account and the folders
  inside them are owner-only, so no slot could enter its own workflow's folder. Found on Confida (Vaibhav, 13:12);
  RTS (scheduled workflows run as the admin, who has a slot) and excellence (Relays/Crew) had the same gap. This is
  the "shared folders are not slot-writable yet" item flagged when opt-in mode was introduced; assigning every
  Confida user a slot made it bite.
- **Decided and done.** One group per product (`<prefix>shared`: `cfshared` on Confida, `slotshared` on RTS and
  excellence) holds the service account and every slot; the shared folders get that group, group read/write and
  setgid, so files either side creates stay reachable by both. Private trees stay closed to other slots (checked: one
  slot cannot list another's tree). Apps still decide who may open which workflow; this only restores what the
  shell could do as the service account. `provision-slots.sh init` runs it and `provision-slots.sh shared` re-runs it
  (`SHARED_DIRS` overrides the folder list); the service's user manager must be restarted once for the new group.
- **Open.** Anything else a slot must reach but does not own (new shared folders outside that list) needs the same
  treatment. CLIs as the user's own account (not enabled outside the excellence canary) have the equivalent question
  for their runtime files.

### 2026-10-01 — Excellence offers Crew and Relays (the product switcher list lives in runtime-config.js)

- **Correction.** Which products a deployment's switcher lists is the frontend `runtime-config.js`
  (`enabledProductSurfaces`), narrowed per account by `allowed_products` (administrators: all enabled; others:
  their `users.json` products plus `AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL`). Excellence's file said
  `["code"]`, so Crew and Relays never appeared even for administrators. Earlier notes that `AGENT_PRODUCTS`
  controls this were wrong: that setting is not passed to these services.
- **Decided.** `enabledProductSurfaces: ["code", "work", "relays"]` on excellence (default stays `code`). Visible to
  Manish and Aayush (administrators) and Vaibhav (`work`, `relays` in his list); every other account stays on
  Code because its own list does not include them.

### 2026-10-01 — Relays (profile id `relays`) opened on Confida for everyone to test; excellence per person

- **Decided.** Confida: `AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL=work,code,relays`, so every account can open Relays
  for testing (the feature landed on `main` today; it was not in any live build before this deploy). Excellence:
  Relays only for Aayush (administrator, every product) and Vaibhav (`relays` added to his `users.json` products);
  excellence's available-to-all list stays `code`. RTS is unchanged. To close it again on Confida, remove `relays` from
  that list and redeploy.

### 2026-10-01 — read_image over the CLI bridge runs on the session's own account

- **Found (Confida, Vaibhav).** `read_image` called by a coding CLI arrives through the tool bridge as a plain HTTP
  request, with none of the turn's provider accounts in its context. The analysis fell back to "the agent's own
  model" (claude-code/claude-sonnet-5-5) but, with no account in hand, started Claude under the **server's**
  account: Confida has no server Claude login, so Claude stopped on "Select login method" and timed out
  ("LLM image analysis failed ... [auth]"), even for a user with their own Claude connection.
- **Decided.** The turn's account keys (`mergedAPIKeys`) are attached to bridge-originated `read_image` calls
  (`SetReadImageLLMConfig(..., keys)` / `injectSelectedLLMConfig`), so the analysis uses the same CLI, connection
  and scope as the session, as the in-process path already did. Keys already in the context are never replaced.
  Test: `virtual-tools/read_image_account_test.go`.
- **Kept.** Custom tools stay: the platform tools (shell as the user's slot, browser, files under the folder guard,
  secrets, workflow and MCP tools) are what the native CLI tools do not provide; only this tool's separate
  model call was wrong. Other bridge-called tools that start their own model (e.g. `generate_text_llm`) were not
  audited for the same gap.

### 2026-10-01 — Confida slot table unreachable (incident), and image analysis login

- **Incident.** After Confida's slots went live (12:00), its service could not read
  `/etc/agentworks/confida/slots.json`: `/etc/agentworks` was `root:agents 0750` (excellence's group), so Confida's
  service could not traverse it. In opt-in mode a table that cannot be read is an error, so every shell and
  browser call failed for every Confida user ("No account slot for this user ... permission denied"), about
  13 calls until it was fixed (`chmod 0751 /etc/agentworks`, search-only; the tables and per-product folders stay
  closed to the other product, verified both ways). `provision-slots.sh` now sets that for any non-default
  prefix. My earlier probe ran as the Confida account but never read the table, so it missed this; the probe
  must read the slot table through the service's own path.
- **Not from slots.** Image analysis (`read_image`) on Confida uses the workflow's chosen model
  (claude-code/claude-sonnet-5-5); Confida has no Claude login (no token, no credentials file), so Claude shows
  its login screen and the call times out. Fix by connecting a Claude account (Providers) or choosing a model
  with a login for image analysis.

### 2026-10-01 — Excellence: Docker for users runs rootless; `agents` left the root `docker` group

- **Found.** `agents` (the platform account, which also runs every non-canary user's coding CLI) was in the host's
  `docker` group, i.e. root-equivalent: any user's Code session could `docker run -v /:/host` and own the box.
  Users really did use it (invoicing, hrms, injuryconnect stacks ran on the root daemon).
- **Decided and done.** `agents` is out of the `docker` group and has its own rootless Docker daemon (user unit
  `docker.service`, data under `/srv/agents/.local/share/docker`, socket `/run/user/990/docker.sock`);
  `DOCKER_HOST` points the services at it (`product.env`, `/srv/agents/.env`). Containers of excellence users on
  the root daemon (7: invoicing x3, hrms_db, injuryconnect x3) were stopped; their named volumes were backed
  up to `/root/docker-backups-20261001/` on the host and kept, so users re-create their stacks (`docker compose
  up`) in the new daemon and can ask for a restore. The other 33 containers on the root daemon belong to other
  developers' accounts (newjoinee, node, dev83, pythonai, react) and were not touched.
- **Not the shared installer.** `deploy/common/install-rootless-docker.sh` runs `apt-get install docker-ce ...`,
  which can restart the root daemon (and everyone's containers); it was not used. Its final check also refuses a
  service user in the `docker` group, which is why the group had to go first.
- **Open.** `newjoinee` is still in the `docker` group (root-equivalent). When coding CLIs move to the user's own
  slot account, Docker needs a per-user rootless daemon (or is unavailable), since a slot cannot reach the
  platform's socket. Several other accounts publish databases on all interfaces (mongo, postgres, redis); not
  checked against the firewall.

### 2026-10-01 — Relay release review: visible readers execute as the owner

- Anyone with live Relay visibility can execute its published API versions and
  poll their own runs. Tokens still require `runs:execute` and the Relay scope;
  listing versions requires `workflows:read`. Publish, edit, and schedule
  configuration remain Owner/Write. Execution uses the owner's configured
  accounts, workflow secrets and quota. This follows the PR 231 review's
  maintainer decision. Live function/caller revocation applies to polling and
  idempotent replay as well as dispatch.
- Reuse the existing workflow read guards, provider-account admission,
  message-sequence executor, database tools, scheduler run store and cost ledger.
  Release identity maps to its live draft for grants/accounts; database and cost
  artifacts stay in the full version workspace. Draft cost totals include all
  versions without recording duplicate global charges.
- Capacity waits now persist as nonterminal scheduler rows, survive restart,
  and are claimed once for the original run. Restore its input, group, route,
  folder and step checkpoint; check live caller access and release integrity
  before resuming. Normal schedules use the same durable wait discovery.
- Missing explicit versions return 404. Invalid release metadata remains listed
  with an error. The schedule UI warns that timed schedules run the draft.
- Accepted: Write editors may publish; frozen variables can retain stale secret
  copies, while execution resolves live workflow secrets. Open: process-crash
  recovery is deferred; execution snapshots are not a general read-only
  filesystem. The Relay Builder warns against modifying executable snapshot
  files, which would make later dispatch fail integrity verification. Binary
  snapshots and pinned timed schedules remain outside this MVP.
- Verified with automated backend and frontend regressions in the isolated
  review worktree. The broader workflow suite still fails the existing AGY
  alpha-gate test; reproduced on clean `origin/main` (`c48042b56`). No live preview, production deployment or interruption of
  the user's running local app is claimed by these checks.

### 2026-10-01 — Scoped Files views show one workspace root above its contents
- Rebuild a single scoped tree from the document API's mixture of nested
  children and flat siblings before shortening display paths. Merge folders
  and files once, including contents-only responses; do not render the root
  beside its children or show entries outside the requested scope.
- Compare case-preserving paths after removing boundary slashes and converting
  only the signed-in user's own physical prefix to its public API path. Keep
  full API paths for file actions and project-root/manifest deletion protection.
  Code and Crew use this shared scoped Files view. Code: `scopedWorkspaceTree`,
  `Workspace`. Tests cover root ordering, duplicate entries, path variants,
  nested contents, hidden folders, foreign paths and rendered bulk deletion.
- Ashutosh's screenshot shows `unknown2-0-8079bd2f` beside `code` and `db`.
  Local fixtures reproduce this layout with the previous exact-path fallback;
  the precise live response/path variant is not yet captured.

### 2026-10-01 — A closed main terminal does not imply an inactivity timeout
- The raw terminal's recovery banner now says the terminal is no longer
  running, without attributing every exit to inactivity. Excellence logs show
  both idle-backstop cleanup and unexpected pane exits; the terminal snapshot
  does not establish a cause for this banner.
- "Back to chat" switches the existing chat to its formatted view. It does
  not send a prompt or relaunch the CLI; the next new chat message uses the
  existing resume path. The project folder, saved conversation and final
  terminal output remain available. Code: `MainAgentTerminal`, `ChatArea`.
- User decision: pin the recovery notice and button below the final output,
  at the bottom of the terminal pane where people expect to type. The footer
  does not shrink into the output and is announced as a status notice.

### 2026-10-01 — Slots per product on a shared host (Confida: prefix cf, 15 accounts)

- **Decided.** Products that share a host each get their own slot accounts, launcher, config, table and sudo
  rule, so one product's service account is never in another's slot groups (a single set would let
  Confida's service read excellence users' folders). The prefix, config path, launcher and table path are
  settings (`AGENTWORKS_SLOT_PREFIX`, `_SLOTCTL_CONFIG`, `_SLOTCTL`, `_SLOTS_FILE`; `slot_prefix` in the
  slotctl config for programs that run without the service environment). The default (`slot`,
  `/usr/local/libexec/agentworks/`, `/etc/agentworks/`) is unchanged, so excellence needs no migration.
  `provision-slots.sh` takes `SLOT_PREFIX`; a non-default prefix puts everything under a per-product name
  (`/usr/local/libexec/agentworks/<product>/`, `/etc/agentworks/<product>/`,
  `/etc/sudoers.d/agentworks-slots-<product>`, its own sudoers alias).
- **Decided.** Confida: `SLOT_PREFIX=cf SLOT_COUNT=15`, opt-in mode (it runs Workflows and Crews). It has 12 users.
- **Decided.** In opt-in mode a host with no slot table yet is unchanged (nobody holds a slot) instead of
  refusing shell commands, so a product can deploy with the slot settings first and be provisioned after.
  A damaged or unreadable table is still an error; `on` mode still refuses without a table.

### 2026-10-01 — Crew offered on excellence (Code + Crew); Code keeps its own-login rule

- **Decided.** Crew (`work`) on excellence is given per person, not to everyone: administrators (every
  product) and anyone whose `users.json` `products` lists it. Vaibhav now has `code` and `work`; the other
  accounts stay on `code`. `AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL` stays `code`. (`AGENT_PRODUCTS` in
  `product.env` is not passed to this deployment's services, so it gates nothing; an earlier edit of it to
  `code,work` had no effect and was reverted.) Crew projects are private to their owner (project sharing is off).
- **Decided.** A single-product deployment refuses a Claude turn with no token by itself
  (`isSingleProductServerDeployment`); a multi-product or unset `AGENT_PRODUCTS` (excellence, Confida) never had
  that safety net. Code is private to
  each person and signs in with their own CLI login, so on a multi-user server a Code turn with no token
  is still refused (`claudeCodeTokenMissingForSingleProductDeployment`), instead of falling back to the
  platform account's own CLI login. Other products on the server keep the shared-server behavior.
- **Open.** Excellence keeps `COPY_PLAYBOOKS=false` and `RUN_WORKFLOW_BUILDER_MIGRATION=false`; check
  Crew templates and any playbook-backed feature in a first Crew test.


### 2026-10-01 — Header activity stays active through stale idle polls during a new turn
- The latest foreground user/start event keeps the chat header active until
  its completion, even when tab flags or a session-status poll still describe
  the previous idle/completed turn. Waiting-for-input remains distinct and
  completion still clears stale running flags. The shared hook applies to
  Code, Crew and workflows, including RTS's Cursor workflow chats.
- Select the latest lifecycle event by its timestamp, using sequence to break
  equal timestamps and arrival order when timing is unavailable. A replayed
  older completion appended after a new message cannot settle that new turn.
  Tests exercise the real store/hook/rendered header through repeated idle
  polls, delayed older completion and the new completion. Code:
  `foregroundTurnActivity`, `chatRuntimeActivity`, `useChatRuntimeActivity`.
- During the RTS investigation, Manish's `automationtesting` request at
  14:31:30 IST ended at 14:33:14 with Cursor `quota_exhausted` / "cursor usage
  limit reached". This is a provider failure separate from the loading race;
  changing the indicator does not resolve account quota.

### 2026-10-01 — Identical chat replies remain visible in separate user turns
- Excellence/Ashutosh's 14:24–14:26 IST Code messages reached Codex and each
  received an answer. Native transcripts, server observer events and browser
  receipt telemetry confirmed delivery. The shared transcript filter hid later
  identical answers because it compared across the whole conversation and the
  retained main execution ID spans multiple turns.
- Deduplicate answer carriers only within a user turn. Each later user message
  resets completion-card comparison; a generation answer cannot be hidden by
  a completion in another turn. Reconcile adjacent frontend/durable user echoes
  before comparing answers. The compact conversation projection also resets
  its last-answer comparison on a user message. Code, Crew and workflow chats
  use these shared projections. Regression tests cover identical native replies,
  a turn missing its completion, and rendered live/restored transcripts. Main
  agent lifecycle cards also supersede each other only within the user turn;
  child lifecycle cards retain their existing execution-based collapse.

### 2026-10-01 — Providers terminals for personal accounts run under Landlock

- **Found.** The Providers screen opens a terminal for a person's own provider account (sign-in, "inspect") by
  starting the coding CLI directly as the platform account with only the CLI's own flags (`--sandbox
  read-only`, `--disable-shell`, `--tools ""`) around it; Pi and Agy opened unrestricted. Any user with a
  personal connection could therefore read other users' folders and the server's secret files through the CLI.
- **Decided.** A personal account's terminal is confined with the same Landlock launcher and policy shape as the
  chat CLIs, limited to the account's private HOME, the folder it starts in and the CLI's install
  (`agent_go/cmd/server/provider_setup_confine.go`, `pkg/clilaunch` in the provider). It cannot be turned
  off. A multi-user host that cannot confine refuses the terminal; a single-user install is unchanged.
- **Not covered.** The server account's own terminals (admin only, service home) are unconfined, and a
  confined terminal still has network access and the account's own login. Sign-in flows that need a local
  callback port or another path outside the account's home may need a grant: test each provider's sign-in
  after deploying.

### 2026-10-01 — Files bulk deletion preserves its project container
- Excellence/Vaibhav's Files selection deleted the entire Code project folder;
  its cached project row remained, and the later project-delete request returned
  "project not found". A scoped pane's Select all now selects its displayed
  contents, preserving the root and `product.json`/`workflow.json`. Protected
  entries cannot be checked or passed to its delete handlers. This shared Files
  behavior also applies to other panes that hide their scoped root actions.
- The browser workspace proxy refuses deletion or clearing of Code roots and
  their parent containers, and deletion of Code identity manifests, including
  for admins. Normal Code content deletion and manifest reads/updates remain
  allowed. Delete Code stops work and uses the existing project lifecycle
  endpoint to clean up its durable chat and connections.
- A project-delete 404 lets the frontend finish clearing an already-missing
  owned project's cached row and tabs. Permission, active-work and server
  errors still surface, and shared-project deletion remains refused. Code:
  `workspaceSelection`, `Workspace`, `PlannerFileList`,
  `workspace_proxy_policy.go`, and `deleteWorkSession`.

### 2026-10-01 — Project sharing removed: projects are private to their owner

- **Decided.** A project (a Crew in the project directory, on RTS the Video Studio / Goals projects that every
  account could open) is private to its owner, as Code workspaces already were. A non-owner can no longer
  see it in the shared-project list or open it as a reader. One switch, `projectSharingEnabled`
  (`agent_go/cmd/server/project_sharing.go`, off by default, `AGENTWORKS_PROJECT_SHARING=on` restores it),
  is checked in `resolveCrewProjectBinding` and `handleListSharedProjects`; the reader code stays so it can
  come back. Why: with per-user Linux accounts (slots) a non-owner's commands run as their own account and
  cannot work in the owner's folder, and the platform's own mediated reads were the only thing that made
  shared projects work; private projects need no shared folders.
- **Effect on RTS.** All existing Video Studio, Work and Code projects live under the admin account, so only
  admin sees them after the next deploy; other accounts start with none. Workflow co-owners and public share
  links are separate features and are unchanged.
- **Tests.** The existing reader-flow tests run with sharing switched on (`project_sharing_testmain_test.go`);
  `project_sharing_test.go` covers the default. `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID` and
  `TestSalesCrewCatalogHasInstallableRoles` fail on a clean `origin/main` as well; not caused by this change.

### 2026-10-01 — Slots on the RTS host: one shared build step, opt-in mode, SSM provisioning

- **Decided.** Every deployment builds and installs slots through one shared script, `deploy/common/slots.sh`
  (build `slotctl` and `slottmux`; install the tmux front-end outside the releases), called from both
  `deploy/rootless-linux/build-and-activate.sh` and `deploy/aws-ec2/server/build-and-activate.sh`. The
  provisioning script moved to `deploy/common/provision-slots.sh` and takes `APP_DIR`, `DOCS` and
  `SERVICE_HOME`, so the same script serves `/srv/<product>` hosts and RTS. The two build scripts themselves
  are still separate (RTS has its own Docker, CloudFront and AppArmor steps); merging them is a larger
  change and is not done.
- **Decided.** `AGENTWORKS_SLOTS=optin` (new): a user who holds a slot runs shell commands as it; a user without
  one is unchanged (`on` still refuses them). RTS runs Video Studio and Workflow/Crew runs that write into
  shared folders a slot account cannot write yet (the per-slot state roots item below), so it rolls out per
  user. The RTS build writes `optin` only when `/etc/agentworks/slots.json` exists, i.e. after an administrator
  ran `deploy/aws-ec2/slots-admin.sh init`.
- **Decided.** RTS has no sudo and SSH is deploy-only, so root steps go through SSM Run Command
  (`deploy/aws-ec2/slots-admin.sh`, like `install-system-tools.sh`). `init` also installs `acl` (setfacl).
- **Open.** CLIs as the user's own account (`AGENTWORKS_SLOT_CLI*`) are not enabled on RTS; shared Workflow and
  Crew folders still need slot-writable state roots before shell-as-slot can cover those runs.

### 2026-10-01 — Foreground completion clears chat header loading despite stale status
- The latest foreground turn's completion event clears the header spinner even
  when tab flags or the active-session cache still report foreground work. The
  next user/start event resets that completion signal; actual background work
  continues to show activity. This applies through the shared hook to Code,
  Crew and workflow chats.
- Reuse ChatArea's existing completion types and child-execution scope rules in
  shared utilities, including its exclusion of restored intermediate narration.
  Regression tests exercise the real store, hook and rendered transcript with
  stale busy flags, completion, and the next message. Code:
  `foregroundTurnActivity`, `runtimeEventScope`, `useChatRuntimeActivity`.

### 2026-10-01 — Review fixes: no cross-user path resolution, server secrets out of agent environments

- **Decided.** `utils.IsValidFilePath` (behind `ResolveUserPath` and every other handler) refuses a symlink
  that carries a path from one user's tree into another's, or from a shared folder into a user's tree: a
  link planted in your own folder can no longer read someone else's. Checked first on excellence and RTS:
  no existing link does either. Regression tests in `workspace/utils/path_cross_user_test.go`.
- **Reversed the same day (RTS outage).** The first version also refused any `_users/<id>/` path whose id was
  not the requester's. That broke RTS: the agent could not load `_users/_system_global_secrets/secrets.json`
  at startup, never listened, and RTS was down for about 8 minutes until the release was rolled back. The
  workspace API has no authorization of its own by design; the app server authorizes the caller and stamps
  `X-User-ID`, and shared Code collaborators, Crew owners and administrators legitimately read another
  user's folder, so the name check must not live there. Lesson: a change to a shared resolver needs a
  startup check against a copy of the real data layout before a swap.
- **Second outage, same day (excellence startup).** The symlink rule compared a path's owner with the owner of
  its nearest *existing* parent, so a path that does not exist yet (`_users/_system_global_secrets/...`, or
  a new user's first file, whose nearest parent is `_users` itself) was refused and the agent could not
  start; excellence went down until its release was rolled back. Fixed: the comparison applies only once the
  resolved path has reached a user's own folder, and a link to `_users` itself stays refused. RTS was only
  unaffected because those folders already existed. Checked against the real service with a data layout
  that has no `_system_global_secrets` folder and a brand-new user's first write, not only unit tests.
- **Decided.** Shell commands (`security.buildNativeEnvironment`) and CLI launches
  (`llmtypes.ScopedCodingAgentEnvironmentPlan`, now also when no secret scope is declared) never inherit
  the host's server-owned secrets: `AUTH_SECRET`, `ACCESS_PASSWORD`, `AUTH_USERS`, `GLOBAL_SECRET_*`, the
  server and bridge tokens. Whether the secrets exposed earlier were actually rotated is a fact about each
  deployment, not this code: confirm it per server.
- **Deferred.** Refusing a CLI launch when Landlock is requested but unavailable (`cli_landlock.go`
  falls back to the older tool-restricted mode). The product is not installed on hosts without Landlock.
- **Open.** The generated-HTML iframes (`HtmlRenderer.tsx`, `HtmlWidgetFrame.tsx`) run with
  `allow-scripts allow-same-origin`, so a malicious report could read the viewer's session. Removing
  `allow-same-origin` needs a message-passing replacement for the report frame's direct DOM access.

### 2026-10-01 — Chat activity belongs beside the current agent turn
- Move the composer loading indicator to the shared conversation's agent header,
  covering Code, Crew and workflow chats. A pending turn gets a header before its
  first text/tool event; streamed text and tool work then share that header.
  Earlier turns keep their recorded duration and never animate for a later turn.
- Reuse the activity monitor's session classification with immediate tab-local
  start/completion signals. Running/background work spins, input waiting shows
  amber, and settled or idle retained CLIs show no activity indicator. Status is
  scoped to the displayed chat and the lifecycle refresh also works without a
  composer in read-only run views. Provider usage and stop/steer controls remain
  in the composer.
- Keep the animation isolated from token updates and honor reduced motion.
  Code: `useChatRuntimeActivity`, `chatRuntimeActivity`, and
  `TerminalEventTranscript`. Tests cover pending/streaming/tool-only turns,
  completion/error/cancel, background work, waiting and chat switches.

### 2026-10-01 — Project prompts use resolved host paths and honor listed folder grants
- The project workspace map now resolves manifest-relative paths against the
  configured docs root before describing them as absolute. Existing absolute
  paths stay unchanged. This applies to Code and Crew through their shared
  workspace section.
- Code's shared-server rules explicitly allow the signed-in user's authorized
  history reads and listed attached-folder access. Attached writes still require
  read_write access and guarded tools; unlisted server folders and other users'
  projects remain forbidden. This aligns the wording with existing authorization,
  without adding a filesystem grant. Code: `prompt_sections.go`.

### 2026-10-01 — Slot launches: git ownership check off, repository hooks off; paste and key hygiene

- **Decided.** Every command and CLI run as a user's own Linux account (slot) gets `safe.directory=*`
  (the user's folders belong to the platform account with the slot's group, so git would refuse them as
  "dubious ownership", and Muse could not resolve its project root). That removes git's guard against
  a repository whose own config runs code, so the same environment also sets `core.hooksPath=/dev/null`
  and `core.fsmonitor=false`: a sharer cannot plant a hook that runs as another user's slot. Other
  repository-config code paths (for example a configured pager) remain; an agent reading an attacker's
  files is exposed to that already. Code: `workspace/slots.GitSlotEnv` (shell tool) and
  `internal/clisandbox/landlock.go` (CLIs, provider).
- **Decided.** `slottmux` keeps paste content only in a 0600 file under the slot registry and loads it
  into a tmux server when the paste names the session, so a slot's paste is never copied into the shared
  default server. Files are removed by the deleting paste and swept after an hour.
- **Decided.** The Codex API-key login the platform saves (`auth.json`, apikey mode) is removed when no
  key is configured any more; a browser login is left alone.
- **Decided.** `server add-user` re-reads the directory after saving and redoes the add when another
  write replaced the file (no lock is shared with the running server, which also writes it).
- **Fixed (same day).** The tmux front-end routed any session in a user's folder to that user's slot, but
  only users with CLI-as-slot (`AGENTWORKS_SLOT_CLI_USERS`) have launch scripts the slot can read, so
  Muse (and Codex) died at start for everyone else ("cannot open mlp-coding-agent-launch-*.sh:
  Permission denied"). It now routes to a slot only when the command's script is in that slot's run
  folder; otherwise the session stays on the platform's tmux. A Muse session that is not a slot's
  keeps its launch error output under `<run root>/.sessions/launch-logs/`.
- **Fixed (same day).** The front-end's session records ("session X lives in slot Y") could go stale: a
  session started on the platform's tmux after a misrouted one left the old record, so `has-session`
  went to the empty slot server and the platform then failed with "duplicate session". A record is
  now dropped when its slot has no running tmux server, and a non-slot new-session clears any record
  of that name.
- **Open.** Muse still prints "local session messaging unavailable: registry root: Permission denied" in
  slot sessions (it tries to `chmod 0700` platform-owned folders). Harmless, not traced.


### 2026-10-01 — Code is always private; owner-authorized function calls do not share files
- User decision: remove human Code sharing. Normal files, links, chats, Git, bots,
  credentials and runtime access require the Code owner. Viewer/editor/co-owner
  grants in legacy `config/code-shares.json` are ignored; that file is retained
  and protected, never rewritten or deleted as a migration. Share controls and
  client APIs are removed; old GET/PUT share URLs return 410, and the Code shared
  directory returns an empty list. Code's audited read-only admin/reviewer
  inspection remains separate from normal access and cannot run or edit Code.
- A Code can call another Code it owns. The actual Code owner may also call
  explicitly declared Code functions from a Crew they own or a workflow whose
  ownership explicitly includes them, using `#code:<id>` with the existing
  function tools. Shared Crew readers and workflow readers/editors cannot inherit
  the resource owner's private Code access. No implicit Code `ask`, public Code
  function catalog, file attachment or filesystem grant is introduced. Declare
  and remove Code functions from the owner's Code chat, not from a Crew/workflow.
- Hidden internal bindings record the source path and stamp. Discovery, connection,
  dispatch, result/pending-input access and queued execution re-read source
  ownership; target execution and history stay under the Code owner's identity.
  Old Code-to-Code bindings still resolve their source ID only in that owner's
  tree. Existing owner-created authenticated webhooks and schedules remain intact.
- This fits per-user Linux accounts: there is no cross-user Code folder or Code
  credentials to mount. It does not complete the separate Crew/workflow sharing
  integration for slots. Main implementation: `code_shares.go`,
  `code_peer_functions.go`, `product_webhooks.go`, `crew_functions.go` and the
  shared Work/Code UI. Regression tests cover stale grants, typed Crew/workflow
  calls, foreign sources, shared actors and ownership loss before a queued call.

### 2026-10-01 — Per-user Linux accounts ("slots"): shell tool and provisioning, off by default
- New package `workspace/slots` and launcher `workspace/cmd/slotctl`. With `AGENTWORKS_SLOTS=on`
  the workspace service runs each folder-guarded shell command as the caller's own Linux account
  through `sudo` and `slotctl` (after the switch the Landlock policy and namespaces are created),
  and refuses commands that carry no folder guard or come from a user without a slot. A root-owned
  allow-list limits what `slotctl` will start. Off by default, so other deployments are unchanged.
- `deploy/common/provision-slots.sh` (run as root on the host) creates the accounts, the
  sudoers rule and the slot table, and assigns a person to a slot; signing in never does. `slotctl`
  is built into every rootless release; installing it root-owned is the script's job.
- Done in this change: the shell tool. Not done yet: CLI launches and their terminals, ownership of
  a user's runtime folders, the Crew and workflow sharing model. Not deployed or enabled anywhere.

### 2026-10-01 — Accounts are added by an administrator only; invitations and automatic sign-up are gone
- Removed the invitation email (`POST /api/admin/users/{id}/invite`, the Supabase invite call, the
  `invite` option on create, the Resend and Copy-invitation controls, `USER_INVITE_EMAILS`).
  Adding a person in the Users panel still creates their account (role, products); the panel now
  just says to ask them to sign in with Google using that address.
- A sign-in never creates an account any more. `externalAuthIdentityApproved` admits only an
  address already in the user directory, or one named in `ADMIN_USERS` so the first administrator
  can bootstrap their own record. `ensureDirectoryUserForExternal` creates a record only for such a
  configured administrator; the OAuth callback answers 403 "has not been added by an administrator"
  to anyone else. `AUTH_ALLOWED_EMAILS` no longer admits anybody (it used to, and the first sign-in
  then created the account).
- Why: per-user accounts ("slots", private plan) need an administrator-provisioned user before
  anyone can sign in. Existing users already have records and are unaffected. Anyone who was only
  in `AUTH_ALLOWED_EMAILS` and never signed in must now be added by an administrator.
- Left: `SUPABASE_SERVICE_ROLE_KEY` on excellence was only needed for the invite email and can be
  dropped from its `.env` later; the password and bot-route sign-in paths are unchanged. Not deployed.

### 2026-10-01 — Keep prompt contracts upfront and load procedures through skills
- System prompts retain role, access/mode limits, live workspace/grants, secret
  safety, discovery and core memory rules. Skills own operating procedures,
  examples, formats and troubleshooting; tool schemas own argument shapes.
  Skill descriptions retain explicit action triggers. No authorization is added.
- AgentWorks owns mode-specific `project-memory` procedures, Crew Builder
  history/coding guidance and Workflow chat operations references. Run gets
  retrieval/execution guidance without authoring procedures. `MEMORY.md` remains
  the only facts store; the managed memory skill is a procedure, not another store.
  Current secret names remain live context; provider details load from the
  admitted capability tool.
- mcpagent owns the on-demand `runtime-http-tools` skill for progressive CLI/code
  execution. The runtime block owns discovery; redundant available-tools
  reminders are removed. Legacy inline mechanics and that skill share one source. Native API
  schemas remain intact; Agy retains its skill-list fallback. The linked CLI may
  `cd project`; bridge shell calls use absolute paths.
- Readable Code snapshots drive the real query handler through finalized agent
  assembly and inspect its outbound composer before any model turn. External
  state is mocked; runtime policy and tool registration are production paths.
  This replaces reconstruction that missed Code's resolved native-tool mode.
  Capture also found a browser pointer to unattached builder-reference; Code/
  Crew chat pointers now name their attached agent-browser skill.
- Controlled server fixtures reduce Code by 21%, Crew Builder by 40% and workflow
  chats by roughly 39–40%. Crew Run was already compact and adds about 500 bytes
  of shared memory constraints. Live first use and changed-skill native resume
  pass on Claude and Codex. Pi currently lacks local Google auth; Cursor/Muse/Agy,
  complete business flows, total turn cost and latency remain unqualified.
  Details: [prompt discovery design](design/progressive_prompt_discovery.md).
  Existing unrelated suite failures remain open. No restart or deployment.

### 2026-10-01 — Audit-log spawned provider-child env names (disable via LOG_CHILD_ENV=0)
- `providerConnectionSetupEnvironment` and `workflowProviderSetupEnvironment`
  now emit `[CHILD_ENV]` lines showing which variable names a spawned child
  keeps, strips and injects, plus the full sorted name list. Names only,
  never values; a test pins that secret values cannot appear in the output.
- On by default so the fail-open denylist surface stays visible; set
  `LOG_CHILD_ENV=0` (or false/off/no) to disable. Lives in
  `agent_go/cmd/server/child_env_log.go`; temporary observability until the
  builders move to an allowlist.

### 2026-10-01 — A skill with an invalid YAML header stopped the server at startup
- `e05c52f23` made `RegisterEmbeddedSkillsRendered` parse every skill header as YAML, and
  `server.go` stops the process on any error. The Video Studio `google-ai` skill had an unquoted
  `: ` inside its description, so the agent exited at start (excellence, 2026-10-01, status 1,
  502 for about 30 minutes) because AGENT_PRODUCTS was unset there and every product registers.
  Every deploy of that `main` would have failed the same way.
- Quoted the description, and added `TestEverySkillFrontmatterIsValidYAML` which checks every
  `SKILL.md` in the repo, so the build fails before a deploy can. Code:
  `agent_go/pkg/agentprofiles/skill_frontmatter_test.go`. Not done: making a bad skill non-fatal.

### 2026-10-01 — Use live tool discovery and one owner for runtime guidance
- AgentWorks owns product prompts, access/mode constraints and feature skills;
  mcpagent owns dynamic tool discovery, filtering, schemas and provider routing.
  Caller instructions are preserved. AgentWorks code-execution wrappers opt into
  bounded `search_tools` discovery instead of the full upfront HTTP catalog;
  native API schemas and other library consumers' legacy inventory remain.
- Keep essential Code/Crew constraints upfront and render product-option
  procedures into session-local skills. Use rendered skill frontmatter for the
  shared feature bundle's descriptions. Agy gets routing and a skill-list
  fallback until its adapter supports native skill projection. Workflow variable
  and output rules remain AgentWorks-owned; generic HTTP guidance is mcpagent-owned.
- HTTP discovery/schema callbacks now check the current session allowlist as well
  as in-process turn policy, including cached schemas and error suggestions.
  No access is granted by a skill or discovery response.
- Same Code feature fixture shrank from 5,487 to 1,460 bytes. Live first use and
  native resume with changed tools/skills passed on Claude, Codex and Pi; Cursor
  reached its usage limit. Muse/Agy live discovery and total turn cost remain
  unqualified. Existing mcpagent cleanup/API-snapshot and workflow model/path/Agy
  gate test failures were reproduced on baseline worktrees and left open.
  Details and controlled size evidence:
  [progressive discovery design](design/progressive_prompt_discovery.md).
  No application restart or deployment.

### 2026-10-01 — Prompt reduction needs discovery and clear instruction ownership
- Investigated Code, Crew Builder/Run, workflow chat/step composition, mcpagent's
  registry/schema discovery, and native skill projection in owned worktrees.
  Skill bodies already load on demand; feature summaries and product/runtime
  procedures still overlap. The local `get_api_spec` requires an exact tool
  name, so removing its always-loaded name catalog first would break discovery.
- Record the recommended design before changing runtime behavior: canonical
  skill descriptions, one owner for transport instructions, essential mode and
  access constraints retained once, and session-authorized tool search before
  the full catalog is removed. This commit is investigation only.
- Open findings: skill frontmatter triggers are replaced by Go descriptions;
  Code connection timing and workflow hybrid-read guidance disagree across
  documents; Agy skill projection/routing delivery needs verification; existing
  option-specific restrictions must survive any move from summaries into skills.
  Evidence, scope and qualification plan:
  [progressive discovery design](design/progressive_prompt_discovery.md).

### 2026-09-30 — AUTH_SECRET rotation is a scriptable offline command
- New `server rotate-auth-secret`: re-encrypts `config/provider-api-keys.json`
  plus every workflow secrets and provider-credentials document from the old
  secret to the new one, then upserts the deployment's env file. Two-phase
  all-or-nothing (decrypt everything first; any undecryptable blob aborts
  before anything is written), timestamped 0600 backups, decrypt-with-new
  verification, `--dry-run` for pre-flight, non-zero exit on any failure so
  deploy scripts can gate the restart. Run it with agent, workspace, and
  gateway stopped; sessions invalidate on restart (stateless JWT).
- The older `rotate-provider-keys-auth-secret` keeps working unchanged and
  now shares the provider-file rekey helper. Env paths per deploy:
  rootless `$REMOTE_APP/.env`, AWS `/opt/video-studio/.env`, dominion
  `/srv/dominion/.env` (all via `--env-file`). Gmail `credentials.enc` is
  not AUTH_SECRET-derived and stays out of scope.
- Tests: `auth_rotate_cmd_test.go` (end-to-end rekey, dry-run and wrong-old
  write nothing, missing provider file tolerated).

### 2026-09-30 — Folder-guard write paths are boundary-checked before creation
- The shell handler pre-created every `FolderGuard.WritePaths` entry with a
  bare `MkdirAll`, resolving relative entries against the workspace root but
  accepting absolute paths as-is: an absolute outside path, a `..` escape, or
  a symlink redirect created directories anywhere as the service account.
  Each entry is now resolved exactly as the isolator resolves it and checked
  with the working-directory containment helper; escapes fail the request
  with 400 before anything is created.
- Scope note: `/api/execute` is token-gated and proxy-refused, so only the
  trusted agent server sends these configs (FolderGuard is never model-set);
  this closes a confused-harness hole, not a remote one. A check-to-create
  swap race remains in principle; accepted as residual (same account).
- Tests: `shell_guard_writepath_test.go` (resolution table plus a 400-and-
  nothing-created handler case).

### 2026-09-30 — Spawned children get a minimal explicit environment
- Every bare spawn site in `agent_go/cmd/server` (nil `cmd.Env`, which inherits
  everything, or a raw `os.Environ()` assignment) now builds its environment
  from `minimalChildEnv` (`child_env.go`): PATH/HOME/TMPDIR, locale, TERM and
  other non-secret vars carried over only when set. Sites that provably need
  more pass it explicitly: tmux commands keep `TMUX_TMPDIR` for socket
  discovery, CLI status/model probes keep that CLI's documented API key var,
  the provider pty keeps TERM/COLORTERM.
- Deliberate constructions are untouched: explicit caller envs (including the
  workflow credential injection in `workflowProviderSetupEnvironment`), the
  per-account env in `providerConnectionSetupEnvironment`, the trigger notify
  allowlist, and the locked-down git env. The two denylist-based builders
  remain fail-open by design and are follow-up work, as is the mcpagent-side
  tmux session creator, which this repo does not contain.
- Tests: `child_env_test.go` pins the base/passthrough behavior. Full
  `cmd/server` suite shows only 6 pre-existing failures, byte-identical with
  and without this change (verified against the pristine base with the same
  dependency worktrees): sales-crew catalog, delegation-tier defaults,
  playbook catalog x2, a real-tmux keystroke-timing assertion, and workshop
  LLM defaults. Left open; none touch process spawning.

### 2026-09-30 — AUTH_SECRET is cached at startup and cleared from the environment
- The server read `AUTH_SECRET` from the environment on every call, so the
  secret stayed in the environment of every child process (agent shells, CLIs,
  tmux). It is now read once at startup into memory (`InitAuthSecretCache`,
  `services.CacheSecretsKey`) and cleared (`ClearAuthSecretFromEnv`); readers
  use `GetAuthSecret`, which prefers a live env var (tests, CLI commands) and
  falls back to the cache. Same shape as the existing bridge-token handling.
- The workspace service never reads the secret and now unsets it at startup;
  the native shell denylist also blocks it, so shells stay clean even if a
  future path re-exports it. Docker shells were already allowlisted.
- Spawn sites that pass the full parent env through (`os.Environ`/nil-`Env`
  execs) are unchanged by this commit; they inherit a clean environment now,
  and minimal per-site envs remain follow-up work.
- Code: `agent_go/cmd/server/auth_middleware.go`, `server.go`,
  `services/workspace_config.go`, `workspace/server.go`,
  `workspace/security/environment.go`. Tests: `auth_cache_test.go`,
  `workspace_config_key_test.go`, `environment_bridge_env_test.go`.

### 2026-09-30 — Workspace ZIP backup export/import removed
- Removed `POST /api/workspace/export` and `POST /api/workspace/import`
  (`workspace/handlers/backup.go` deleted, routes dropped from
  `workspace/server.go`) after a security review found the extraction loop
  unsafe to keep; the private audit holds the details, not this log. The proxy
  bulk-route entries, the `local_zip` supported strategy, and every UI caller
  (Files tree menu, Backup popup Download ZIP, shared-folder Import) went with
  it. The public share-link folder download is a separate endpoint and stays.
  A router test pins both routes as 404 so a reintroduction fails loudly.
- To restore the capability, reimplement export/import with separator-aware
  containment, symlink resolution before create, and size/count caps, plus
  regression tests — do not revert this commit as-is.
- Code: `workspace/server.go`, `workspace/workspace_backup_removed_test.go`,
  `agent_go/cmd/server/workspace_proxy_policy.go`, `workflow_backup.go`,
  `frontend/src/services/api.ts`, `Workspace.tsx`, `PlannerFileList.tsx`,
  `WorkflowBackupView.tsx`, `BackupPopupBody.tsx`, `SharedFolder.tsx`
  (`ImportProgressDialog.tsx` deleted). Committed bundles under
  `agent_go/static/` refresh on the next frontend build/deploy.

### 2026-09-30 — Process-killing routes: browser ids checked, and admin-only through the proxy
- Audit of the other workspace routes a logged-in user reaches through `/api/wp`:
  `POST /api/browser/cleanup` ran `kill -9` on any process id in the body, so any user
  could stop the agent, gateway or workspace service (all one account) or another user's
  work; `{"all": true}` kills every user's chromium. `POST /api/processes/cleanup` sweeps
  workflow processes server-wide.
- The handler now kills only ids that are in the current browser-process list
  (`filterBrowserPIDs`, `workspace/handlers/browser_processes.go`). The proxy makes
  `api/browser/cleanup` and `api/processes/cleanup` admin-only (`workspaceProxyAdminOnlyRoutes`);
  on a single-user machine everyone counts as an admin, so the top-bar runtime-health control
  keeps working there. In multi-user mode an ordinary user's click on cleanup now gets a 403;
  hiding those buttons for non-admins is not done.
- Not changed, noted: `GET /api/browser/processes` and `GET /api/processes` list server
  processes to every user; `GET /api/cdp-check` probes local ports; `POST /api/skills/cli/install`
  runs `npx skills add <source>` by design. Not deployed.

### 2026-09-30 — Browsers can no longer call the shell-execute route through the proxy
- Found from a pasted Slack message: a logged-in user could `POST /api/wp/api/execute`
  with only `{"command": …}`. The workspace service runs a command with no `folder_guard`
  unconfined from the workspace root (`workspace/handlers/shell.go`, the "non-isolated"
  branch), the proxy adds the service token itself, and the proxy only inspects path fields,
  not command text. Any user could therefore run commands as the shared server account and
  read other users' chats and files, or delete them. Logged: his calls returned 200 at
  16:35-16:36 on excellence. Whether anyone used it to read or delete anything is not known.
- `api/execute` is now in `workspaceProxyRefusedRoutes` (server-only). The UI never called
  it and the agent server reaches the workspace service directly. The test that allowed
  shell text through the proxy now asserts it is refused. Code:
  `agent_go/cmd/server/workspace_proxy.go`.
- Still open: the workspace service itself runs an unguarded command when a request has no
  `folder_guard`; only the proxy stands in front of it. Other routes that accept a command
  were not audited. Not deployed.

### 2026-09-30 — Each rootless release keeps the source it was built from
- `deploy/rootless-linux/build-and-activate.sh` copies the three repos into
  `<release>/source/<repo>` (no `.git`, `node_modules` or `dist`) right after writing
  `SOURCE_REVISIONS`. Confida, SparkQuill and excellence get it; RTS uses
  `deploy/aws-ec2/` and is unchanged. Requested by the owner after the excellence
  release folders were deleted on 2026-09-30.
- It lives inside the release, so it is pruned with it and is **not** a backup: a wipe of
  `/srv/<product>` removes it too (only `data` and `state` survived). It is owned by the
  shared service account, so any process running as that account can read, change or
  delete it, and confined CLIs get no grant to it. Measured on this checkout: about
  380 MB per release before a server clone's smaller tracked-only tree.
- Not deployed yet.

### 2026-09-30 — Local linked-runtime qualification is partial; step searches name `output`
- Real local CLI fixtures passed artifact discovery, linked reads, authoritative
  writes through admitted tools, native resume and cleanup for Claude, Codex,
  Cursor and Pi. Claude and Cursor native writes were denied by existing policy;
  their bridge writes passed. Pi required its existing provider credential in
  the managed process. Muse completed linked artifact work but resume exceeded the 150-second budget;
  Agy stopped at sign-in. Do not call all six providers qualified.
- Add explicit `output` search-path guidance to mcpagent's step instructions,
  matching the `project` guidance: search from the private cwd can skip links.
  Keep tool modes and permissions unchanged. macOS checks are not Landlock
  certification or a production deployment gate; both-mode Linux qualification
  remains required. Evidence and reproduction: [local report](design/local_linked_runtime_qualification.md).


### 2026-09-30 — Linked runtimes tell the agent to name `project` when searching
- The `project/` link is a symlink, and search tools do not walk into a symlink they
  find: plain `rg` from the runtime folder, and Claude Code's Grep and Glob with no path,
  returned no workflow files; with `project` as the path (or `rg -L`) they did. A
  `RIPGREP_CONFIG_PATH` with `--follow` fixed `rg` but not Claude Code, so it is not a fix.
- The Crew and workflow runtime instructions and the workflow shared prompt now say to
  always pass `project` (or `project/<folder>`) as the search path. Per-folder links would
  not help (ripgrep skips those too). The real fix, if the prompt proves unreliable, is a
  bind mount that makes `project/` a real directory: the launcher would need its own
  namespace plus a host AppArmor allowance, and macOS keeps the link. Cursor, Codex, Muse,
  Pi and Agy were not tested; each needs the same live check.

### 2026-09-30 — Workflow steps link their own iteration output into private runtimes
- Extend the linked-runtime design to execution steps and step orchestrators.
  Keep the existing per-session CLI directory identity and isolated generated
  instructions/skills/configuration. Add `output/` pointing to the exact current
  step artifact directory, including group, nested Agent and message-sequence
  output overrides. Files written through it are authoritative artifacts with
  no copying or synchronization. API models and review/learning agents do not
  acquire an output link merely by inheriting shell environment.
- The dedicated step session's final folder guard admits the link target.
  Resolve existing ancestors before creating missing output directories; reject
  mismatched or unauthorized targets, including symlink escapes. Replace inherited
  chat workspace grants with that step's existing grants for native CLI security,
  retaining its admitted DB/cache/KB/owning-subtree/host capabilities. Keep the
  CLI home inside the step runtime. Links do not enable native write tools or
  change the existing tool modes or broader sandbox limitations.
- Same-session resume keeps cwd and link stable. An obstructing file/directory,
  wrong target, unavailable runtime or cross-iteration rebinding fails rather
  than overwriting content or falling back to the real workflow cwd. Session
  cleanup removes the private link, never its output target. Closing an isolated
  turn no longer attempts projection cleanup in its real bridge working directory.
- Code: `pkg/orchestrator/coding_agent_output_runtime.go`, step factories and
  mcpagent `WorkspaceRuntimeConfig.OutputDir` / `agent/isolated_output.go`.
  Tests cover all six providers including Agy, resume and cleanup, iteration/group
  separation, target failures and policy narrowing; a Linux Landlock test verifies
  direct atomic output writes and denial of sibling/other-iteration mutations.
  Not deployed; live CLI qualification remains required before deployment.

### 2026-09-30 — No public default secrets key left in the decrypt path
- `services/workspace_config.go` still derived its decryption key from the public
  `dev-secret-change-in-production` when `AUTH_SECRET` was empty. The server already
  refuses to start with that value, so it was dead-but-confusing. `deriveSecretsKey` now
  returns nil without `AUTH_SECRET` and decryption fails with a clear error; the value is
  trimmed like the server's own reader. No real secret was ever in the code (`env.example`
  holds a placeholder).

### 2026-09-30 — Workflow Run and Builder also link the real workflow into private runtimes
- Apply Crew's linked-directory design to workflow conversational CLI runtimes.
  Keep the existing user/workflow/chat/provider/mode identity and distinct Run/
  Builder prompts and skill bundles. Add `project/` pointing to the authoritative
  workflow folder; native tools use that prefix or `cd project`, while bridge
  paths remain workflow-relative. Prompts, skills, configs and private CLI homes
  stay outside workflow documents. The earlier shared-folder plan is superseded.
- Keep the existing private directory digest, so adding the link does not move
  a saved session or discard compatible native resume. The updated shared prompt
  carries the path contract in both mode definition fingerprints, ensuring an
  old retained CLI reloads instructions. Mode/chat/provider boundaries and
  Codex's project-directory check remain enforced. Restored workflow terminals
  defer to fresh query admission even during rollback instead of attaching a
  saved pane with potentially stale access and working-directory permissions.
- Read-only workflow turns no longer retain the initial real-folder write grant
  in CLI security or inherit folder-guard workspace write grants in the final
  Landlock policy. The CLI can write its private runtime and read granted data;
  authorized workflow execution still writes its results through the backend.
  Builder retains its authorized project/folder writes. Existing blocked-path
  and host-grant limitations are not resolved by a symlink.
- `AGENTWORKS_ISOLATE_WORKFLOW_CLI=false` remains a transitional rollback for
  writable Builder turns. Run always uses a private linked cwd, even with that
  flag, because the launcher grants cwd writes automatically. Link/state errors
  fail launch instead of falling back to the real workflow folder. API models
  and workflow step agents keep their existing working-directory behavior.
- Code: `workflow_cli_isolation.go`, `cli_landlock.go`, `server.go`, and
  `internal/agentworksproduct/prompts/workflow-shared.md`. Tests cover all six
  providers (including Agy), mode contracts, existing-session resume, link
  failures and final read/write policies. Linux launcher tests exercise the
  linked-file boundary. Not deployed; authenticated CLI qualification in both
  modes remains required before deployment.

### 2026-09-30 — Crew Run and Builder use private runtimes linked to the real project
- Reverse the shared Crew prompt/folder decision in PLAT-371. Every Crew coding
  CLI uses a private directory keyed by user, project, chat, provider and mode.
  Its `project/` directory link points to the authoritative Crew files; generated
  instructions, projected skills, CLI configuration and private homes stay beside
  the link. Directory linking preserves new files, atomic saves, renames and
  deletes without copying or synchronizing project data. Code and workflows keep
  their existing working-directory policies.
- Access selects the mode: owners use Builder; readers, guest function calls and
  downgrade-only pinned turns use Run. Run has a separate prompt and `crew-run`
  skill, without the built-in authoring bundle or feature/setup/memory-write
  instructions. Builder retains its feature bundle and adds `crew-builder`.
  The per-message reader notice, denied tools, folder guards and terminal access
  checks still enforce the current turn's restrictions.
- The link grants no permission. Landlock grants the real project separately:
  read/write for Builder, read-only for Run. A reader's final CLI policy drops
  initial/attached-folder workspace write grants and allows writing only the
  private runtime. Linux launcher tests verify read, create, edit, rename, delete
  and denial of a nested link to an ungranted folder. This does not resolve the
  existing confinement/host-grant issues listed below.
- Resume requires the same private directory (including Codex's project-directory
  override). Old native sessions launched in the shared Crew folder start fresh
  once, using saved application history; same-mode private sessions resume across
  restarts and day-folder rollover even after a compatible definition refresh.
- Code: `crew_cli_runtime.go`, `pkg/cliruntime/project.go`, `cli_landlock.go`,
  `internal/workproduct/prompts/run.md`, and mode skills. Provider skill projection
  is tested for Claude, Codex, Cursor, Pi and Muse; all six Crew CLI runtime
  identities are tested. Agy uses launch-context prompts and the bridge skill
  reader; its private-home, tool-hook and prompt-input tests pass too.
  Not deployed: authenticated live CLI qualification in
  both modes remains required by the deployment decision below.

### 2026-09-30 — Chat jumpiness: no clear during a running turn, no doubled reply at the end
- The live streamed text auto-cleared after 60s of silence (the code comment said 3s; it
  never was) even while a long tool call was still running, so the reply vanished and
  jumped back. It now re-arms while a tab of that session is streaming
  (`armStreamingInactivityClear`, `useChatStore.ts`).
- The finished reply is added as a normal row while the live row stays up to 500ms, so a
  long answer showed twice and then collapsed. The live row is dropped as soon as the
  finished reply says the same thing (`liveTextAlreadyCommitted`,
  `TerminalEventTranscript.tsx`). Found by reading the code, not reproduced live.

### 2026-09-30 — Agy tool calls show while the turn runs, not only at the end
- Agy has no live tool stream; its calls are read from the conversation database.
  They were published only once the whole turn settled, so a 4-minute Confida turn
  showed no tools until the end. `ReadRetainedTurnStructuredProgressMessages` now
  also publishes calls a later step follows (`agyCompletedToolCallsSince`): tools
  run one after another, so those have finished. The newest step waits for the next
  poll. Code: provider repo `pkg/adapters/agycli`. Not verified live yet.

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

### 2026-10-03 — Cursor curated models can exceed the account's live availability
- RTS's current CLI/key rejects the newly curated GLM 5.3 and Flash choices.
  `WorkModelsPanel` currently unions live choices into the curated list; a
  successful list therefore does not remove unsupported curated entries.
  Account-aware model filtering remains a follow-up; do not describe those GLM
  choices as qualified on RTS until its live account list and inference pass.
- Bare `grok-4.6` resolves to Fast for this key while the current metadata assumes
  standard pricing unless the selector explicitly contains `fast=true`.
  Live suffix IDs such as `cursor-grok-4.6-high` also need family-aware metadata.
  Model/pricing resolution needs follow-up before certifying cost attribution.

### 2026-10-03 — Cursor's separate reasoning-effort option is not forwarded
- The Cursor adapter currently launches the selected model ID without mapping
  `CallOptions.ReasoningEffort` into Cursor's native model parameters. A separate
  effort selection therefore does not override the CLI's model default.
- Explicit parameterized model IDs such as `grok-4.6[effort=xhigh,fast=false]`
  are passed through unchanged, including choices discovered from the live CLI
  list. Wiring the separate effort option requires a follow-up adapter change;
  this catalog update does not advertise effort controls for the new models.

### 2026-10-01 — Ashutosh's lost terminal and retained submission retry need separate evidence
- After the answered 14:26:04 IST submission, the 14:26:25 retry reused its
  submission ID. Returning the original successful receipt without another
  provider send is intentional idempotency, not proof of a new model turn.
  Browser telemetry also recorded a conversation error for the original
  response; it does not include the error text, so its cause is unconfirmed.
- By 14:26:42 IST the main-terminal stream returned 410; a read-only tmux check
  and subsequent lease logs confirmed the terminal no longer existed. The
  logs inspected do not explain its exit. The existing new-message route
  closes an idle retained session with no live terminal and resumes normally;
  changing replay behavior or sending the user's prompt again during diagnosis
  would risk duplicating an already-answered request. No production session
  was modified in this investigation.

### 2026-10-01 — Generated model names can disagree with the actual runtime
- Excellence/Ashutosh's 14:15 IST Code turn used `gpt-6.1-sol`: both the app's
  generation records and Codex's native rollout `turn_context.model` confirmed
  it. The earlier 13:07 turn used `gpt-6-sol`. The later response saying
  "GPT-6 (Codex)" was generated prose, not an additional catalog entry or proof
  that the model switch failed. No runtime-switch change is needed for that
  latest turn.
- Use native turn metadata to verify the executed model; a saved selection or
  requested-model log alone is insufficient, and asking the agent its model
  can produce a wrong answer. The UI currently emphasizes the selected model;
  explicit pending/running model feedback and accurate agent self-reporting
  remain open. Related code: `WorkModelsPanel`, `agent_profile_routes.go`, and
  the Codex adapter's native transcript.

### 2026-10-01 — Model selection is pending while typing directly into a retained terminal
- Excellence/Ashutosh's Code saved `gpt-6.1-sol` in its project manifest, while
  its retained Codex runtime still had `gpt-6-sol`. The model was saved after
  that runtime's last chat turn. Saving is working; model/account reconciliation
  and relaunch happen in the next project chat request, and native terminal
  input bypasses that request path.
- The Models panel says "next message", which can imply terminal input applies
  the change too. Pending-versus-running feedback and native-terminal handling
  remain open; the current way to apply the saved choice is to send the next
  message through the chat. Code: `WorkModelsPanel`, `changeWorkRuntime` in
  `WorkSurface`, and `prepareProductConversationTurn` in `agent_profile_routes.go`.

- **Remaining local linked-runtime CLI qualification (2026-09-30).** Muse's
  integrated resume exceeded the fixture budget after linked artifact work passed; Agy's
  private launch needs sign-in. Claude/Cursor native writes remain subject to
  their existing restrictions. The [local report](design/local_linked_runtime_qualification.md)
  records what actually passed; do not treat bridge writes as native-write proof.


- **Two mcpagent cleanup tests still expect deletion of unmarked provider folders
  (confirmed 2026-09-30).** `TestAppendCodingAgentWorkingDirOptionCleansInactiveGeneratedArtifacts`
  and `TestAppendCodingAgentWorkingDirOptionRemovesInactiveProviderDirsInWorkflow`
  fail unchanged on `origin/main` (`fa6a186`) as well as the linked-output branch.
  Their fixtures/whole-directory deletion expectations conflict with the current
  cleanup safety contract. Correct that contract separately; this task does not
  reintroduce broad deletion. Output, resume, provider-policy and safe-cleanup
  regression tests pass.

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
