# Deployment unification

Owner decision (2026-10-03): every server is deployed the same way, with one runtime profile, so a bug found on one server is found on all
and a fix is tested once. Browser profiles have one setup; native workspace mode and slots behave the same everywhere.

Status: **step 1 done** (2026-10-03): `./deploy.sh report [server]` (deploy/common/profile-report-all.sh, profile_report.py,
runtime_profile.json) prints each server's difference from the profile, read-only, and every rootless-linux deploy prints it at the end.
No server setting has changed yet. The inventory below was read on 2026-10-03 ~18:00 (read-only, from the running
processes' environment, `.env`, systemd units, releases and health endpoints).

## Today: three deploy paths

| | Excellence / Confida / SparkQuill | RTS | Dominion |
|---|---|---|---|
| Script | `deploy/rootless-linux/build-and-activate.sh` | `deploy/aws-ec2/server/build-and-activate.sh` | `deploy/dedicated-vm/deploy-dominion.sh` |
| Secrets | hand-kept `.env` | AWS Secrets Manager → `.env` (`GLOBAL_SECRET_*`) | hand-kept `.env` |
| Units | hand-installed base unit, deploy writes one drop-in (ExecStart drifts from product.env) | full unit reinstalled from the repo each deploy | hand-installed, deploy rewrites drop-ins |
| Build | fresh clone, cgo agent (`build-linux-agent.sh`), slotctl/slottmux, deploy lock | same shape, under `systemd-run` limits | persistent checkout, plain `go build`, no slotctl, no lock; `configs/` and `runtime-config.js` copied from the previous release |
| Drain | yes (`DEPLOY_DRAIN_SECONDS`, default 0 for now) | none | none |
| Health check | env check + `deployment_checks running` + URL wait | `is-active` + log tail | `is-active` + 120 s health wait |
| Rollback | none | none | **automatic** |
| Kept releases | 1 on Excellence (no rollback target) | 2 | 2 |

## Today: runtime differences that change behaviour

| Setting | Excellence | Confida | SparkQuill | Dominion | RTS |
|---|---|---|---|---|---|
| `NATIVE_WORKSPACE` | true | true | true | true | **unset** |
| `MULTI_USER_MODE` | true | true | **unset** | true | true |
| `AGENTWORKS_SLOTS` | on | optin | unset | unset | optin |
| Slot table path | unprefixed `/etc/agentworks/slots.json` | `/etc/agentworks/confida/…` | — | — | default |
| `AGENTWORKS_CLI_LANDLOCK` / `CLI_FULL` | on/on | on/on | unset | unset | unset |
| Private `/tmp` for sandboxes | yes | yes | yes | **no** (no `STATE_ROOT`/`TMPDIR`) | yes |
| `AGENT_BROWSER_SHARED_PROFILE` | unset | unset | `state/browser-profile` | unset (ephemeral Chrome) | `/data/video-studio/browser-profile` |
| Agent ports bound to | 127.0.0.1 | 127.0.0.1 | 127.0.0.1 | 127.0.0.1 | **0.0.0.0** (behind Caddy) |
| Default LLM source | unit ExecStart (none → bedrock) | unit | unit (drifted from product.env) | unit | deploy-written, `LLM_CONFIG_LOCKED` |

The 2026-10-03 browser bug ("Socket directory /run/user/990/agent-browser is not writable") came straight from this table: only a host
with native mode on and no shared profile left `AGENT_BROWSER_SOCKET_DIR` unset.

## Target

**One script**: `deploy/rootless-linux/build-and-activate.sh`, parameterised (app dir, data dir, bind address), used by every server.
RTS keeps a small pre-step that turns Secrets Manager into `.env`; Dominion's hand-kept `runtime-config.js` and `configs/` move into
`deploy/rootless-linux/products/dominion/`.

**One profile, the same everywhere:**
- `NATIVE_WORKSPACE=true`, `MULTI_USER_MODE=true`, `AGENT_BROWSER_CDP_ENABLED=false`.
- `AGENTWORKS_CLI_LANDLOCK=on`, `AGENTWORKS_CLI_FULL=on` (and whatever replaces them after hybrid removal).
- `AGENTWORKS_STATE_ROOT=<app>/state`, `TMPDIR=<app>/state/agent-tmp` (private `/tmp` for every sandbox), `AGENTWORKS_MCP_STATE_DIR=<app>/state/mcp`.
- Browser: `AGENT_BROWSER_SHARED_PROFILE=<data>/browser-profile` (with `-users`, `-projects`, `-workflows`), socket folder set by the
  platform (never `$XDG_RUNTIME_DIR`).
- Slots: same handling everywhere; the slot table always at `/etc/agentworks/<product>/` with an explicit `SLOT_PREFIX`; `slots/bin` first on PATH.
- PATH, units and drop-ins generated from the repo on every deploy (no hand-kept drop-ins).
- Every release keeps `source/` + `SOURCE_REVISIONS`; the previous release is kept; drain, health wait and **automatic rollback** on.

**Per server only:** product name, ports, domain, account/app/data dirs, product list and surfaces, default provider/model and
`SUPPORTED_LLM_PROVIDERS`, gateway auth mode and admins, slot prefix/count/CLI users, `DOCKER_HOST`, browser session namespace, secret source.

**Guard:** each deploy prints the effective settings and their difference from the profile, and refuses a difference that is not on the
per-server list. A post-deploy check runs the same tests on every server (shell and terminal sandbox, browser start, one coding-agent turn, health).

## Order

1. Settings report + guard in the rootless-linux path (changes nothing, shows drift).
2. Add rollback and parameterised dirs/bind address to the rootless-linux path.
3. Bring Excellence, Confida, SparkQuill to the profile (Dominion and RTS untouched): browser profile on, `MULTI_USER_MODE` on SparkQuill,
   prefixed slot table on Excellence, units from the repo.
4. Dominion: products/dominion directory, then switch paths in a quiet window (Saturday, no trading), agreed with its owning session.
   Adds private `/tmp` (trading code must use `$TMPDIR`) and the cgo build.
5. RTS: Secrets Manager pre-step, browser service and Chrome wrapper ported, keep the live profile at `/data/video-studio/browser-profile`,
   `NATIVE_WORKSPACE=true`, remove the stale `/etc/systemd/system/video-studio-*` units; dry run first, quiet window.

## Risks

- RTS layout (`/var/lib/video-studio`, `/data` mount, 0.0.0.0 behind Caddy) differs from `/srv/<product>`; moving it needs parameters, not
  a data move. Its 144 MB browser profile holds sign-ins and must stay where it is.
- Switching RTS to native mode changes HOME and env for every sandboxed command; test on Confida first.
- Dominion runs a live trading workflow on weekdays; its first switch also changes the agent build (cgo) and adds private `/tmp`.
