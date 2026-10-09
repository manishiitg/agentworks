# Video Studio — Linux / EC2 Deployment Runbook

This is the isolated public Video Studio deployment. It has one fixed product
contract: the shared AgentWorks platform and the Video Studio surface are
always present; Finance, Dominion, and other product surfaces are not exposed,
and only the `video-studio` backend product is loaded. The release script
validates both allowlists before every deployment.

`./deploy.sh rts` already guarantees most of
[`../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md`](../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md)
automatically on every release — that doc exists because a sibling
deployment without an equivalent script had to rediscover each item as a
live incident. Skim it after any change to the shared `workspace`/`agent_go`
sandboxing or Caddy config, since this deployment shares that code.

- URL: `https://video.realtrainingsys.com`
- Region: `us-west-2`
- Stack: `video-studio-prod`
- Deploy method: server clones all three `main` branches → native Linux build → user-level systemd restart
- Infrastructure: CloudFormation

The deployer uses the `RTS` AWS profile and an SSH key. SSH ingress is limited
to the deployer's current public IP; it is not publicly open.

## Deploy

From the repository root:

```bash
./deploy.sh <server>
```

The RTS case in `deploy.sh` (`deploy_rts`) sends the deployment instructions
and secrets; the server-side `server/bootstrap-build.sh` and
`server/build-and-activate.sh` do the build, activation, and health gates.
After the deploy it prints the CloudFront usage report
(`report_rts_cloudfront_usage`; override the distribution with
`CLOUDFRONT_DISTRIBUTION_ID`).
Its defaults use the `RTS` AWS profile, `us-west-2`, and
`~/.ssh/id_ed25519`; set `AWS_PROFILE_NAME`, `AWS_REGION`, or `SSH_KEY_PATH`
only when overriding those defaults.

This release path connects as `video-studio`, writes only its own application
directory, and restarts only its user services. It neither runs `sudo` nor
touches infrastructure, the shared login password, or unrelated services.

The Mac sends only small deployment instructions and secret configuration. The
server clones `main` from `mcp-agent-builder-go`, `mcpagent`, and
`multi-llm-provider-go`, and records their exact SHAs in `SOURCE_REVISIONS`.
`bootstrap-build.sh` installs Go 1.27.1 with checksum verification when needed.
Builds run under a deployment lock, with a 6 GB memory ceiling and two CPU cores.
Failed builds leave the current release active. Once a build and its release gates
pass, activation is intentionally immediate and breaking: user services restart
without waiting on the logical active-session count, and clients reconnect to the
new runtime. Temporary checkouts are removed after the build. The host retains
Go/npm dependency caches for reuse.
The three configured GitHub repositories must be readable by the server.

## Per-user accounts (since 2026-09-02)

Visitors sign in with their own AgentWorks account instead of the shared
gateway password. The gateway runs with `GATEWAY_DISABLE_PASSWORD_GATE=true`
and verifies the app's own JWT on every `/api` and `/api/wp` request (401
otherwise, apart from the login/mode/health routes), stamping `X-User-ID`
from the token. Accounts live in `config/users.json` on the box and are
managed by an admin in the app ("Users & access" in the workflow toolbar)
— see `docs/design/user_accounts_and_workflow_sharing.md` and
`docs/core/multi_user_authentication.md`.

The one-time switch-over from the shared password (done on server A in
September 2026; the script has since been removed) set `MULTI_USER_MODE`, `ADMIN_USERS`, a bootstrap `AUTH_USERS` (initial
password = the old `ACCESS_PASSWORD`; remove that line once the admin has
logged in), `GATEWAY_DISABLE_PASSWORD_GATE`, and moves everything the shared
identity owned (`_users/default`: projects, history, secrets) to the admin's
own user id. Each user then gets their own projects tree; the admin adds
users and ticks `video-studio` for them. `ACCESS_PASSWORD` must still be
present (the gateway refuses to start without it) but is no longer asked for.

The agent binary is built natively with cgo and GCC on the Linux server
(`build/build-linux-agent.sh`). Video Studio's
microphone dictation (`agent_go/pkg/voicestt`, `voice: preferred` in its
`product.yaml`) links sherpa-onnx's native Linux libraries, which a plain
`CGO_ENABLED=0` cross-build replaces with a stub that answers the mic with
503. The libraries ship in `bin/lib` beside the binary. On the first start
the first mic click offers a one-time ~690MB download (NVIDIA's Nemotron
English streaming model plus a punctuation model) into
`~/.agentworks/voice-models/`, with progress shown in the composer; pre-place
the files there to skip it. No Docker or Go installation is required on the deployer's Mac.

After the agent passes its health check, normal deployments remove older release
copies. Only the current release, releases referenced by running processes, and
uploads marked `.deploying` are protected. No rollback archive is retained.
The helper is shared by the other Linux release deployments; see
[`../common/README.md`](../common/README.md).
`python3 prune-releases.py /var/lib/video-studio/video-studio` previews cleanup;
add `--apply` to remove the listed unused copies. Application data and logs live
outside `releases` and are not touched. A failed upload can leave a `.deploying`
marker; remove that marker after confirming the deployment has stopped.

## Runtime security model

Normal releases run with no `sudo`. A one-time bootstrap/migration uses
administrator access to create the dedicated account and its initial service
files; the running product and every subsequent release use `video-studio`.

| Component | Runtime identity |
| --- | --- |
| Agent API | `video-studio` system user |
| Workspace API | `video-studio` system user |
| Cookie-auth gateway | `video-studio` system user |
| HTTPS proxy | Existing Caddy container, UID/GID `10001` (`video-studio`) |
| Frontend files | Served by the non-root gateway from the release directory |

The release directory and runtime environment are owned by `video-studio` at
`/var/lib/video-studio/video-studio`. The user cannot read the legacy
root-owned environment file under `/opt`.

Do not set `WorkingDirectory` to a release path that the runtime user cannot
traverse. Do not run an installer with `RELEASE_DIR=/opt/video-studio/current`:
that can make the `current` symlink point to itself. Always use a concrete path
under `/opt/video-studio/releases/`.

## Global secrets

Deployment-managed global secrets are stored in AWS Secrets Manager:

```text
video-studio/global-secrets
```

The secret value is a JSON object with upper-snake-case keys:

```json
{
  "EXAMPLE_API_KEY": "replace-me"
}
```

During deployment, each entry becomes `GLOBAL_SECRET_<NAME>` in the
runtime environment file. The agent makes it available to every Video
Studio project as `SECRET_<NAME>`. Values are never returned by the UI or
`list_secrets`; only names are visible.

`CLAUDE_CODE_OAUTH_TOKEN` is the sole reserved entry. It is a Claude Code
setup token used as the deployment-wide provider default, not a project tool
secret. A saved project-specific Claude Code token overrides it.

The deploy script retrieves the Secrets Manager value into a temporary mode
`0600` file, uploads it, installs it, then removes the temporary upload. Never
put actual secret values in this repository, a release directory, or command
output. `deploy/aws-ec2/global-secrets.env` is intentionally gitignored as an
emergency local fallback; use the included `.example` file only as a format
reference.

## MCP configuration

This product has its own intentionally empty MCP config:

```text
/var/lib/video-studio/video-studio/current/configs/mcp_servers_video_studio.json
```

Do not copy the generic developer workstation MCP catalog into this host.
Product tools are provided through the bundled `mcpbridge` binary.

## Agent-to-platform URL

The rootless EC2 agent must call the platform API through its local loopback
address:

```text
MCP_API_URL=http://127.0.0.1:8000
```

This is a deployment setting, not a secret. The server A deploy preserves an
existing value or writes this default into
`/var/lib/video-studio/video-studio/.env` before restarting the services.
Do not set it to `http://host.docker.internal:8000`: that hostname is only
appropriate when the agent itself runs in Docker and does not resolve on this
rootless host. A wrong value prevents agent tools such as `show_video` from
calling the Production API.

## Post-deploy checks

Run these checks from a machine with the deploy SSH key. They show service
status and secret names only—never secret values.

```bash
ssh -i ~/.ssh/id_ed25519 video-studio@"$RTS_HOST_IP" \
  'systemctl --user is-active video-studio-agent video-studio-workspace video-studio-gateway'

ssh -i ~/.ssh/id_ed25519 video-studio@"$RTS_HOST_IP" \
  'awk -F= "/^GLOBAL_SECRET_/ {print \$1}" /var/lib/video-studio/video-studio/.env | sort'

ssh -i ~/.ssh/id_ed25519 video-studio@"$RTS_HOST_IP" \
  'grep -q "^MCP_API_URL=http://127.0.0.1:8000$" /var/lib/video-studio/video-studio/.env'

curl -fsSI https://video.realtrainingsys.com/

# Caddy does not compress responses by default -- a site block with no
# `encode` directive silently ships the full uncompressed frontend bundle
# (multiple MB) on every page load. Confirmed as the root cause of a real
# "server is slow" report on the sibling Dominion Hetzner deployment
# (dominion-hetzner.md), where the fix was adding `encode zstd gzip` to
# that one site's Caddy block. Verify it here too, against the real JS
# bundle referenced by the authenticated app shell, not just the small
# /login page (small responses compress trivially either way and can look
# fine even when the real bundle isn't compressed):
curl -sS -H "Accept-Encoding: gzip" -D - -o /dev/null \
  https://video.realtrainingsys.com/assets/index-*.js
# expect: content-encoding: gzip
```

An unauthenticated HTTP `303` redirect to `/login` is expected.

### Trace public request latency

The gateway logs a start and completion record for every public request by
default. Completion records include the route (`frontend`, `agent`, or
`workspace`), response status and bytes, time to first byte, total duration,
stream classification, and `slow=true` for non-streaming requests taking at
least one second. `X-Request-ID` is returned to the client and forwarded to the
agent API, whose `[API]` lines include the same ID for cross-layer correlation.
Gateway logs record only the escaped URL path, never the query string, so SSE
and media bearer tokens are not written to the request timing log.

```text
[GATEWAY] --> request_id=... method=GET path="/api/..." route=agent stream=false client_ip="..."
[GATEWAY] <-- request_id=... method=GET path="/api/..." route=agent status=200 bytes=... ttfb_ms=... duration_ms=... slow=false ...
```

Use `GATEWAY_REQUEST_LOG=false` only when this diagnostic stream must be
disabled. Long-lived SSE and WebSocket requests are marked `stream=true`; their
total duration is connection lifetime and is intentionally excluded from the
slow-request flag.

The gateway treats browser pages and API calls differently when the shared
login session expires: pages redirect to `/login`, while `/api` and `/ws`
return `401` with an `X-AgentWorks-Login` header. The shared frontend follows
that explicit signal instead of reporting a healthy backend as unavailable.
Authenticated sessions are renewed when they are within six hours of expiry,
so regular use does not repeatedly prompt for the password; an idle session
still expires after twelve hours.

## Headless browser prerequisite

The bootstrap template, repair script, and every normal rootless deploy install
HyperFrames' pinned Chrome Headless Shell under the unprivileged
`video-studio` account. The server does not need or install desktop Google
Chrome. `agent-browser` remains the browser-control CLI, while both it and
HyperFrames receive the same managed executable path:

```text
AGENT_BROWSER_EXECUTABLE_PATH=/var/lib/video-studio/.cache/hyperframes/chrome/<platform-version>/chrome-headless-shell-linux64/agentworks-chrome-headless
HYPERFRAMES_BROWSER_PATH=<same path, injected into guarded commands>
```

The wrapper runs Chromium multi-process under the Landlock filesystem policy:
`--no-zygote` and `--in-process-gpu` avoid the zygote and GPU-process startup
paths that need a child's own `/proc` entries (Landlock's `/proc/self` grant is
bound to the launcher PID), `--disable-dev-shm-usage` keeps shared memory out
of the ungranted `/dev/shm`, and a private `/tmp/aw-browser-<uid>` TMPDIR
survives the per-command scratch cleanup. It is the same launcher as
`deploy/rootless-linux/chrome-agentworks`. It no longer passes
`--single-process`, under which one renderer crash killed the whole browser. The deployment verifies the managed binary and
`agent-browser` before switching releases, and rewrites the runtime path on
every restart so it survives reboots and upgrades. Do not replace it with
`/usr/bin/google-chrome` or `/usr/bin/chromium`.

The Linux shell sandbox is Landlock-first with a verified mount-namespace
fallback. It grants Chromium only its own `/proc/self` view plus read-only font
and hardware metadata—not all of `/proc`. Do not run the service as root, grant
it `CAP_SYS_ADMIN`, or broaden `/proc`, because doing so could expose other
service processes' environments.

**Verify the mount-namespace fallback actually works, not just that
Landlock's own preflight passes.** Landlock is purely additive (no
allow-with-carve-out rules) and rejects any Folder Guard policy that needs
one — e.g. write access to a folder except one of its subfolders — falling
through to the mount-namespace path instead. Found live on the Dominion
Hetzner deployment (`dominion-hetzner.md`) 2026-08-28/29 as two stacked
bugs, both now fixed:

1. **Application bug, fixed in `workspace/security/isolator.go`/
   `isolator_linux.go`**: the fallback ran a plain `unshare -m` (mount
   namespace only, no `-U`/`--user`), which needs real `CAP_SYS_ADMIN` in
   the *current* user namespace — something an unprivileged service account
   never has. It failed with `Operation not permitted` on every rootless
   deployment, unconditionally, making the fallback permanently
   non-functional regardless of any OS setting. Fixed by adding
   `--user --map-root-user` (the standard unprivileged-mount-namespace
   pattern rootless container runtimes use). Any deployment built from a
   `workspace` module at or after this fix already has it — no action
   needed beyond redeploying.
2. **Ubuntu 23.10+/24.04-specific, still needs a per-host check**:
   `kernel.apparmor_restrict_unprivileged_userns` defaults to `1`, which
   separately blocks unprivileged user-namespace creation for any process
   without an explicit AppArmor profile, independent of
   `kernel.unprivileged_userns_clone` (which can show enabled while this
   still blocks it) and independent of fix 1 above — both were needed
   together. Symptom if only this one is present:
   `SANDBOX_UNAVAILABLE: Landlock cannot represent this Folder Guard policy
   and mount namespaces are unavailable: ...`. Check
   `cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns` — if it's
   `1`, decide (a real security trade-off, not a default to reach for)
   between disabling it host-wide (`kernel.apparmor_restrict_unprivileged_userns=0`
   via `/etc/sysctl.d/`, simplest but loosens every process on a shared
   host) or a narrower AppArmor profile granting `userns_create` to only
   this product's own binaries.

Post-deploy, validate the actual guarded path (not only direct SSH) by running
`npm run check` through `/api/execute` with Folder Guard enabled in a Video
Studio production. A direct SSH browser test is insufficient because it does
not exercise Landlock or the sanitized command environment.

## Future SSM option

The current deployment uses SSH + rsync. If it later moves to SSM, use an S3
or CI-built artifact for file delivery and SSM only for controlled remote
installation. The EC2 instance role—not the deployer user—will need
`AmazonSSMManagedInstanceCore`.
