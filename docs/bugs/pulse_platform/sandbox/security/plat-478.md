# PLAT-478 — Slot shell commands never worked end to end on RTS; slots are canary-stage, not production-ready

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | fixed on main 2026-10-04, not deployed. |

Status: P1. Layers 2 and 3 FIXED ON MAIN 2026-10-04 (code, deploy scripts, deploy self-test), NOT deployed; RTS still mitigated (the admin account's slot released). Found 2026-10-04 when a Crew on RTS first used the workspace shell tool.

## What happened

Slots (per-user Linux accounts, enabled on RTS 2026-10-01) run the workspace shell tool as the user's own account through `slotctl` and the Landlock launcher.
On RTS the chain failed three times in a row, each failure hidden behind the previous one:

1. `slotctl.json` `allowed_cwd` named `<app>/data/docs`, which does not exist on RTS (docs at `/data/video-studio/docs`): exit 126 "outside the allowed folders". Fixed (PLAT-476, script + live file).
2. `<app>/releases` was 0700, so the slot user could not reach `video-studio-landlock-runner`: "fork/exec ...: permission denied". Fixed live: `chmod 711` (undo: `chmod 700`). Excellence/Confida: 775/755, fine. Not yet in the deploy scripts.
3. The launcher stats every granted path before applying Landlock, and fails closed when it cannot: "SANDBOX_UNAVAILABLE: inspect Landlock path: stat /data/video-studio/browser-profile-projects/project-...--browser: permission denied". Browser profile folders are 0700 app-owned (they hold logged-in sessions), on Excellence too; the grant appears for project (Crew/Code) chats, not workflows. Not fixed.

Excellence: the slot shell ran 13 times on 2026-10-04, all in a workflow, 0 failures; a project chat there has not been tried and probably hits layer 3.

## Mitigation on RTS (2026-10-04, owner approved)

`deploy/aws-ec2/slots-admin.sh release aa73da63e26b40a1bb701c2b4c024870` (the admin account; held `slot01`). Its shell tool runs as the app account again, as before slots.
Restore with `slots-admin.sh assign aa73da63e26b40a1bb701c2b4c024870 slot01`. The other 6 assigned RTS accounts (slot02-07) still hit layer 3 in project chats.

## Incident caused by the mitigation (2026-10-04, fixed)

`slots-admin.sh release` rewrote `/etc/agentworks/slots.json` as root with mode 0640 but without restoring its group, so the file became `root:root`
(it must be `root:<product>`, as `provision-slots.sh` line ~276 sets it). The RTS service could no longer read the table and, with slots in opt-in mode,
refused every slot user's shell command: "HTTP 403: No account slot for this user / slot table unavailable: permission denied". Restored live via SSM
(`chown root:video-studio`, 0640; the service reads it again, slots 02-07 listed). Script fixed: `assign` and `release` keep the existing owner and group
(`os.chown` before the atomic replace); test in `deploy/common/test_provision_slots_config.py`. `docker`'s rewrite of slotctl.json was already correct (root:root 0644).

## Why the slot command was granted the browser profile (layer 3, traced)

1. agent_go, `pkg/workspace/execute_shell_command.go` (after `[FOLDER_GUARD_RESOLVE]`): every folder-guarded shell request gets
   `FolderGuard.BrowserSession = common.SandboxBrowserSession(sessionID)`, the session's own managed browser
   (`<namespace>--browser`, e.g. `project-<hash>--browser` for a Crew/Code project, `workflow-<hash>--browser` for a workflow chat
   whose session has a browser namespace). The intent: a command may drive ONLY its own browser.
2. Workspace service, `handlers/shell.go` passes it to `security.Isolator.BrowserSession`; `Isolator.scopeBrowser()` (isolator.go)
   then appended that browser's socket folder (`/tmp/.agent-browser/o/<id>`) and its Chrome profile
   (`browserconfig.ProfilePathForSession`: `<AGENT_BROWSER_SHARED_PROFILE>-projects/<session>`) to the command's WRITE grants, and
   created both (MkdirAll 0700, as the service account).
3. For a slot user the command is wrapped (`slots.WrapCommand`: sudo -> slotctl -> `video-studio-landlock-runner`), so the launcher
   runs as the slot. `RunLandlockLauncher` stats every policy path (`addLandlockPathRule`); a path it cannot stat is an error, so the
   0700 app-owned profile root (`browser-profile-projects`, not traversable for the slot) failed the whole command:
   `SANDBOX_UNAVAILABLE: inspect Landlock path: stat ...: permission denied`. The socket folder did not fail only because, with the
   private /tmp, it does not exist inside the command's own /tmp (ENOENT is skipped).
4. Workflows on Excellence passed because a workflow step's session has no browser namespace: no `BrowserSession`, so the profile
   was not in the policy (the shared profile roots the launcher adds for an unscoped command are silently dropped when they cannot
   be stat'ed). A workflow CHAT with a browser namespace fails the same way (reproduced in the container test below).

The grant was useless to a slot anyway: Chrome started by the slot could not open an app-owned 0700 profile, and agent_go already
refuses `agent-browser` in `execute_shell_command` ("use the agent_browser tool"); a standalone `agent-browser` command reaching the
workspace runs as the service account (`isStandaloneBrowserCommand`).

## The rule (decided 2026-10-04, DECISIONS entry "Slot-run shell commands never receive app-private paths")

- A command run as a slot never receives an app-private path: the browser profiles (`<profile>`, `-users`, `-workflows`, `-projects`,
  and `AGENT_BROWSER_PROFILE_ROOT`'s), the browser socket folders (`/tmp/.agent-browser`, `<docs>/tmp/.agent-browser`) and the app
  state root (`AGENTWORKS_STATE_ROOT`, unless it contains the docs root). `scopeBrowser` adds nothing and creates nothing for a
  slot; `landlockPolicy` filters the request's read/write grants (`withoutAppPrivatePaths`, logged `[SLOT_GRANT]`) and marks the
  policy browser scoped, so the launcher adds no shared browser folder. Nothing is opened up: no chmod/chgrp/ACL of those trees.
- The launcher leaves out a policy grant its account may not stat (EACCES/EPERM only), printing
  `SANDBOX_GRANT_SKIPPED: <path> is not accessible to this account; it is not granted` on the command's stderr. It grants less,
  never more; ENOENT is skipped as before; every other error (ELOOP, EIO, a failed open, ruleset, restrict, chdir, hidden or
  read-only overlay paths) still refuses the command. A slot command never runs unconfined or as the app account (PLAT-451 kept).
- What a slot shell can do with the browser: drive its project's managed browser through the platform (`agent_browser` tool, or a
  plain `agent-browser ...` command, which the workspace runs as the service account with that browser's own folders); start its
  own throwaway headless Chrome/Playwright (no profile, e.g. the `agentworks_browser.py` helper). What it cannot: read or write any
  managed browser profile (cookies, logins), reach another browser's socket, or launch Chrome on the project's saved profile.

## Threat note: what a slot account reaches after this change

| | Read | Write |
|---|---|---|
| Its user's own tree (`_users/<id>`, group slotNN, 2770) inside the command's grants | yes | yes, where granted |
| Shared folders it is granted (Workflow/..., group `<prefix>shared`) | yes, where granted | where granted |
| Its own home and slot state/run folders (Code: the slot home is a write grant) | yes | yes |
| System read paths (`/usr`, `/etc`, ...) | yes (read-only) | no |
| Browser profiles, browser sockets, app state, `<app>/.env` | no (not granted; Unix permissions also deny) | no |
| Another user's tree | no (o-rwx; not granted) | no |
| `<app>/releases` | search only (0711): it can reach the launcher by name, cannot list | no |
| Anything world-readable outside its grants | no (Landlock) | no |

Limits of the rule: it removes grants that lie INSIDE a private root. A grant that CONTAINS one (say the whole docs root, which
holds `<docs>/tmp/.agent-browser`) is not narrowed (Landlock cannot carve a subtree out without the hidden-path mounts, which would
refuse the command on a host without namespaces); there the private tree stays closed by its Unix permissions alone (app-owned
0700). Observation for review, not changed here: `provision-slots.sh shared` puts `<docs>/tmp` (recursively) in the `<prefix>shared`
group with g+rwX, so browser socket folders that existed under `<docs>/tmp/.agent-browser` when it ran are group-accessible to every
slot; no slot shell grant covers `<docs>/tmp` today.

Proof (container test, real accounts, real sudo/slotctl/launcher): `TestRealSlotChain` runs as the service account through the real
chain as `slot01` in a Crew project whose browser profile is 0700: `pwd` and reading its own file work; `cat <profile>/Cookies`,
`ls <profile>`, `ls`/`cat` in another user's Crew, `cat <app>/.env`, and `cat` of a world-readable file OUTSIDE the grants all fail
(the last one only Landlock can stop, so the command ran confined). The launcher step shows the old launcher refusing with the exact
RTS error and the new one starting the same policy, skipping the grant, with the cookie still unreadable.

## Done (on main, not deployed)

- **Layer 3, code (workspace module).** `security/slot_grants.go` (rule + `grantStatSkippable`), `security/isolator.go`
  (`scopeBrowser`: nothing for a slot), `security/isolator_linux.go` (filter, browser scoped for slots),
  `security/landlock_runner_linux.go` (`addPolicyPathRule`), `security/landlock_policy.go` (doc). agent_go unchanged: it still
  names the session's browser; the workspace, which knows the slot, decides.
- **Layer 2, deploy.** `deploy/common/slots.sh` `slots_release_traversal`: on slot hosts `releases/` 0711, the new release and its
  `bin/` o+x, the launcher 0755, every deploy, idempotent, only folders the service owns. Called by both
  `deploy/rootless-linux/build-and-activate.sh` and `deploy/aws-ec2/server/build-and-activate.sh` before activation.
  `allowed_exec` (`<app>/releases/*/bin/video-studio-landlock-runner`) already names the file; the self-test checks it matches the
  launcher in use (`slots.ExecConfig.AllowsProgram`, the same rule slotctl uses).
- **Deploy self-test** (`workspace/slotcheck`, `workspace/cmd/slotcheck` built into every release by `slots_build`,
  `deploy/common/slotcheck.sh` copied into every release, `deploy/common/admission_scan.py`). Run as the service account at the end
  of every deploy of a slot host (non-zero exit, loud, no rollback) and alone with `./deploy.sh slotcheck <rts|excellence|confida|sparkquill>`.
  It reads the service's slot settings from the running workspace process (else `.env`; slot keys only), then checks:
  `slotctl-config` (readable), `slotctl-docs-root`, `slotctl-allowed-cwd` (covers the docs root, PLAT-476), `slotctl-allowed-exec`
  (lists the launcher), `slot-table` (owner root, the service's group, 0640, readable: the root:root incident), `runner-reachable`
  per slot (every folder from / to the launcher traversable for that slot by owner/group/other bits), and per slot a real `pwd`
  through `security.Isolator` (the shell tool's grant builder, with a workflow or project browser session like a real chat; the
  folder is granted read-only, so the shell tool sets up no `.sandbox-cache` in it) +
  sudo + slotctl + the launcher in `pwd-docs-root`, `pwd-workflow` (first `Workflow/<x>`), `pwd-crew-project` and
  `pwd-code-project` (the slot's user's first project; skipped when the user has none; Code carries the slot home like the handler).
  Slots: every assigned slot (only `pwd`, only in its own user's folders and shared folders it is granted) plus one TEST slot, the
  highest-numbered unassigned slot account (it holds no user's data; its Crew/Code shapes run in its own `slots/state/<slot>`).
  Output: a PASS/FAIL table, then one `FAIL <check> [slot]: <what> -- fix: <how>` line per failure; slot names and counts only,
  user folders shown as `<docs>/<user>/...`. Read-only: it changes no mode, owner, table or config; the only files are the
  per-command scratch the shell tool itself creates and removes (and, on a host whose service never started its sandbox, the empty
  browser profile roots the sandbox probe creates).
- **Secret admission scan** (WARN section, never fails): `deploy/common/admission_scan.py` (named so the `*secret*` ignore rule does
  not drop it). Names only: a manifest selecting a secret that exists nowhere (same rule as `migrate-secret-selections`), and a
  selected shared secret without a Platform group grant (read with the agent's Vault service credential through
  `GET /api/admin/groups/<platform id>/secrets`; skipped with an INFO line when Vault is not configured). User ids are shown as `<user>`.
- **Tests.** `security/slot_grants_test.go` (no browser folder for a slot, nothing created, service account keeps its own; app-private
  filter; only EACCES is skippable), `security/slot_grants_linux_test.go` (the slot policy has no profile and is browser scoped),
  `slotcheck/slotcheck_test.go` (fake chain on fake trees: the fixed layout passes 16/16; each RTS failure is detected with a fix:
  allowed_cwd without docs, releases 0700, the launcher failing on the profile, table group/mode/owner, missing config, allowed_exec,
  unreadable table; no slot assigned runs only the test slot; an assigned slot runs only in its user's folders; user ids never
  printed), `deploy/common/test_slot_selfcheck.py` (traversal on the RTS 0700 layout and Hetzner 775, idempotent, untouched without
  slots; both activation scripts wired; the wrapper passes only slot keys and propagates failure; the scan prints names only),
  `slotcheck/container_e2e.sh` + `slotcheck/chain_linux_test.go` (Linux container, below).

### Container test (golang:1.26-bookworm, privileged, kernel 7.0 with Landlock; run 2026-10-04)

`docker run --rm --privileged -v "$PWD/workspace:/src/new:ro" -v "$PWD/deploy/common:/src/deploy:ro" -v <origin/main workspace>:/src/old:ro -v "$(go env GOMODCACHE):/go/pkg/mod" golang:1.26-bookworm bash /src/new/slotcheck/container_e2e.sh`

```
old launcher: SANDBOX_UNAVAILABLE: inspect Landlock path: stat /srv/vs/state/browser-profile-projects/project-3bdc30503fa28a4f--browser: permission denied
new launcher: SANDBOX_GRANT_SKIPPED: /srv/vs/state/browser-profile-projects/project-3bdc30503fa28a4f--browser is not accessible to this account; it is not granted
OK   self-test passes on the fixed layout (exit 0)          slot self-test: 21 passed, 0 failed, 0 skipped
OK   self-test fails when releases/ is 0700 (exit 1)        FAIL runner-reachable [slot01]: /srv/vs/releases is not traversable for slot01 -- fix: chmod 0711 /srv/vs/releases ...
OK   no .sandbox-cache created by the self-test
OK   TestRealSlotChain (exit 0)
OK   deploy wrapper passes and prints no secret value
OK   no profile folder created
OK   self-test fails on the old code (exit 1)               FAIL pwd-crew-project [slot01]: <docs>/<user>/Chats/Work/projects/u1crew: SANDBOX_UNAVAILABLE: inspect Landlock path: stat /srv/vs/state/browser-profile-projects/project-...--browser: permission denied -- fix: PLAT-478: deploy a release whose launcher skips ...
container e2e: 0 failure(s)
```

Fixed layout, as printed by the self-test there:

```
      CHECK                 SLOT     DETAIL
PASS  slotctl-config        -        /usr/local/libexec/agentworks/slotctl.json readable
PASS  slotctl-docs-root     -        docs_root = /srv/vs/data/docs
PASS  slotctl-allowed-cwd   -        allowed_cwd covers the docs root
PASS  slotctl-allowed-exec  -        allowed_exec lists /srv/vs/releases/r1/bin/video-studio-landlock-runner
PASS  slot-table            -        /etc/agentworks/slots.json root-owned, service group, 0640, readable
PASS  slot-table-count      -        2 assigned slot(s); test slot slot50
PASS  runner-reachable      slot01   every folder down to the launcher is traversable
PASS  pwd-docs-root         slot01   <docs>
PASS  pwd-workflow          slot01   <docs>/Workflow/wf1
PASS  pwd-crew-project      slot01   <docs>/<user>/Chats/Work/projects/u1crew
PASS  pwd-code-project      slot01   <docs>/<user>/Chats/Code/projects/u1code
...   (slot02, and the test slot slot50 in /srv/vs/slots/state/slot50)
slot self-test: 21 passed, 0 failed, 0 skipped
```

## How to run the self-test

- Every deploy of a slot host runs it (`./deploy.sh rts`, `excellence`, `confida`; the end of the output). A failure exits 1 after
  activation; the release stays live.
- Alone, read-only: `./deploy.sh slotcheck rts` (or `excellence`, `confida`, `sparkquill`). On the server, as the service account:
  `bash <app>/current/slotcheck.sh --app <app> --docs <docs root> --product <product>`.

## Rollout

RTS (coordinator; the self-test needs this release):
1. Check nothing is running on RTS, then `./deploy.sh rts`. The end of the output must show `slot self-test: N passed, 0 failed`
   (slots 02-07 assigned plus test slot, e.g. slot50). The deploy sets `releases/` to 0711 itself (it was set live to 711 already).
2. If anything FAILs: fix as the FAIL line says (root through `deploy/aws-ec2/slots-admin.sh` / SSM), then `./deploy.sh slotcheck rts`.
3. Re-assign the admin: `deploy/aws-ec2/slots-admin.sh assign aa73da63e26b40a1bb701c2b4c024870 slot01`, then
   `deploy/aws-ec2/slots-admin.sh status` (table still `root:video-studio` 0640) and `./deploy.sh slotcheck rts`: slot01 now has its
   own pwd rows (docs, workflow, Crew, Code). Then a Crew chat as the admin: the workspace shell tool runs `pwd` and `id -un`
   (prints slot01), with no `SANDBOX_UNAVAILABLE`.
Excellence (after RTS is clean):
1. `./deploy.sh excellence`; the self-test runs at the end for the assigned slot(s) and the test slot (`SLOTS_ENABLED=true`).
2. A Crew/Code project chat of the canary user (`AGENTWORKS_SLOT_CLI_USERS`): the shell tool's `pwd` works.
Confida: its next deploy runs the same self-test (prefix `cf`, its own config and table paths come from its service environment).

## Left

1. Deploy (above) and record the self-test output of RTS and Excellence here.
2. Define "stable" (self-test passes on every slot host; Crew, Code and workflow shell each run as a slot; PLAT-457 closed) before
   assigning slots anywhere new; decide whether to keep the other 6 RTS slots meanwhile (with this release they work again).
3. Not verified: a real server (the container uses the same programs and the provision-slots layout, not RTS's EC2/AppArmor);
   hosts where the private /tmp is unavailable (the service scratch folder `/tmp/aws-<uid>/c-*` is then skipped with a
   `SANDBOX_GRANT_SKIPPED` line on every slot command instead of failing it); ACLs (the hosts use none; `runner-reachable` reads
   mode bits only); the Vault grant read of the scan against a live gateway.

## History of this ticket's open items (before the fix)

1. Fix layer 3 in code: the launcher (`workspace/cmd/landlock-runner`, `workspace/security`) must not fail the whole command on a grant path the slot user cannot see; skipping that grant (granting nothing) is the safe default. Or do not grant the browser profile to slot shell commands at all. Tests on both servers.
2. Put layer 2 in the deploy scripts (releases/ 0711 on slot hosts).
3. Deploy self-test on every slot-enabled server: per assigned slot, a real slotted `pwd` in the docs tree, in a workflow folder and in a Crew/Code project folder; fail the deploy loudly.
4. Define "stable" (self-test passes; Crew, Code and workflow shell each run as a slot; PLAT-457 closed) before assigning slots anywhere new; decide whether to release the other 6 RTS slots meanwhile.

## Register notes

[PLAT-478](plat-478.md), P1, fixed on main 2026-10-04, not deployed. Three stacked failures (allowed_cwd, releases/ 0700, Landlock launcher fails closed on an unreadable browser-profile grant). Fix: slot commands never get app-private paths (browser profiles/sockets, app state), the launcher skips an un-stat-able grant, deploys set releases/ 0711 and run a read-only slot self-test (`./deploy.sh slotcheck <server>`). RTS admin slot still released until the deploy's self-test passes.
