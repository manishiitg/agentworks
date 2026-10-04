# PLAT-480 — Slot sandbox hardening: tmux/Docker reachable from confined commands, release trees readable, state-root grant filter

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

