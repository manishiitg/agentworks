# PLAT-478 — Slot shell commands never worked end to end on RTS; slots are canary-stage, not production-ready

Status: open, P1. RTS mitigated 2026-10-04 (the admin account's slot released). Found 2026-10-04 when a Crew on RTS first used the workspace shell tool.

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

## Left

1. Fix layer 3 in code: the launcher (`workspace/cmd/landlock-runner`, `workspace/security`) must not fail the whole command on a grant path the slot user cannot see; skipping that grant (granting nothing) is the safe default. Or do not grant the browser profile to slot shell commands at all. Tests on both servers.
2. Put layer 2 in the deploy scripts (releases/ 0711 on slot hosts).
3. Deploy self-test on every slot-enabled server: per assigned slot, a real slotted `pwd` in the docs tree, in a workflow folder and in a Crew/Code project folder; fail the deploy loudly.
4. Define "stable" (self-test passes; Crew, Code and workflow shell each run as a slot; PLAT-457 closed) before assigning slots anywhere new; decide whether to release the other 6 RTS slots meanwhile.
