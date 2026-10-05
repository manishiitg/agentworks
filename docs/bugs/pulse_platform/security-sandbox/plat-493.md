[<- Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-493 - The desktop app (DMG) ran with no Vault

| Coordination | Value |
|---|---|
| State | fixed on main (not in a released DMG yet) |
| Priority | P2 |
| Owner | security-sandbox |

## Problem

`desktop/` bundled only `agent-server` and `workspace-server`. With no gateway, `CAPLAYER_SERVICE_URL` was unset and the backend ran with no Vault. Owner decision 2026-10-05: Vault is on by default locally and in the DMG.

## What was done

- `mcp-gateway/cmd/server` is built as `vault-server` (pure Go, modernc sqlite, `CGO_ENABLED=0`) in `desktop/dev-setup.sh` and both jobs of `.github/workflows/desktop-release.yml` (darwin arm64), and shipped through `extraResources`.
- `desktop/lib/vault.js` + `main.js`: the agent port and a different Vault port are picked up front (`pickPorts`; the agent keeps preferring 45678 so the frontend's localStorage stays stable; Vault prefers 45680). Vault starts before the agent in platform mode with the server deploy's env, state in `<userData>/vault` (0700, outside the workspace docs), a once-generated 32-byte hex token file (0600), audit sqlite async, log `<userData>/logs/vault.log`. The gateway prints no `DynamicPort:` line, so readiness is `GET /healthz`. The agent gets `CAPLAYER_SERVICE_URL`/`CAPLAYER_SERVICE_TOKEN_FILE`. Vault is listed in the health wait and killed with the other children, and restarted by "restart servers".
- Opt out with `AGENTWORKS_LOCAL_VAULT=0`. A missing binary or a gateway that does not become healthy starts the app without Vault (CAPLAYER_* removed from the agent env, a warning in vault.log), because a set-but-unreachable Vault URL fails closed.
- `desktop/package.json` `files` now includes `lib/*.js`: the packaged app omitted `desktop/lib` (found with an unsigned `electron-builder --dir`), so `require('./lib')` could not resolve.

## Verified

Gateway built for this Mac and started with the exact env: `/healthz` 200, state created under the given dir. Real `agent-server` started by the real spawn module with the Vault env: logs `[SECRET_SELECTION_MIGRATION]` (Vault configured), no Vault errors. Missing binary and opt-out paths return no Vault. Unsigned `--dir` build contains `vault-server` and `lib/vault.js`.

## Not verified / left

Signed/notarized DMG, first run on a clean Mac, upgrade of an existing install with existing secrets, Intel build (CI builds arm64 only). Existing installs: the one-time secret-selection migration runs at the first start with Vault and detaches selections whose secret exists only in the legacy store (backups under `state/migrations/secret-selections-v1/`, see `docs/vault-secret-selection-migration.md`); the Platform group auto-grant keeps shared secrets/MCPs usable for the single user. `desktop-sparkquill` is not changed.
