[← code / frontend-chat](index.md)

# PLAT-403 — Code terminal: a shell as the person's own account, tabs, colours, copy and scrolling

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | frontend-chat |
| Summary | fixed on `main`; server scroll batching on Excellence only. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; in the builds of Excellence `agents-0cf68aa9` / `agents-e7db4f50`; the server-side scroll batching (`b3d82b816`) is only in those two Excellence releases, deploy pending elsewhere; everything else here is also in the commits behind Confida `confida-23270875`, Dominion `5e15f373` and SparkQuill `sparkquill-49a1e676` (RTS: not verified, no terminal there, see Left) |
| Severity | P2 (a new product surface plus a series of defects found while using it) |
| Date | 2026-10-03 |
| Owner | frontend-chat |
| Related | PLAT-374 (blocked file, weaker sandbox), PLAT-404 (private home, one home per person), `docs/DECISIONS.md` 2026-10-03 terminal entries |

Commits: `265dbcc86` terminal, `9c33b310f` xterm.js, `6ca99b48b` Homebrew colours, `e6338e925` copy, `e526cf6eb` history and colours,
`fb9505f98` empty `cd`, `ed1191f08` local home and icon, `d687b03dd` sandbox switch, `147af8766` tabs, `975ef23a4` smooth scroll,
`b3d82b816` server scroll batched.

## Problem and what was done

- **A terminal in Code (reverses 2026-09-28).** On the user's request Code got a Terminal tab again. The old panel (`7affa8a90` / `dc8cdb8c4`) was ported, not
  reverted. The agent server authorizes the owner (Code is owner-only), builds the Code's Folder Guard and stamps the user on every call; the workspace
  service starts a tmux server in the same Landlock sandbox as the shell tool (private /tmp, private /dev/pts) and attaches from inside it. Where slots are on,
  all of it runs as the caller's slot: `slots.WrapCommandFile` leaves the request in a file in the slot's run folder so the terminal stays the command's stdin,
  tmux files live in `<slot run folder>/shells/<id>` (group-shared with the service, socket `chmod 0660` after start), and a person without a slot gets 403.
  Scratch folders the platform creates for a sandboxed command (`.tmp`, `.cache`) became group-writable (a slot could not create a temp file in its TMPDIR).
- **Looks.** xterm.js stays (engine of VS Code, Hyper, JupyterLab); ttyd/wetty/GoTTY were not used. `CodeShellPanel` takes the coding-tool terminals' theme and font
  and the official add-ons (WebGL with fallback, http/https links in a new tab, search Ctrl/Cmd+F, Unicode 11), a toolbar (search, copy, paste, clear, size 10-22
  remembered, full screen, status dot) and a quiet automatic reconnect (4 tries, 1-8 s). Packages `@xterm/addon-webgl`, `-web-links`, `-search`, `-unicode11`.
  Default colours became a Homebrew scheme (the plain xterm default was white on black); a palette button switches to Classic and is remembered; Homebrew's
  blues are lightened (the dark blue is unreadable and `ls` prints directories in it). The server prompt was `user@host:/srv/agents/data/docs/_users/...` wider than
  the screen, so `PROMPT_COMMAND` shows only the folder name in bold (`code $`); a shell already running keeps its old prompt until Stop and Start.
- **Copy and scrolling (four steps).** (1) tmux `mouse on`, `history-limit 50000`, `status off`, colour aliases (GNU) / `CLICOLOR` (BSD), a plain
  `command_not_found_handle` (Ubuntu's Python handler crashed: its database cannot be opened in the sandbox), the person's own `~/.bashrc` sourced once.
  (2) Nothing could be selected: tmux mouse mode took every drag, and a refused tmux ("access not allowed", exit 0) read as running. tmux mouse off; the wheel
  sends `{"type":"scroll","lines":N}`, the workspace runs tmux `copy-mode -e` + `scroll-up/down`, the first keystroke sends `{"type":"scroll","cancel":true}`;
  `interactiveShellRunning` treats "access not allowed" as not running. (3) An attempt with tmux's alternate screen off (`smcup@:rmcup@`) for browser-side smooth
  scrolling (`975ef23a4`) did not work: tmux repaints its screen, the browser never held history. (4) Reverted to server scroll, at most once per animation frame
  (deltas add up), one tmux command per message (`if-shell #{pane_in_mode} '' 'copy-mode -e'; send-keys -X -N N scroll-up`); tmux's mouse stays off so copy works.
- **Empty `cd`.** In a sandboxed terminal `$HOME` is the private home inside the project, shown as `~`; an empty `cd` went to "some root folder". `PROMPT_COMMAND`
  defines `cd` so no argument (or `~`) goes to `AGENTWORKS_START_DIR`; `cd -`, `cd <path>`, `cd ..` unchanged; the prompt names the folder (`${PWD##*/}`). An
  unconfined terminal keeps the normal `cd`.
- **Local terminal and home.** Locally (native mode) the sandboxed command kept the real `HOME`, which Code's strict sandbox forbids reading
  (`~/.bash_profile: Operation not permitted`, git and codex could not read their config). A non-slot terminal now gets a private home in the project
  (`<project>/.sandbox-cache/home`); `claude` and `codex` are not on the sandbox PATH and have no login (the coding agents run through the chat). The terminal icon is
  the plain `>_`. `interactive_shell_darwin_test.go` now runs natively and fails with those errors without the fix.
- **Sandbox switch.** The terminal follows the coding agents' `AGENTWORKS_CLI_FULL_UNCONFINED` on a person's own machine: the agent server sends `unconfined`, the
  workspace service honours it only with `AGENTWORKS_TERMINAL_UNCONFINED=on`, `NATIVE_WORKSPACE=true` and per-user accounts off. On servers the terminal keeps the
  strict Landlock sandbox and runs as the person's account, which is stronger than the chat coding tools (shared platform account except the rollout user);
  aligning it down would weaken it. Tests: `TestInteractiveShellUnconfinedIsLocalOnly` (six cases) and a Mac end-to-end check.
- **Tabs and slot shells.** Up to 3 terminals per person per Code: the stream/stop routes take `tab` (1..3, else refused); tab 1 keeps the pre-tab shell id; the panel
  has a tab strip (`+`, close stops that shell), keeps hidden tabs connected, remembers tabs per Code. Search stays in the toolbar; copy, paste, clear, size,
  colours, full screen and new terminal are in a `⋯` menu with shortcuts (⌘ on a Mac; Ctrl+Shift+C/V/K elsewhere so Ctrl+C stays the interrupt; Alt+1..3 tabs).
  Found on Excellence: (1) a slot terminal kept the service account's HOME (`/srv/agents/home/.profile: Permission denied`); (2) tmux 3.3+ refuses clients of
  another user and the slot's tmux runs in the sandbox's user namespace where the service is the overflow user, so the service could not see or stop a slot
  shell and every start left another tmux server (five for one terminal); (3) with mouse on, tmux's right-click menu covered the browser's. Fix: every sandboxed
  terminal gets the project's private home (group-accessible); slot shells grant `server-access -a -w` to the overflow user (the socket's file mode still limits
  who connects); tmux prefix and right-click bindings removed; the launcher refuses a policy with fields it does not know (an old launcher ignored `hidden_paths`).

## Verified

Real-sandbox shell tests on Linux (`interactive_shell_e2e_linux_test.go`, private PTY) and `interactive_shell_slot_e2e_linux_test.go` run on Confida: the shell is
the user's slot, has a pty, cannot read the service `.env` or list other people's folders; orphan sweep `interactive_shell_sweep_test.go`. Copy, scroll, cd and tabs
checked on a Mac and on Excellence/Confida (normal and as a user's slot); HOME in the project, service reaches the shell, bindings off, Stop leaves no tmux server.
Frontend tests for palette, blues readability and wiring; the xterm panel was rendered in a browser against a fake connection (not at first against a real shell). One
non-slot Linux e2e run failed on a loaded server and did not fail again in five re-runs (fixed waits in those tests).

## Left

- RTS: a raw shell can reach the instance role through IMDS; no terminal is offered there until that is closed.
- A non-strict guard as a slot still fails where the platform's Gmail tool config folder is service-only (Excellence `stat .../gog: permission denied`; also non-strict workflow shells).
- Four orphaned tmux servers of one user's Code terminal from before the tabs fix still run on Excellence (left for the user to decide).
- After a reconnect the browser terminal has only the visible screen; older output stays in tmux.
- Deploy the server scroll batching (`b3d82b816`) beyond Excellence.

## Register notes

[PLAT-403](plat-403.md), fixed on `main`; server scroll batching on Excellence only. Real
shell as the person's slot, up to 3 tabs, themed xterm.js, copy and wheel scroll through tmux; RTS has no terminal yet.
