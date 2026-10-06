[← platform / security-sandbox](index.md)

# PLAT-404 — Slot accounts: private home, system Chrome, browser socket folder, slot table, Usage terminal, tunnels

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | fixed on `main`; Chrome grant deployed to Excellence only. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; the Chrome grant (`eb278bedf`) is only in Excellence `agents-0cf68aa9` / `agents-e7db4f50`; the others are also in the commits behind Confida `confida-23270875`, Dominion `5e15f373`, SparkQuill `sparkquill-49a1e676`; deploy pending where not listed (RTS builds from `ad3956735` are older) |
| Severity | P1 (Code browser and terminal unusable for slot users; shared home) |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | PLAT-374 (blocked file, weaker sandbox; its `HOME` change is what makes agent and terminal share a home), PLAT-403 (terminal), PLAT-401 (managed browser launcher), slot accounts (`docs/DECISIONS.md` 2026-10-01) |

Commits: `fd137d5be` sandbox home, `975ef23a4` one home per person, `eb278bedf` Chrome, `ba95c2f3c` socket folder, `265dbcc86` slot table,
`6c6229882` Usage terminal, `2016b526d` tunnel prompt.

## Problems, causes, what was done

- **Sandbox home was owner-only.** Installing nvm in the Code terminal failed; reproduced as the user's own account on Excellence. The private home
  (`<project>/.sandbox-cache/home`) is created by the service account with mode 0700, so the slot (same group, other user) could not enter it
  (`mkdir: Permission denied` for any installer under `$HOME`); the Shell tool uses the same folder. `privateSandboxHome` now makes `.sandbox-cache`, `home` and
  `.config` group rwx + setgid every time (existing folders heal on the next start); the terminal creates an empty `~/.bashrc`. Verified with the real nvm
  installer as the slot (`nvm install 24` gives Node 24). Shared workspace code: no per-host step.
- **One home per person in Code.** A coding agent's own nvm install landed in the platform account's real home `/srv/agents/home` (`.nvm`, `.bashrc`, `.profile`
  edited): native mode keeps the real host HOME in `privateSandboxHome`, so the agent saw Node 22 while the terminal ran Node 24, and the agent could write a home
  shared by every user. Now a Code command run as the owner's slot (terminal, and the agent's `execute_shell_command` in a Code project) gets the slot's own home
  (`/srv/<app>/slots/home/<slot>`) as HOME plus a Landlock write grant (`Isolator.UserHome`); nvm's default Node from that home leads PATH so a non-interactive
  `sh -c` runs the same node. Other slot commands get the project's private home whatever the native setting (`SlotHomeEnv`); users without a slot keep the
  per-project home. Workflows and Crew keep per-project homes. Verified on Excellence and Confida.
- **System Chrome not granted.** "Failed to launch Chrome at /usr/bin/google-chrome: Permission denied" on Excellence: it resolves to `/opt/google/chrome`, not
  granted by Landlock; it only worked before PLAT-374 because the removed mount-namespace fallback could see /opt. `/opt/google/chrome` is in
  `landlockSystemReadPaths` (read + execute, dropped when absent); `TestSystemChromeRunsInsideTheSandbox` runs the real Chrome in the sandbox. Every Linux server.
- **Browser socket folder.** "Socket directory '/run/user/990/agent-browser' is not writable": a project browser (`agents--project-...`) gets no scoped socket
  folder and in native mode nothing set `AGENT_BROWSER_SOCKET_DIR`, so agent-browser used `$XDG_RUNTIME_DIR`; this worked only under the fallback removed by
  PLAT-374. A sandboxed command without a scoped socket now always gets `AGENT_BROWSER_SOCKET_DIR=/tmp/.agent-browser`, the folder the sandbox grants. Same commit:
  the New chat button became icon only in the composer's neutral colours (slides out on hover/focus), see PLAT-406.
- **Re-running the slot setup took Confida's slot table away.** `provision-slots.sh init` for the default product resets `/etc/agentworks` to 0750 root:agents;
  Confida's service reads its table through it (`o+x` added after the 2026-10-01 incident), so after Excellence's init (2026-10-02 slot-program refresh) Confida's
  shells would fail with "slot table unavailable". Found testing the terminal on Confida; `chmod 0751 /etc/agentworks` fixed it by hand; the script now sets `o+x`
  unconditionally after creating the folder.
- **Provider Usage terminal.** A manager of a shared provider account gets a live terminal for Usage and could type `/logout` (or change settings) for everyone on the
  account. In a `usage` setup session typed input goes through the coding agents' slash allowlist (`AGENTWORKS_TERMINAL_SLASH_COMMANDS`, default `/usage`): other slash
  lines are erased, menu navigation dropped. Non-managers still get server-collected text. Supersedes the unpushed "usage read-only for everyone" branch.
- **Tunnels in the Code agent prompt.** Owner: the platform should be secure and people can already do anything from the terminal, so `codeHostSafetyInstructions`
  no longer forbids tunnels, reverse proxies and port forwarders (the agent says once that the app becomes reachable by anyone with the link); it still forbids browser
  IDEs, SSH/remote-desktop servers and VPNs (the 2026-09-30 code-server incident) and binding ports to all interfaces.

## Left

- Deploy the Chrome grant (`eb278bedf`) to Confida, Dominion, SparkQuill and RTS.
- Old /tmp credentials and `/srv/agents/home` contents (nvm edits) are PLAT-374's cleanup.

## Register notes

[PLAT-404](plat-404.md), P1, fixed on `main`; Chrome grant deployed to Excellence only.
Group-accessible sandbox home, one home per person in Code, `/opt/google/chrome` granted, browser socket folder always
set, slot table readable after re-init, Usage terminal slash allowlist, tunnels allowed in the Code prompt.
