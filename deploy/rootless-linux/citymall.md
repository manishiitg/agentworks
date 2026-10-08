# Citymall (agents.citymall.live)

Citymall's own host: AWS Mumbai, `13.206.199.45` (the IP changed once already; ask Citymall for an Elastic IP),
Ubuntu 26.04 LTS, x86_64, 2 CPUs, 7 GB RAM, 96 GB disk. Tickets: PLAT-710 (server), PLAT-711 (Pi providers).

Products: Goals (`agentworks`), Crew (`work`), Code, Brain (`knowledgebase`) and Vault (`mcp-gateway`). Relays and the
others are off. Pi is the only coding agent, on Citymall's own AI gateway, model `citymall/gpt-6-luna`
(the only model; gpt-5.6-luna was removed on 2026-10-08 at the owner's request).

## SSH: through the Hetzner jump, the key stays on the laptop

Port 22 admits only the Hetzner build host. The laptop connects through it with ProxyJump:

```bash
ssh -i ~/Downloads/manish.pem -o IdentitiesOnly=yes -J root@116.202.210.102:2299 ubuntu@13.206.199.45
ssh -i ~/Downloads/manish.pem -o IdentitiesOnly=yes -J root@116.202.210.102:2299 citymall@13.206.199.45
```

The jump only forwards the TCP connection; authentication to the box happens from the laptop. Rule (owner): the key
`~/Downloads/manish.pem` stays on the laptop. Never copy it, or any key derived from it, to the Hetzner host, this box,
the repository or a build, and never use agent forwarding (`-A`). `deploy.sh` reads the key path from
`products/citymall/product.env` (`SSH_KEY_PATH`, default `~/Downloads/manish.pem`, overridable) and the jump from
`SSH_JUMP`.

## Deploy

```bash
./deploy.sh citymall
```

From an owned checkout, like every rootless product (`deploy_rootless_product` in `deploy.sh`), with three differences
set in `products/citymall/product.env`:

- `HOST_SETUP_SCRIPT=setup-citymall-host.sh`: first, as root through the ubuntu account's sudo, the idempotent host
  preparation: packages (including nginx and certbot), the unprivileged `citymall` account (home `/srv/citymall`, no
  sudo, no docker group), the owner-only `/srv/citymall/.env` (secrets generated once, never replaced), the
  `citymall-agent`/`-workspace`/`-gateway` systemd `--user` units and the nginx site.
- `PREBUILT_DELIVERY=fetch`: nothing is compiled on this 2-CPU box and it cannot read the build host's `/srv/_builds`.
  The shared build (built once on the Hetzner host) is published to `github.com/manishiitg/agentworks-builds` and the box
  downloads it into `/srv/citymall/prebuilt/<build>`, checking the manifest hash read from the build host; without a
  published release it is streamed through the laptop. The normal manifest verification and activation follow.
- `PUBLIC_CHECKS=false` until DNS and HTTPS exist: the deploy checks the loopback services and nginx on
  `127.0.0.1:80` instead of `https://agents.citymall.live`.

Ports: 25000 agent, 25001 workspace, 25003 Vault, 25080 gateway (all loopback). The activation job is capped at
3 GB / 1.5 CPUs, each Pi process at a 1.5 GB Node heap (`PI_CLI_NODE_MAX_OLD_SPACE_MB`), and the agent has a 2 GiB
`GOMEMLIMIT`.

## nginx

`products/citymall/nginx-site.conf` is installed as `/etc/nginx/sites-available/citymall` (the default site is
removed), validated with `nginx -t` (the previous site is restored on failure) and reloaded. It listens on port 80 with
`server_name agents.citymall.live _;`, so it answers on the IP now and on the domain later, and proxies to the
**gateway** on `127.0.0.1:25080`, never to the agent (25000) directly: the gateway serves the frontend and checks each
request's user token before anything reaches the agent or the workspace. WebSockets (Upgrade/Connection), SSE
(`proxy_buffering off`), hour-long read timeouts and 512 MB uploads are configured.

Ports 80/443 are closed in Citymall's security group, so check it on the box: `curl -sS -o /dev/null -w '%{http_code}'
http://127.0.0.1/login`. To use the app before DNS, tunnel the gateway: `ssh -L 25080:127.0.0.1:25080 ... citymall@...`
and open `http://localhost:25080` (cookies are Secure; browsers accept them on localhost only).

### HTTPS

DNS (`agents.citymall.live` → 13.206.199.45) and port 80 were live on 2026-10-08; the Let's Encrypt certificate was
issued then (`certbot --nginx … --no-redirect`) and `certbot.timer` renews it. A new box gets it with:

```bash
sudo certbot certonly --nginx -d agents.citymall.live      # on the box, as ubuntu; port 80 must be open
```

The deploy never overwrites HTTPS: `setup-citymall-host.sh` writes the listen/ssl lines to
`/etc/nginx/citymall-tls.d/tls.conf` whenever the certificate exists, and `nginx-site.conf` includes that folder.
Port 443 must also be open in Citymall's security group. Then set `PUBLIC_CHECKS=true` in `products/citymall/product.env` and redeploy. `PUBLIC_URL` is already
`https://agents.citymall.live` in `.env`, and the runtime config already names that origin.

## Pi on the Citymall gateway

The gateway is OpenAI Chat Completions at
`https://cm-ai-images-apim.azure-api.net/chat/v1/<deployment>/chat/completions` with an `api-key` header.
`gpt-6-luna` (answers as `gpt-6-luna-2026-09-22`) and `gpt-5.6-luna` work. Tool calls work only with
`"reasoning_effort": "none"`: the default reasoning with tools returns HTTP 400 ("use /v1/responses"), and
`/responses` returns 500, so Codex cannot use this gateway until Citymall enables the Responses API. Model listing
endpoints return 500/404, so the models are configured explicitly.

`products/citymall/pi-agent/` holds the Pi `models.json` (the `citymall` provider, one `baseUrl` per model,
thinking `off` mapped to `none`) and `settings.json` (thinking `off` by default for both models). The release copies it
to `current/configs/pi-agent`; `PI_CLI_AGENT_TEMPLATE_DIR` points the Pi adapter at it and the adapter stages both
files into every session's private `PI_CODING_AGENT_DIR` (PLAT-711). The files hold no key: the adapter refuses a
template whose `apiKey` or credential header is not `$CITYMALL_API_KEY`. The key is `CITYMALL_API_KEY` in the
owner-only `.env` and reaches each Pi through the adapter's provider-key path (the server account), never through
the template, logs or the repository. Confined Pi (Landlock) may exec only the managed CLI prefix and its Node, granted by
`SANDBOX_EXTRA_SYSTEM_PATHS` in `product.env`. The product defaults (Goals, Crew, Code) are Pi on
`citymall/gpt-6-luna`, set once as admin defaults (Providers page; `PUT /api/provider-accounts/product-defaults`). A person choosing a higher thinking level for these models gets the gateway's
HTTP 400 on tool calls; leave it off.

## Sign-in (pending)

Google through Supabase (`AUTH_PROVIDERS=supabase-google`). Only people in the user directory can sign in: on
2026-10-08 the owner chose four admins (gaurav@, akram@, nverdhan@citymall.live and the owner's own Gmail) and no
domain-wide rule (`AUTH_ALLOWED_EMAIL_DOMAINS` is not set). An admin adds everyone else; a sign-in never creates an
account. Until the owner adds these to
`/srv/citymall/.env` and redeploys, nobody can sign in through the UI:

- a Supabase project for Citymall with the Google provider enabled. Its Google Cloud OAuth client (Web application)
  needs the authorized redirect URI `https://<project-ref>.supabase.co/auth/v1/callback`; in Supabase, Authentication
  -> URL Configuration: Site URL `https://agents.citymall.live`, Redirect URLs `https://agents.citymall.live/auth/callback`.
  If citymall.live is a Google Workspace domain, an "Internal" consent screen restricts Google itself to it.
- `SUPABASE_URL=https://<project-ref>.supabase.co`, `SUPABASE_ANON_KEY=...`
- `ADMIN_USERS=<first admin>@citymall.live`

## Not established yet

Model pricing, exact context/output limits, image input on `gpt-6-luna`, the image-generation endpoint
(`/images/v1/mai-image-2.6-flash/generations`, needs its own tool integration), managed Chrome (no Chrome on the box;
browser tools are off until one is installed) and per-user slot accounts (not provisioned).
