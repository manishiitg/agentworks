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

