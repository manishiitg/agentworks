[← ops / deploys](index.md)

# PLAT-710: Citymall: new server with Pi on the Citymall gateway

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | ops |
| Area | deploys |
| Summary | New customer server for Citymall at agents.citymall.live (own AWS host, 13.206.199.45): Goals, Crew, Code, Brain, Vault; Pi on Citymall's gateway (gpt-6-luna) as the only coding agent; nginx; Google sign-in for @citymall.live pending. |

## What happened

Citymall gets its own server. The host was prepared on 2026-10-01 (setup-citymall-host.sh); it was not a deploy target,
the Pi model template was not loaded by the app, and nothing ran there.

## Fix

- `./deploy.sh citymall` (products/citymall/product.env): reaches the box with ProxyJump through the Hetzner host (the
  key stays on the laptop), runs the idempotent root preparation (packages, units, nginx site) through ubuntu's sudo,
  and gets the shared prebuilt build onto the box from GitHub (or streamed) instead of compiling (2 CPUs).
- nginx on port 80 (`server_name agents.citymall.live _`) proxies to the gateway on 25080 with WebSocket/SSE settings;
  certbot is a documented step once DNS points at the box.
- Products: agentworks, work, code, knowledgebase, mcp-gateway. `SUPPORTED_LLM_PROVIDERS=pi-cli`, default model
  `citymall/gpt-6-luna` (PLAT-711 stages the Pi provider template).
- Sign-in: `AUTH_PROVIDERS=supabase-google`, new `AUTH_ALLOWED_EMAIL_DOMAINS=citymall.live` (SSO and admin-added
  accounts are refused outside the domain, admins included).
- Sized for 7 GB: activation job 3G/150%, Pi Node heap 1.5 GB, agent GOMEMLIMIT 2 GiB.
- deploy/rootless-linux/citymall.md describes all of it.
- The app's daily CLI updater ran `codex update` (npm install -g) into Node's own prefix: a second codex in
  tools/node/bin, ahead of the managed tools/bin on PATH, failed the second deploy ("codex resolves outside the
  managed installation"). Self-updates now set npm_config_prefix to the prefix the CLI was installed in
  (agent_go/internal/cliupdate). Affects every host with a pinned Node (Confida too); the stray copy was removed on
  Citymall by hand.
- Pi runs under Landlock: `SANDBOX_EXTRA_SYSTEM_PATHS` grants exec on the managed CLI prefix and Node (first turn
  failed with "exec: /srv/citymall/tools/bin/pi: Permission denied").
- Acceptance (2026-10-07, on the box through the gateway on 127.0.0.1:25080): a Brain chat on Pi
  `citymall/gpt-6-luna` called `mcp__api_bridge__brain_access` (list) and answered "I found that Brain's root folder
  exists and currently has no subfolders; you have Owner access." Pi's transcript shows model gpt-6-luna, thinking
  off; the key appears in no file under the docs, logs, temp or configs folders.
- Admin product defaults set to Pi / citymall/gpt-6-luna for Goals, Crew and Code (Providers page setting, stored
  in config/provider-account-settings.json).

## Left

- Live on the box as release citymall-b3d9bc72 (2026-10-07); not reachable publicly until the owner steps below.

- Owner: DNS A record agents.citymall.live -> the box (an Elastic IP: the IP changed once), open 80/443, then
  `sudo certbot --nginx -d agents.citymall.live` and `PUBLIC_CHECKS=true`.
- Owner: a Supabase project with Google enabled (Google OAuth redirect `https://<ref>.supabase.co/auth/v1/callback`,
  Supabase redirect `https://agents.citymall.live/auth/callback`), then SUPABASE_URL, SUPABASE_ANON_KEY and
  ADMIN_USERS (first admin) in /srv/citymall/.env.
- Codex on the gateway needs Citymall's Responses API (/responses returns 500).

## 2026-10-08: first accounts

Owner: only four people for now, all admins (three Citymall staff and the owner). The citymall.live domain rule is removed from the Citymall config; the user directory is the only gate.

## 2026-10-08: DNS and HTTPS

DNS live (agents.citymall.live → 13.206.199.45), port 80 open, Let's Encrypt certificate issued. The nginx site from the repo now includes /etc/nginx/citymall-tls.d/, which the host setup fills when the certificate exists, so deploys keep HTTPS. Waiting on port 443 in their security group; then PUBLIC_CHECKS=true and an HTTP→HTTPS redirect.

Port 443 opened by Citymall the same day: http on the domain now redirects to https, and PUBLIC_CHECKS=true.
