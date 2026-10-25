# Internet share tunnel (temporary off-machine links)

Local AgentWorks servers bind loopback, so `dashboard action=link` / `get_file_link`
URLs are same-machine previews. The internet share tunnel puts the running
server on the internet through a Cloudflare quick tunnel
(`https://xxx.trycloudflare.com`), after which those two tools return links
any signed-in collaborator can open. No account, DNS, or deploy needed —
`cloudflared` mints an ephemeral public URL on demand.

This is for **temporary sharing** (show a report for an hour). Durable public
distribution stays on the publish flow (Vercel / Netlify / Pages / S3), which
deploys static snapshots instead of exposing a live server.

## Security model

- A tunnel exposes the **whole server** (every route), not one report. There
  are no anonymous links: recipients must still sign in and have workflow
  access, and tunnel traffic transits Cloudflare.
- Starting/stopping is **admin-only**, per invocation. In single-user mode the
  local user is an admin; in multi-user mode only directory admins.
- **Default 1h expiry, max 8h.** Expiry, manual stop, tunnel-process death, and
  server shutdown all kill the tunnel and clear the URL override, so links
  stop advertising a dead URL.
- Hardening that ships with this feature: `/debug/pprof/*` requires
  authentication (it used to ride the static-file exemption), and the
  `/public/*` share routes were audited — they enforce handler-level auth,
  workflow access, token scopes, and protected-path rules.

## How it works

```
chat: manage_internet_share(action=start)
  → StartShareTunnel (admin check)
    → cloudflared tunnel --url http://127.0.0.1:<actualPort>
    → parse trycloudflare.com URL from stderr (45s timeout)
    → store override + arm expiry timer
  → dashboard action=link / get_file_link build URLs from the override
chat: manage_internet_share(action=stop|status) / expiry / shutdown
  → kill cloudflared, clear override
```

- `agent_go/cmd/server/share_tunnel.go` — supervisor, URL parsing, override,
  tool registration (`manage_internet_share`, action `start|stop|status`,
  optional `duration_minutes`), and `GET /api/share-tunnel/status`
  (admin-only, serves the Publish panel).
- The override lives only in `effectiveShareBaseURL()`, used solely by the two
  share-link builders. OAuth callbacks, Gmail, and bot connectors keep reading
  `PUBLIC_URL` env directly, so a temporary tunnel never rewrites their hosts.
- `SetShareTunnelServerPort(actualPort)` is called at startup because
  `--port 0` (dynamic) makes the configured port wrong; `StopShareTunnel()` is
  called on server shutdown.
- `CLOUDFLARED_BIN` overrides the binary lookup (tests inject a stub). A
  missing binary errors with install instructions instead of auto-installing.
- Frontend: `InternetShareSection` in `WorkflowPublishView` shows the active
  URL + expiry with a whole-server warning, or a quiet inactive line. It hides
  entirely for non-admins (403). Start/stop stays in chat — the panel is
  status-only.
- Agent guidance: `templates/system/secure-share-links.md` points local-only
  link flows at the tool, with the never-start-without-explicit-request rule.

## Operating notes

- One tunnel at a time; `start` while active returns the existing tunnel.
- A restart mints a new random URL — previously shared links break. Tell
  recipients links are temporary.
- If `cloudflared` prints no URL within 45s (slow edge assignment), start
  fails and the agent should retry; the supervisor kills the orphaned process.
- A fresh tunnel URL needs ~20-30s of DNS propagation before it resolves
  ("it may take some time to be reachable" in cloudflared's own words). If a
  recipient reports the link not resolving immediately after sharing, have
  them retry rather than re-sharing.
- Logs carry the `[SHARE_TUNNEL]` prefix (start/stop/expiry).
