# PLAT-480 — Slot sandbox hardening: tmux/Docker reachable from confined commands, release trees readable, state-root grant filter

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | open, P1. |

Status: open, P1. From the security review of PLAT-478 (commit 1242a74b2) and two hardening attempts on 2026-10-04 (both stopped early; no code merged).
Design work should be planned and reviewed with the owner before implementation.

## Findings (from code review; no exploit reproduced, by choice)

1. **H1, slot tmux server outside the sandbox.** `slotctl` allows `/usr/bin/tmux` and `slottmux` starts the slot's tmux server as the slot, not under the Landlock
   launcher; its socket is `<SlotRunRoot>/<slot>/tmux.sock` (0660, group slotNN). Landlock (incl. ABI 8 on kernel 7.0) does not restrict path lookup, stat or connect() on
   pathname Unix sockets, so path rules cannot hide it. Impact: a confined slot command could act with the slot's full Unix permissions (all `<prefix>shared`
   folders: Workflow/, Downloads, skills, subagents, `<docs>/tmp`; anything world-readable). Not: other users' private trees, app state, secrets.
   Fix direction: in the launcher's own mount namespace, mount an empty mode-000 placeholder over `<SlotRunRoot>` and `/run/user` (the private-/tmp mechanism),
   refuse slot commands when namespaces are unavailable; on ABI >= 6 also scope abstract sockets and signals and log the ABI.
2. **H1b, CLI-as-slot inherits the unconfined tmux.** Coding CLIs run as a slot inside the slot's tmux server; their Landlock policy
   (multi-llm-provider-go `internal/clisandbox/landlock.go`) has no private namespaces and the panes inherit `$TMUX`. Needs the launcher's own namespaces or a confined tmux server.
3. **Slot Docker.** `slot_docker` (off on all hosts today) gives the slot an unconfined Docker; keep it off until it is confined (needs an owner decision to enable).
4. **H2, release trees readable.** RTS: `releases/` 0711 (set live 2026-10-04 so slots can exec the launcher); slots can read the current release (public frontend,
   playbooks, systemd units, migration files; no source, no secrets found). Hetzner: Excellence `releases/` 0775, current release 0775; Confida current release 0775;
   ~9 non-AgentWorks accounts on that box can read both releases (source of the three public repos, frontend, binaries; no secret values found).
   Fix: releases readable by the service account only on every host; a root-owned, hash-verified copy of the launcher and `mcpbridge` beside slotctl
   (needs a root step per release: SSM on RTS, root on Hetzner); self-test rows for release readability and for ACL entries (provider `slotfs.traverse` adds `setfacl u:slotNN:--x`).
5. **H3, grant filter.** The slot filter drops all grants under the state root where it does not contain the docs root; CLI runtimes live under `<state>/cli-runtimes/v1`.
   Replace with an explicit deny list of the state root's private entries (ownership, migrations, structured-chat-events*.sqlite, instruction-snapshots, gmail-inbound, auth, personal-mcp, ...).
6. **H4-H6, self-test and scan.** Workflow probe only for the test slot; per-component, no-symlink project-root resolution; `admission_scan.py` O_NOFOLLOW + regular files +
   containment + size cap + timeout; make preflight/private-/tmp probes browser scoped so no empty profile roots are created.

## Done elsewhere

- RTS `<app>/logs` was world-readable (`drwxr-xr-x`, agent.log 0664): set to 0750 live 2026-10-04; Hetzner products already 0750. **The RTS deploy of 2026-10-04 (c35e552) reset it to drwxr-xr-x**, so it was set to 0750 again by hand; the deploy must enforce 0750 (open item H2).
- Partial, uncommitted, untested edits from the second attempt are in the worktree `/Users/mipl/ai-work/mabg-slotharden` (root-owned program check in slotctl,
  installed-copy lookup, no DOCKER_HOST for slot commands). Not merged: alone they break slot commands until the root install step exists.

## Host difference that constrains the F1/H1 design (found 2026-10-04)

RTS (Ubuntu, kernel 7.0 aws) has `kernel.apparmor_restrict_unprivileged_userns = 1`: an unprivileged process cannot make a mount namespace. The workspace's startup probe logs
`[SANDBOX] private /tmp unavailable, commands see the host /tmp: exit status 125: SANDBOX_UNAVAILABLE: make mounts private: permission denied` on every service start there
(10/03 x3, 10/04 15:05 and 16:49; one per workspace restart), i.e. RTS has run without the private-/tmp namespace all along (commands still get no /tmp grant beyond a small browser temp
dir, so Landlock keeps other users' temp files closed). The Hetzner box has the sysctl at 0 (kernel 6.8) and never logs the warning: private /tmp works there.
Consequence: an H1 fix that relies on a mount namespace (placeholder mount over the slot run root, /run/user) works on Hetzner but NOT on RTS unless the launcher gets an AppArmor
profile exception (a root step) or the design avoids namespaces (e.g. a socket location/ownership no confined slot command can reach). The design must cover both hosts.

## Confinement checks in the deploy self-test (built 2026-10-04)

`slotcheck` now also proves, per slot, that a slotted command started with only its own project granted is REFUSED when it tries to create a file in the docs root, list `_users`, list `releases/`, or list the app `state/` folder (`deny-write-outside`, `deny-list-users`, `deny-list-releases`, `deny-list-state`). Each command prints `started` first and its exit code last, so a launcher that failed to start is a FAIL, never a "refusal". Unit tests cover pass, a leak, and a launcher that does not start. Live runs by the owner on RTS (Crew and Code chats as slot01, 2026-10-04) agreed: all refused. Attached Crews are shared read-write by design (server.go "Crew references ... shared read-write"), so a Code chat can reach a Crew attached to it; no other Crew was reachable. Not yet run on a host: the first deploy that carries it will show the four rows.

## F1 confirmed live on RTS (2026-10-04, 17:14 UTC)

Probe by the owner, read-only, from the Crew `gptlive1` chat (workspace shell tool, confined as slot01): the slot's tmux control socket `<app>/slots/run/slot01/tmux.sock` (owner slot01, mode 0600/0660, parent `slots/run` 0711) is visible and a `tmux -S <socket> list-sessions` from inside the sandbox connected to a LIVE unconfined server and listed its session. Nothing was sent to the session and nothing attached. The test server was started by the operator through `sudo -u slot01 slotctl exec` with `SHELL=/bin/sh` (the slot accounts' login shell is `/usr/sbin/nologin`, so a session whose command runs through the default shell exits at once and the server with it) and was stopped afterwards with `kill-server`.
Scope: same-user only. Other slots cannot connect (different uid, mode 0600/0660); the exposure is confined-to-unconfined within ONE slot, i.e. Landlock's file rules are bypassable for slot01's own Unix permissions (not for other users' files or the app's private state). Still the confinement the slots work exists to provide, so it needs the fix (design must cover RTS, where mount namespaces are blocked by AppArmor, see above).

## F1 fix, stage 1: workspace shell tool (built 2026-10-04, not deployed)

Neither host has the Landlock right for connect() on pathname sockets (RTS kernel 7.0 = ABI 8, Hetzner 6.8 = ABI 4; the right is ABI 9), so the socket folder is hidden instead:
- `workspace/security/isolator_linux.go`: a slot command's policy now hides the slot run root (slotctl `slot_run_root`) with the launcher's existing HiddenPaths mount (an empty mode-000 folder over it, in the command's own user+mount namespaces). If the host cannot give the command those namespaces the command is REFUSED (`SANDBOX_UNAVAILABLE`), never run with the socket in view.
- RTS: AppArmor restricted unprivileged user namespaces, so the launcher could not mount (the `private /tmp unavailable ... make mounts private: permission denied` line on every RTS start: slot commands never had a private /tmp or hidden paths there). New `provision-slots.sh userns` installs a path-scoped `userns` exception for `slotctl` and the release's `video-studio-landlock-runner` (no-op where the sysctl is 0). Root step on RTS: `deploy/aws-ec2/slots-admin.sh userns`.
- Self-test: new row `deny-slot-run-root` (the run root must look empty from a slot command).
- Linux container e2e (`workspace/slotcheck/container_e2e.sh`, step 5a): a real tmux server for slot01; control = the launcher alone still reaches it (the hole reproduced), the real chain does not. 0 failures; self-test 36 passed.
Open (stage 2): the coding CLIs' own launches (multi-llm-provider-go `internal/clisandbox/landlock.go`) build their own policy without private_tmp/hidden_paths/namespaces, so a CLI running as a slot (and the commands it runs) still sees the socket. Same hide to be added there.

### F1 stage 1 follow-up: the first deploy broke the Code terminal and the output helpers (2026-10-04, found by the owner's test)

Deployed 4f13fcb on RTS at 17:43: tmux socket hidden (owner's T1-T3 all blocked) but the slot run folder is also where (a) the Code terminal's tmux server keeps its socket (`<run>/<slot>/shells/<id>`, started INSIDE the sandbox) and (b) the output/database helper files for Python live (`output-helper-*`, read grant). Hiding the whole folder with an empty placeholder made the launcher skip the helper grant (`SANDBOX_GRANT_SKIPPED ... output-helper-...`, the notice the owner's agent flagged) and would have stopped the terminal from creating its socket. Fix: `LandlockPolicy.PrivateRoots` replaces the placeholder: an empty tmpfs over the run root with only the policy paths granted inside it bound back (same mechanism as the private /tmp: `maskDir`). New e2e subtests: a granted folder inside the run root still works, another shell's folder and the slot socket are not visible, the output helper is importable. Earlier rollback: RTS was rolled back 17:39 to c35e552 once (self-test had failed), then redeployed 17:43 after the slotcheck exception was added.

### F1 stage 1: deployed and verified on RTS (2026-10-04 18:00 UTC, release 4441bb0)

Owner's live tests, all as slot01: Crew chat T1/T2 with a LIVE tmux server on the slot socket (started by the operator; server confirmed alive on the host at 18:00:53): socket not visible, connect fails ("No such file or directory"), no session listed. `import agentworks_output, agentworks_db` works. Code terminal tab: `pwd` = the project folder, `whoami` = slot01. Deploy self-test: 44 passed, 0 failed (new rows `deny-slot-run-root`, `deny-write-outside`, `deny-list-users`, `deny-list-releases`, `deny-list-state`). The test tmux server was stopped. Hetzner hosts (Excellence, Confida) have NOT received this; they need the same deploy and have no AppArmor restriction (sysctl 0, kernel 6.8).
Observed, not yet explained: `[SANDBOX] private /tmp unavailable ... make mounts private: permission denied` is still logged in agent.log once per agent start, at the first chat launch (17:46:09 and 18:00:20 coincide with the owner's Crew runs); never in workspace.log. Slot shell commands (workspace process) do get the namespaces, so this is another process's launch (probably the agent process starting a CLI, whose binary has no userns exception). Belongs with stage 2 (the CLI launches).
Still open under F1: stage 2 (coding CLI launches: multi-llm-provider-go internal/clisandbox/landlock.go has no private_tmp/private_roots/namespaces), Docker socket (`/run/user/<uid>/docker.sock`), F2 releases/ readability, deploy resets `<app>/logs` to 0755.

### Docker socket (owner decision 2026-10-04): accepted, not fixed

Owner: users need Docker in Code and run things through it, so slot commands keep their slot's rootless Docker socket (`/run/user/<uid>/docker.sock`). Correction, same day: slot Docker is NOT provisioned on any host yet (the 8 sockets seen on the Hetzner box belong to non-slot accounts, the one on RTS is the app account's own). The owner wants it on every host; RTS has the packages (docker 29.7, rootlesskit, uidmap, subuid for 50 slots) and 3.8 GB RAM / 2 vCPU, so enable per assigned slot only. Recorded in DECISIONS (same date) with the consequence: on a Docker-enabled host the confinement of a slot command is the slot account's Unix permissions + rootless user namespace, not Landlock. The F1 tmux fix is unaffected and still valuable (it is what keeps the Landlock-confined agent inside Landlock on hosts without Docker, and keeps the terminal/other shells apart). Remaining F1 work: stage 2 (coding CLI launches). F2 (release folders) stays open: Excellence `/srv/agents/releases` is 0775 and slot accounts can read it (public code only).

### Deploy flag for the extended security checks (built 2026-10-04; owner asked for the hand tests to be automatic)

`slotcheck --level full` (default for `DEPLOY_SECURITY_CHECKS`, `basic` skips it: `DEPLOY_SECURITY_CHECKS=basic ./deploy.sh confida`; standalone `./deploy.sh slotcheck <server> [basic|full]`, default full) adds, on the unassigned TEST slot only (no user's slot or chat is touched): `tmux-socket-unreachable` (a real tmux server started as the test slot through slotctl, proven alive from outside, then a confined command must fail to reach it; the server is always killed), `slot-python-helpers` (output + database helpers importable), and the workflow-chat (app account) refusals `wf-write-outside`, `wf-read-other-workflow`, `wf-list-users`, `wf-list-state`, `wf-read-env`, `wf-list-shared-secrets`, `wf-app-tmux-socket`. Each command prints `started` first so a launcher that did not start is a FAIL. Linux container e2e (`workspace/slotcheck/container_e2e.sh` step 2b): all rows pass, including `wf-read-env` with the file made world-readable (so the sandbox, not file mode, refuses it); one find fixed on the way: the workflow probe must not carry a BrowserSession (the sandbox would create the workflow's browser profile folder; the self-test changes nothing).
Not automated (still by hand, need a real chat, LLM or login): the Code terminal tab, a Crew/Code chat with its real selected secrets, a real workflow run. Candidate next level: drive the shell tool endpoint with a test user over the API.


### Excellence deployed (2026-10-05 20:31 UTC, release agents-85c97836, main 85c978361)

First deploy with the F1 fix and the full self-test on a Hetzner host: slot self-test 133 passed, 0 failed, 11 skipped (no AppArmor exception needed: sysctl 0, kernel 6.8). All extended rows passed (`tmux-socket-unreachable` on the test slot, `slot-python-helpers`, `wf-write-outside`, `wf-list-users`, `wf-list-state`, `wf-read-env`, `wf-list-shared-secrets`, `wf-app-tmux-socket`) and every `deny-*` row for slots 01-12. `releases/` went from 0775 to 0711 (F2 for Excellence closed by the deploy's slots_release_traversal), `logs/` 0750, slot Docker on for all 12 slots (provisioned before the deploy). No chat turns, tmux sessions or CLI processes were running at deploy time. Services active, site 200, no errors. Leftovers not touched: two old failed deploy job units under the agents user (`agents-deploy-20261001181304-72506`, `agents-deploy-20261004164304-5381`). Secret admission warning (names only): MISTRAL_API_KEY selected by one manifest, no Platform grant. Confida is not deployed yet.

## Todo (as of 2026-10-05, owner asked for it)

State today: F1 (tmux) fixed and deployed on RTS (4441bb0) and Excellence (agents-85c97836); full self-test default on every deploy; slot Docker on for all slots on RTS (7), Excellence (12), Confida (13).

1. **Agentic checks as Manish on Excellence (owner: "do that later").** Add the MCP connection (`! claude mcp add --transport http agentworks-excellence https://agents.excellencetechnologies.in/api/external/v1/mcp`, then `/mcp` login); pick or create a test Crew (`smoke-test`) and workflow; run the paste sets as chats (Crew shell, GitHub/Notion/secret names, Docker D1-D3, Code terminal, workflow-chat W1-W11, a workflow run); then an opt-in deploy flag (`DEPLOY_AGENTIC_CHECKS=1`) with a dedicated smoke user and one retry; assert on tool results, not model wording.
2. **Confida**: deploy current main (the Vault owner normally deploys it; check nothing is running first), read the first full self-test, compare with Excellence.
3. **Stage 2 of F1 (deprioritized by the owner 2026-10-05, "ok" to the recommendation; revisit only if Docker for slots is ever restricted, because a slot command with Docker already leaves the Landlock rules the same way tmux did)**: coding CLI launches (multi-llm-provider-go `internal/clisandbox/landlock.go`) have no private_tmp / private_roots / namespaces; the agent process also lacks the userns exception on RTS (the "private /tmp unavailable" line at each chat start). Same hide for the CLI and its commands.
4. **F2 release readability**: Excellence closed (0711 by the deploy). Left: launcher outside the release tree (root-owned, hash-verified) so `releases/` can be 0700.
5. **RTS deploy resets `<app>/logs` to 0755**: enforce 0750 in the deploy (set by hand each time so far).
6. **PLAT-485 (frontend)**: dead session id after a server restart and the raw 409; rotate to a fresh session or offer it.
7. **Docker**: watch RTS memory (about 70 MB per idle daemon, 1.4 GB available of 3.8 GB); optional later trial of rootless Podman where it works (keeps Landlock confinement, but no_new_privs blocks newuidmap).
8. **Excellence leftovers**: two old failed deploy units under the agents user; `MISTRAL_API_KEY` selected by a manifest without a Platform grant.
9. **Other open tickets**: PLAT-457 (P1, symlink in a user's tree bypasses the proxy gates), close issues #266 and #269 and update PLAT-469 when the owner confirms, Muse restart on the Mac (PLAT-468), local-app Cmd+K chat bounce, a script or SSM to stop the RTS firewall IP churn.
10. **Housekeeping**: remove my worktrees (`mabg-terminal-fix`; `mabg-slotharden` holds partial, unpushed hardening edits: do not push as-is) and fast-forward the three main checkouts when the work is finished (AGENTS.md rule 5).

### Agentic checks as Manish on Excellence, via the MCP connection (2026-10-05, first run)

Connected `agentworks-excellence` (OAuth, as Manish; the page needed PLAT-487 first), created the scratch Crew `smoke-test` (owned by Manish, no schedules/functions/secrets) with `create_crew`, and sent messages with `ask_crew`. The Crew's model came back empty from `create_crew` but a default was used, so a turn ran. Results through the workspace shell tool (`execute_shell_command`), release agents-f429a8cc: `id -un` = slot03, cwd = the Crew's project folder; refused: write in the docs root, list `_users`, list `state/`, read `.env`; `slots/run` empty; tmux on the slot socket: No such file; Docker works (rootless); 0 SECRET_ names (none selected); network 200. All PASS.
Finding to track, not a defect by decision: when asked only to "use the workspace shell tool" the CLI used its BUILT-IN shell, which ran as the app account `agents` in `state/cli-runtimes/v1/<hash>` (PLAT-446: Crew turns run as the app account; Landlock confines it). The confinement of that native shell was not probed. Naming `execute_shell_command` explicitly routes to the slot. The MCP cannot set a Crew's model or list provider accounts (no field in create_crew/update_crew, no listing tool); ticket as a feature if wanted.

## Register notes

[PLAT-480](plat-480.md), open, P1. Follow-up of the PLAT-478 security review; design to be agreed with the owner before implementation.
