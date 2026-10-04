# PLAT-476 — slotctl refuses every command that starts in the docs tree on RTS ("the working folder is outside the allowed folders")

Status: script fixed on main; the RTS config file is NOT yet corrected (needs root on RTS); not deployed.

## What was wrong

`deploy/common/provision-slots.sh` writes the slot helper's config (`/usr/local/libexec/agentworks/slotctl.json`) with `"docs_root": "$DOCS"` but `"allowed_cwd": ["$HOME_DIR/data/docs", "$HOME_DIR/slots"]`.
The two agree only where the docs live under the app folder (Excellence, Confida, SparkQuill). On RTS `DOCS=/data/video-studio/docs` while `HOME_DIR=/var/lib/video-studio/video-studio`,
so `allowed_cwd` names `/var/lib/video-studio/video-studio/data/docs`, which does not exist there. `slotctl` (`workspace/slots/exec_linux.go`) resolves the request's working folder and requires it to be
inside one of `allowed_cwd`, so every slotted command whose folder is in the docs tree is refused with exit 126: `slotctl: refused: the working folder is outside the allowed folders`.
Seen on RTS 2026-10-04 in the Crew `gptlive1` (admin account holds `slot01`): the workspace shell tool failed, the agent fell back to Claude Code's native Bash, which
runs in "don't ask" mode and refuses network commands (GitHub, Notion), so the Crew could not do its work. Not caused by PLAT-435/442/451 (the check and the file date from 2026-10-01).

## Fix

`allowed_cwd` now lists `$DOCS` (the same variable as `docs_root`). Shared hosts keep the default `DOCS=$HOME_DIR/data/docs`, so their config is unchanged. Test `deploy/common/test_provision_slots_config.py`.

## Left

Correct the live RTS file (root-owned, written by the SSM channel `deploy/aws-ec2/slots-admin.sh`): add `/data/video-studio/docs` to `allowed_cwd` (back it up first). slotctl reads the file on each call, so no restart is needed.
Then check Excellence/Confida's `slotctl.json` for the same mismatch (their `docs_root` and `allowed_cwd` should agree).
