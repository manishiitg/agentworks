[← ops / deploys](index.md)

# PLAT-710: Citymall: new server with Pi on the Citymall gateway

| Field | Value |
|---|---|
| State | in progress |
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

## Left

- Owner: DNS A record agents.citymall.live -> the box (an Elastic IP: the IP changed once), open 80/443, then
  `sudo certbot --nginx -d agents.citymall.live` and `PUBLIC_CHECKS=true`.
- Owner: a Supabase project with Google enabled (Google OAuth redirect `https://<ref>.supabase.co/auth/v1/callback`,
  Supabase redirect `https://agents.citymall.live/auth/callback`), then SUPABASE_URL, SUPABASE_ANON_KEY and
  ADMIN_USERS (first admin) in /srv/citymall/.env.
- Codex on the gateway needs Citymall's Responses API (/responses returns 500).
