# PLAT-476 — slotctl refuses every command that starts in the docs tree on RTS ("the working folder is outside the allowed folders")

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | script fixed on main; live RTS config still to be corrected (root). |

Status: script fixed on main; the live RTS config corrected 2026-10-04 (root, via SSM); Excellence and Confida checked and fine.

## What was wrong

`deploy/common/provision-slots.sh` writes the slot helper's config (`/usr/local/libexec/agentworks/slotctl.json`) with `"docs_root": "$DOCS"` but `"allowed_cwd": ["$HOME_DIR/data/docs", "$HOME_DIR/slots"]`.
The two agree only where the docs live under the app folder (Excellence, Confida, SparkQuill). On RTS `DOCS=/data/video-studio/docs` while `HOME_DIR=/var/lib/video-studio/video-studio`,
so `allowed_cwd` names `/var/lib/video-studio/video-studio/data/docs`, which does not exist there. `slotctl` (`workspace/slots/exec_linux.go`) resolves the request's working folder and requires it to be
inside one of `allowed_cwd`, so every slotted command whose folder is in the docs tree is refused with exit 126: `slotctl: refused: the working folder is outside the allowed folders`.
Seen on RTS 2026-10-04 in the Crew `gptlive1` (admin account holds `slot01`): the workspace shell tool failed, the agent fell back to Claude Code's native Bash, which
runs in "don't ask" mode and refuses network commands (GitHub, Notion), so the Crew could not do its work. Not caused by PLAT-435/442/451 (the check and the file date from 2026-10-01).

## Fix

`allowed_cwd` now lists `$DOCS` (the same variable as `docs_root`). Shared hosts keep the default `DOCS=$HOME_DIR/data/docs`, so their config is unchanged. Test `deploy/common/test_provision_slots_config.py`.

## Done on RTS (2026-10-04, owner approved)

Through `aws ssm send-command` (the repo's admin channel): `/data/video-studio/docs` appended to `allowed_cwd` in `/usr/local/libexec/agentworks/slotctl.json`, backup beside it as
`slotctl.json.bak-20261004-plat476`, atomic replace, owner root and mode 0644 unchanged, no restart (slotctl reads the file on each call). Excellence and Confida were read: their `allowed_cwd`
already contains their docs root (it sits under the app folder), so only RTS had the mismatch. A stray older `/etc/agentworks/slotctl.json` on the Hetzner box has no `docs_root`; slotctl reads the file beside its launcher, so it is not in use.

## Left

Confirm in the Crew (a `pwd` through the workspace shell tool must succeed). To undo: copy the `.bak-20261004-plat476` file back over `slotctl.json` (root).

## Register notes

[PLAT-476](plat-476.md), script fixed on main; live RTS config still to be corrected (root). `provision-slots.sh` built `allowed_cwd` from the app folder while the docs live at `/data/video-studio/docs`, so the slot shell tool failed with exit 126 and the Crew fell back to a no-ask native shell.
