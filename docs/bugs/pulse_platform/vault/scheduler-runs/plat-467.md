# PLAT-467 — The Vault systemd unit quoted paths, so it never started (first Vault install, Excellence 2026-10-04)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | vault |
| Area | scheduler-runs |
| Summary | fixed on main; Excellence redeploy pending. |

Status: fixed on main; Excellence redeploy pending, Confida and RTS not yet deployed with it.

## What was wrong

`deploy/common/install-vault-service.py` wrote `EnvironmentFile="<state>/service.env"` and `WorkingDirectory="<app>/current"` with quotes, and the agent drop-in
`zz-vault.conf` the same for `EnvironmentFile`. systemd reads quotes in those two settings as part of the path ("path is not absolute"), so `agents-vault.service` had a
fatal configuration error and `systemctl restart` failed, which ended the whole Excellence deploy with exit 1 right after it had activated the new release
(agent, workspace and gateway were already up on the new release; only Vault was down; the drop-in's quoted `EnvironmentFile` was merely ignored, so the agent lacked
the Vault variables). `ExecStart=` may be quoted and still is.

## Fix

The two paths are written unquoted. The installer already refuses paths with quotes, newlines, `%`, `$`, backslash or a backtick, so an unquoted path is safe.
Test `test_unit_paths_are_unquoted_where_systemd_takes_quotes_literally` checks both files, and runs `systemd-analyze --user verify` when it is available (the box).

## Left

- Redeploy Excellence with the fix (a rebuild: the installer is bundled into the release), then check `agents-vault` is active and `/healthz` answers.
- Same check on Confida (needs `VAULT_ENABLED=true`) and RTS before their first Vault install.

## Register notes

[PLAT-467](plat-467.md), fixed on main; Excellence redeploy pending. `EnvironmentFile=` and `WorkingDirectory=` were written with quotes, which
systemd takes literally, so the first Vault install failed and ended the Excellence deploy (new release was up, Vault was not).
