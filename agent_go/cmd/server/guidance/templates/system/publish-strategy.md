# Publish strategy — publish a dashboard on any static host

Publish the saved dashboard to the destination in `workflow.json.publish`, using
that provider's CLI, git or file-sync. The same portable export works on Surge,
Vercel, Netlify, Cloudflare Pages, GitHub Pages, Firebase and object storage.
The destination and existing public/private settings are the contract.

An AgentWorks live link already uses sign-in and project access. It is not a
static export and must not receive a second password gate. External publishing
exposes the publishing Builder's frozen data under the saved Publish visibility.
Never publish other people's private chats, secret values, account credentials
or the raw database. The person's request to publish authorizes the export;
do not add another confirmation for the same destination and audience.

## Build the static site from the same dashboard

1. Use dashboard action=list and choose the saved published dashboard(s). Do not build
   a separate React/Vite app, extract literal SQL, parse minified JavaScript,
   convert TSX back to hand-written HTML, or copy the live index.html alone.
2. Call **dashboard action=export_static** with dashboard_id and the selected theme
   (dark by default). It renders the exact publication in the Builder's scoped
   preview, captures real query/get/run results and authorized media bytes, and
   packages the shared React, shadcn, Recharts, CSS and report runtime. Existing
   HTML publications also use this path. A failed export is not a publish.
3. Copy **every returned file** from its `path` into the scratch staging directory.
   `index.html` includes the authored page and captured data. `report-preview.js`,
   `dashboard-react.js` and `dashboard.css` are shared runtime assets and must
   accompany it. All URLs are relative, so roots and subfolders both work.
   Do not upload scripts/, the raw DB, workspace files or preview tokens.
4. Keep the generated Updated-at notice and Open live dashboard link. Charts,
   tabs, filters and animations remain interactive with captured data. Ask,
   functions and writes use the signed-in live dashboard; a static site never
   gains backend access. Refresh replays captured data; a new Publish updates it.
5. If several dashboards are configured, export each into its own subfolder and
   make a simple index linking them. Keep their original names and views. Do not
   expose AgentWorks's private conversation: it remains in the application.
   Separately authored public pages may be included only when selected by the
   person; do not manufacture an AgentWorks transcript or force a second page.
6. Apply the saved visibility/password procedure below to **all data-bearing
   HTML pages**, then deploy the whole staged folder with the existing provider
   adapter. Encrypting index.html covers its source/data; shared JS/CSS has no
   snapshot payload. Never leave an unencrypted copy or separate data JSON beside
   an encrypted page. Do not encrypt or edit the platform runtime files.

Put initial data for every view in TSX `loadData(report)`, including file/media
calls whose results the static page needs. Local filters/conditions can use that
returned data without a backend. An interaction needing a previously unread
query or file reports that it needs the live dashboard; gather that data in
loadData when the person wants it available in the static snapshot too.

## Refresh through Publish automation

Every Publish pass, including after-run auto-publish, calls dashboard action=export_static
again before deploying. After every run is one Goal choice, `publish.after_runs`,
off for a new Goal. Preserve its existing choice when configuring a destination.
The Publish panel, MCP update_settings and chat update_workflow_config change
the same setting. An explicit Publish now action works with automatic publishing
off; do not create a second publisher or a new schedule. Source change checks
include managed dashboard revisions and db/code files; generated publish/exports
folders are excluded. Do not redeploy an older export when current data changed.
Record the actual destination URL and current source hash in publish/status.json
and workflow.json.publish only after the provider confirms the deploy. Open the
hosted URL and check its views, timestamp and assets. Never report an internal
preview as successful external publishing.

## Private by default — a simple password gate

Publishing puts the data on a URL, so **default to private.** The catch: no major static host
gives you a simple shared password for free — Netlify/Vercel site passwords are paid, and
Cloudflare/Azure "private" means a full **login** (OAuth/email), not a password. So don't reach
for those by default, and **never ask the user for an access token** to make a page private.

Instead, gate it **client-side** with a passphrase — free, one command, identical on every
host. Encrypt the static HTML with **StatiCrypt** *after* baking + theming, in the staging dir,
in **one invocation** so a single unlock covers the whole site for the browser session. The
password prompt is user-facing UI: do not ship StatiCrypt's default green/white page. Use the
AgentWorks dark gate styling below every time.

```
cd /tmp/publish-<workflow>
# For multiple dashboard folders, include every data-bearing HTML page here.
npx staticrypt index.html \
  -p "$SECRET_PUBLISH_PASSWORD" --remember --salt <fixed-32-hex> -d . \
  --template-title "AgentWorks Private Report" \
  --template-instructions "Enter the publish password to open this report." \
  --template-button "Unlock report" \
  --template-placeholder "Publish password" \
  --template-remember "Remember this browser" \
  --template-error "That password did not unlock this report." \
  --template-color-primary "#0ea5e9" \
  --template-color-secondary "#070b12"

cat > agentworks-staticrypt-gate.css <<'CSS'
.staticrypt-html,
.staticrypt-body {
  min-height: 100%;
  background: #070b12 !important;
}
.staticrypt-content {
  min-height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  padding: 24px;
  background: #070b12 !important;
  color: #e5e7eb;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
.staticrypt-page {
  width: min(100%, 420px);
  padding: 0;
  margin: 0;
}
.staticrypt-form {
  max-width: 420px;
  margin: 0;
  padding: 28px;
  text-align: left;
  color: #e5e7eb;
  background: #111827;
  border: 1px solid rgba(148, 163, 184, 0.18);
  border-radius: 8px;
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.42);
}
.staticrypt-instructions {
  margin: 0 0 18px;
  color: #aab4c5;
}
.staticrypt-instructions p {
  margin: 0;
  line-height: 1.5;
}
.staticrypt-title {
  margin: 0 0 8px;
  color: #f8fafc;
  font-size: 22px;
  font-weight: 750;
  letter-spacing: 0;
}
.staticrypt-hr {
  margin: 0 0 18px;
  border: 0;
  border-top: 1px solid rgba(148, 163, 184, 0.16);
}
.staticrypt-password-container {
  margin: 0 0 14px;
  background: #070b12;
  border: 1px solid rgba(148, 163, 184, 0.22);
  border-radius: 8px;
}
.staticrypt-form input[type="password"],
.staticrypt-form input[type="text"],
input[type="text"] {
  min-height: 46px;
  color: #f8fafc;
  background: transparent;
  font-size: 15px;
}
.staticrypt-form input::placeholder {
  color: #7f8a9d;
}
.staticrypt-toggle-password-visibility {
  opacity: 0.72;
  filter: invert(1);
}
label.staticrypt-remember {
  margin: 0 0 18px;
  color: #cbd5e1;
  font-size: 14px;
}
.staticrypt-remember input[type="checkbox"] {
  accent-color: #0ea5e9;
}
.staticrypt-form .staticrypt-decrypt-button {
  min-height: 46px;
  padding: 0 16px;
  background: #0ea5e9;
  border-radius: 8px;
  color: #03111f;
  font-size: 15px;
  font-weight: 750;
  text-transform: none;
  box-shadow: 0 12px 28px rgba(14, 165, 233, 0.22);
}
.staticrypt-form .staticrypt-decrypt-button:hover,
.staticrypt-form .staticrypt-decrypt-button:active,
.staticrypt-form .staticrypt-decrypt-button:focus {
  background: #38bdf8;
}
.staticrypt-spinner {
  border-color: rgba(148, 163, 184, 0.45);
  border-right-color: transparent;
}
@media (max-width: 480px) {
  .staticrypt-content {
    padding: 18px;
  }
  .staticrypt-form {
    padding: 22px;
  }
  .staticrypt-title {
    font-size: 20px;
  }
}
CSS

python3 - <<'PY'
from pathlib import Path
css = Path("agentworks-staticrypt-gate.css").read_text()
for path in Path(".").rglob("*.html"):
    name = str(path)
    if not path.exists():
        continue
    html = path.read_text()
    if "staticrypt-html" not in html:
        raise SystemExit(f"{name} was not encrypted by StatiCrypt")
    if "agentworks-staticrypt-gate" not in html:
        if "</head>" not in html:
            raise SystemExit(f"{name} has no closing </head> tag")
        html = html.replace(
            "</head>",
            '<style id="agentworks-staticrypt-gate">\n' + css + "\n</style>\n</head>",
            1,
        )
        path.write_text(html)
PY
rm -f agentworks-staticrypt-gate.css
```

Then deploy the (now-encrypted) files. Use `--remember` **plus a shared `--salt`** (any fixed
32-hex string, reused across all three files) so the unlock carries across the nav and its
iframes in one browser session — the viewer types the password **once** on `index.html`.
Verify after deploy in a fresh/private browser session (or append `#staticrypt_logout`) that
the gate is dark/app-like, not the default green/white prompt. If a frame still prompts,
inline the two views into the single nav page and encrypt just that one file.

**Store the password as a named secret — never in plaintext.** For workflow publish, put it in
the workflow's secret store (e.g. `PUBLISH_PASSWORD`) and read it as `$SECRET_PUBLISH_PASSWORD`,
so the **auto-republish (AgentWorks)** step can re-encrypt without the user. Record only
`visibility: "private"` and the `secret_name` in `workflow.json.publish` / `publish/status.json`.
**Never the password itself.**

**Honest limit:** a client-side gate is good *casual* privacy (keeps it out of public/search
view, needs the password to read) but not strong security — a weak password on the encrypted
file can be brute-forced offline. We already forbid publishing raw secrets; for genuinely
sensitive data, offer a **login host** instead.

**Stronger private (opt-in, only if the user asks):**
- **Azure Static Web Apps** — built-in auth + route authz on the **free** tier
  (`staticwebapp.config.json`, `"allowedRoles": ["authenticated"]`); login via GitHub / Entra.
- **Cloudflare Pages + Cloudflare Access** — free Zero Trust (≤50 users): deploy to Pages, then
  put an Access policy (email OTP / Google) in front.

**Public (opt-in, only when the user says so):** skip the gate and deploy the baked files as-is.
Fine when the dashboard has no sensitive data.

Whatever the choice, before the first publish of a destination:
- State plainly what will be visible (which artifacts; for the dashboard, which queries/rows)
  and confirm scope. If the config scopes the report to specific views/queries, honor it.
- Never expose raw credential/secret rows. If a query would surface sensitive data, either
  scope it out or use a private host — ask rather than publish.

## Deploying — three universal paths (pick what the host supports)

**Stage outside the workspace first.** The workspace folder is write-guarded, so deploy CLIs
that write working files into it (`.netlify/`, build output, lock files) will fail. Copy the
complete returned export folder — into a scratch dir
**outside** the workspace (e.g. `/tmp/publish-<workflow>/`), and run the deploy CLI from there
with an explicit `--dir`, skipping any build step (the files are already built). Don't try to
`cd` inside the workspace or let the CLI build in place.

**Then come back and persist state INTO the workflow folder — this is not optional.** The
`/tmp` dir is throwaway; the record the UI reads lives in the workflow. A deploy that succeeds
but leaves no `publish/status.json` + no `workflow.json.publish` block shows up as a **grey
"not configured"** dot — i.e. it looks like you never published. After the CLI returns the URL,
return to the workflow folder and, before you finish: (1) set `workflow.json.publish.enabled =
true` with the destination + top-level `url`, and (2) write `publish/status.json` with
`state: "published"`, the `url`, and `last_source_hash` (see the two sections below). Never
write these into the `/tmp` staging dir.

Read the destination's `provider`, `method`, and `site` from `workflow.json.publish`.
Then deploy:

1. **Provider CLI** (`method: cli`) — the default, and the host's own CLI **handles its own
   auth**. Almost every major static host ships a CLI, most installable with `npm i -g`.
   **Do NOT ask the user for an access token / API key.** The auth path is always: *install the
   CLI → the user runs `<cli> login` once (browser) → you deploy.* Before deploying:
   - **Auto-check + install.** As soon as the user names a host, check its CLI
     (`command -v vercel`). If it's missing, **install it for them**: say what you're doing
     ("Installing the Vercel CLI…") and run the table's install command (`npm i -g vercel`).
     Don't just hand over the command and wait — drive it. Pick the command for the host they
     named: Vercel → `npm i -g vercel`, Netlify → `npm i -g netlify-cli`, Cloudflare
     Pages/R2 → `npm i -g wrangler`, Firebase → `npm i -g firebase-tools`, AWS → `brew install
     awscli`. If the global install fails (e.g. `EACCES` on the npm prefix, or needs sudo), then
     fall back to giving the user the exact command to run. Announce, don't silently install.
   - **Check it's logged in** (`netlify status`, `vercel whoami`, `firebase login:list`,
     `wrangler whoami`, …). **You do NOT handle tokens or secrets — the CLI uses its own
     stored login session.** If it isn't authenticated, tell the user to run the one-time
     login command (e.g. `netlify login`); it opens a browser, so you can't do it for them.
     Then deploy. If a login command stalls, the fix is to re-run `<cli> login` — **not** to
     fall back to pasting a token.

   **Default to a FREE CLI host** unless the user asks for a specific one or already uses a
   paid provider. Good free defaults: **Surge** (simplest, instant `*.surge.sh`), **Cloudflare
   Pages** (most generous — unlimited bandwidth), or **Netlify**. The table is ordered free-first;
   AWS is the only paid option — only use it if the user already lives in AWS or asks.

   | Host | Tier | Install | Log in (one-time, user) | Deploy |
   |------|------|------|------|------|
   | Surge | **free** | `npm i -g surge` | `surge login --browser` | `surge <dir> <site>.surge.sh` |
   | Cloudflare Pages | **free** (unlimited bandwidth) | `npm i -g wrangler` | `wrangler login` | `wrangler pages deploy <dir> --project-name <site>` |
   | Netlify | **free** | `npm i -g netlify-cli` | `netlify login` | `netlify deploy --prod --dir <dir>` |
   | Vercel | **free** (Hobby) | `npm i -g vercel` | `vercel login` | `vercel deploy --prod --yes` |
   | Firebase Hosting | **free** (Spark) | `npm i -g firebase-tools` | `firebase login` | `firebase deploy --only hosting` |
   | GitHub Pages | **free** (public sites) | `gh` CLI (`brew install gh`) | `gh auth login` | `gh-pages -d <dir>` or push the `gh-pages` branch |
   | Azure Static Web Apps | **free** (+ built-in login auth) | `npm i -g @azure/static-web-apps-cli` | `az login` | `swa deploy <dir> --env production` |
   | Cloudflare R2 | free storage, **+setup** | `npm i -g wrangler` | `wrangler login` | `wrangler r2 object put <bucket>/<key> --file <f>` (serve via the public r2.dev / custom domain) |
   | AWS S3 + CloudFront | **paid** (pay-as-you-go) | `brew install awscli` | `aws configure` | `aws s3 sync <dir> s3://<bucket>` |

   If the host isn't listed, it almost certainly still has a CLI — check its docs for the
   install + login + deploy commands; the user logs in once, you deploy.

   *(Headless/CI only: most CLIs also accept a token env var — `NETLIFY_AUTH_TOKEN`,
   `VERCEL_TOKEN`, `CLOUDFLARE_API_TOKEN`, `SURGE_TOKEN`, etc. — via the destination's optional
   `secret_name`. Plain `surge login` always prompts for email/password interactively and will
   hang piped or in a background/scheduled context — never run it bare there. Two ways around it:
   (1) `surge login --browser` (needs a reasonably current surge — `npm i -g surge@latest` if the
   installed one predates it) prints a URL and a short code instead of prompting; paste both into
   chat, the user opens the URL and approves in their own browser, and the still-running command
   polls and completes on its own once approved — no password ever passes through the agent, and
   it works fine from a background context as long as a human is present to click the link. (2) For
   a genuinely unattended run with no human to approve anything (a scheduled publish), that's not
   an option — get a token once from an interactive session (`surge token`, itself requires a
   prior login), store it as the destination's secret, then deploy with
   `SURGE_TOKEN=<token> surge <dir> <site>.surge.sh`, no login step at all.
   For a person at the keyboard, prefer interactive `<cli> login`.)*
2. **Git-push-to-deploy** (`method: git`) — commit the static files to the repo/branch the
   host auto-builds (Netlify/Vercel/Pages/Render watch a branch). Use the git discipline from
   `read_skill(skills=[{"name":"builder-reference","path":"references/backup-strategy.md"}])` (atomic commit, `--force-with-lease`).
3. **Object-store / file sync** (`method: sync`) — the catch-all for any host that serves
   files from a bucket or directory: `aws s3 sync <dir> s3://<bucket>` (+ CloudFront),
   `rclone copy <dir> <remote>:<path>`, or `rsync` to a server+nginx.

If the provider isn't one you recognize, fall back to its documented static-deploy command,
or to `git`/`sync` — the method is what matters, not the brand. Auth always comes from the
named secret; never hardcode a token.

## Setup → verify → then auto

Follow the same set-up-then-prove flow as backup:

1. **Configure** (`action: "configure"`) — if the destination isn't set yet, add it to
   `workflow.json.publish` (enabled, mode, targets, destination provider/method/site, visibility,
   and secret name only). For a CLI host, **proactively suggest the one-time CLI install** (exact command from
   the table) and confirm the CLI is installed and **logged in** (`<cli> login`) — the CLI
   handles auth, so you don't store tokens. If critical details are missing, ask in this chat
   and write `publish/status.json` with state `configured_not_verified`. Do not publish yet.

   **Edit workflow.json safely.** For workflow publish, change ONLY the `publish` block and preserve every other
   field. The `targets` value must be a JSON array (of strings like `"report"`, or
   objects). After writing, re-read it with a JSON parser
   (`python3 -c "import json; json.load(open('workflow.json'))"`) to confirm it still parses —
   a malformed workflow.json drops the workflow's config and can hide the workflow from the UI.
2. **Verify** — on the first real publish, deploy, fetch the returned URL to confirm it loads,
   and only then mark `published`. Record the URL in both the destination and the top-level
   `url`.
3. Auto-republish (the post-run AgentWorks step) runs against a **verified** destination on **every**
   run — the source artifacts (`db/db.sqlite` + reports) can change every run, so
   publishing must use the current verified source set.

## Always write publish status

Before you finish — even on failure — write `publish/status.json`:

```json
{
  "version": 1,
  "state": "configured_not_verified | publishing | published | stale | failed",
  "url": "<public URL of the latest publish>",
  "last_published_at": "<ISO when a publish succeeded>",
  "last_attempt_at": "<ISO>",
  "last_source_hash": "<hash of the published artifacts>",
  "destinations": [
    { "id": "<id>", "provider": "<provider>", "method": "cli|git|sync", "url": "<url>", "state": "published|failed|skipped", "error": "<if failed>" }
  ],
  "last_error": "<empty on success; concise on failure>",
  "updated_at": "<ISO>"
}
```

Do not write operational publish status into `workflow.json`. Use `publish/status.json`.
If a destination is missing credentials or setup, mark it `failed` and continue with any
others.

**Record the source hash from the backend.** Read `current_source_hash` from the
workflow Publish status and save it as `last_source_hash` after a successful
deploy. It covers report/db/code sources, including managed TSX and JSON data;
`publish/exports` is outside the hash. The existing after-run check compares these
hashes to decide whether a new Publish is needed. A changed source does not turn
a successful deployment into a failure; record destination health truthfully.

## Discipline

- Provider-agnostic: deploy what the config names; don't assume a host.
- Static only: snapshot the dashboard, never ship the DB or stand up a server.
- Confirm public scope before the first publish; never expose secrets or raw sensitive rows.
- One destination missing setup never blocks the others.
