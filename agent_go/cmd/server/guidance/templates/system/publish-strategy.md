# Publish strategy — share HTML artifacts at a public URL

You publish a workflow's **HTML artifacts** to a **public URL** on a static
host. This is the share-twin of backup: backup keeps things safe; publish makes them visible.
You are **provider-agnostic** — you deploy to whatever static host the config names, using its
CLI / git / file-sync, and you record the resulting URL. Never invent a destination; the
config is the contract.

This publish flow is only for separately hosted static reports. An internal
AgentWorks live report URL created with `get_report_link` already uses the app's
SSO and workflow authorization; it is not a publish target and must not receive
a second StatiCrypt/password/login gate. Keep the private/public visibility and
password rules below for reports deployed to an external static host.

## What you publish

For a **workflow**, publish **BOTH** artifacts — the dashboard **and** the Pulse log. This is
**mandatory** unless the user explicitly says "dashboard only" (or "pulse only"). Do not skip
the Pulse log just because the saved `publish.targets` only lists the dashboard — if both
should go out, **update `targets` to include both** before you build. Deploy `dashboard.html`
AND `pulse.html`; if only one file ends up on the host, you did it wrong.

Use the same workflow-style config/status split:

- workflow publish config/status: `workflow.json.publish` + `publish/status.json`

- **Reporting dashboard** (`reports/`) — **live** HTML: it calls `window.report.query(sql)`
  (and may call `updateField`/`updateFields` to write a field) against `db/db.sqlite` inside
  the app, which doesn't exist on a static host — reads AND writes both need it. **Generate a
  static snapshot** first (next section) → `dashboard.html`.
- **Pulse state** — remains in SQLite and is shown in the application Pulse popup.
  It is not a static publish artifact.

For workflow publish, deploy three files: `dashboard.html`, `pulse.html`, and an
**`index.html` wrapper** with a **top nav** (Dashboard | Pulse) over a single iframe —
clicking a tab swaps the iframe's source. This gives each view full width, avoids
the double-scroll / collapse problems of a side-by-side embed, and never modifies the two
inner pages:

```html
<!doctype html><html class="dark" data-theme="dark"><head>
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <meta name="color-scheme" content="dark">
  <style>
    html,body{margin:0;height:100%;font-family:system-ui,-apple-system,sans-serif;background:#141413;color:#e8e7e2}
    nav{display:flex;align-items:center;gap:.25rem;padding:.5rem .75rem;border-bottom:1px solid #2c2b28}
    nav button{border:0;background:transparent;padding:.4rem .85rem;border-radius:.4rem;
      cursor:pointer;font:inherit;color:inherit;opacity:.65}
    nav button.active{background:#ffffff22;opacity:1;font-weight:600}
    iframe{border:0;width:100%;height:calc(100vh - 49px);display:block}
  </style>
</head><body>
  <nav>
    <button data-src="dashboard.html" class="active">Dashboard</button>
    <button data-src="pulse.html">Pulse</button>
  </nav>
  <iframe id="view" src="dashboard.html" title="view"></iframe>
  <script>
    var view=document.getElementById('view');
    // Inner pages already force dark; re-assert both hooks on each load as a backstop.
    view.addEventListener('load',function(){try{var d=view.contentDocument.documentElement;d.classList.add('dark');d.dataset.theme='dark';}catch(e){}});
    document.querySelectorAll('nav button[data-src]').forEach(function(b){
      b.onclick=function(){view.src=b.dataset.src;document.querySelectorAll('nav button[data-src]').forEach(function(x){x.classList.toggle('active',x===b)});};
    });
  </script>
</body></html>
```

(Prefer a **left sidebar** instead of a top nav if the user asks — same idea, nav on the
left, iframe filling the rest.) The wrapper above is **dark only** and re-asserts dark on the
iframe each load; apply the matching dark shim (below) to `dashboard.html` and `pulse.html` so
they force dark too. Never
publish secrets, `db/db.sqlite` raw, credentials, or `.env`/key files.

## Generating the static dashboard snapshot (the report)

A live report won't work on static hosting. Bake it to static HTML at publish time:

1. **Find the queries.** Read `db/reports/index.html`, including each internal view, and collect
   every `window.report.query("…")` SQL string it runs.
2. **Run them** through `query_workflow_db`, capturing each result set as JSON.
   Use the managed DB boundary; do not open the live database with `sqlite3` or
   Python while authoring/publishing the snapshot.
3. **Inline the data + a shim** into a copy of the report HTML, so the page reads baked data
   instead of the live bridge:
   ```html
   <script>
     window.__REPORT_DATA__ = { /* normalized-sql -> rows */ };
     var deniedWrite = function () { return Promise.reject(new Error('Editing is not available on this published snapshot.')); };
     window.report = {
       query: function (sql) { return window.__REPORT_DATA__[normalize(sql)] || []; },
       updateField: deniedWrite, updateFields: deniedWrite,
       run: function (path) { var runs = window.__REPORT_RUNS__ || {}; return path in runs ? Promise.resolve(runs[path]) : Promise.reject(new Error('Live data is only available in the app.')); },
       sendChatMessage: function () { return Promise.reject(new Error('Agent chat is only available in the live app.')); }
     };
   </script>
   ```
   Put this **before** the report's own scripts so the shim wins. Normalize SQL consistently
   (trim + collapse whitespace) on both the keys and in the shim. If the live report uses
   `window.report.updateField`/`updateFields` (an inline Approve/edit action — see
   `reporting-policy.md`), the shim's rejection is the only thing standing between a static
   viewer and a raw "not a function" error on click; there is no live backend on a static host
   for either function to reach, so a published snapshot is always read-only regardless of what
   the live report can do.
   A `window.report.run(path, args)` call has no server behind a static page:
   run the script once at publish time (same `REPORT_ARGS` the page sends)
   and bake its output into `window.__REPORT_RUNS__[path]`, or hide that panel.
   Hide or disable live chat/action controls on published snapshots; never embed
   app credentials or invent a public endpoint to emulate `sendChatMessage`.
4. The result is a self-contained static file. The data is a **snapshot as of now** — that's
   expected; auto-republish after each run keeps it current.

(Do not ship `db.sqlite` to the client or stand up a server — snapshot only.)

## Force the published HTML to DARK (both artifacts)

The output must be **dark only**, matching the app. Match exactly what the app sets on `<html>`:

- The app themes via **Tailwind's `dark` class** (`ThemeContext` does `classList.add('dark')`).
- Report/dashboard widgets honor **both** — `HtmlWidgetFrame` sets `class="dark"` **and**
  `data-theme="dark"`.
- The Pulse-log skeleton keys on **`data-theme="dark"`**.

So set **both** hooks, hard-coded to dark. Do **NOT** use `prefers-color-scheme` (it follows
the viewer's OS, usually light) and do **NOT** add a light/auto toggle — dark only.

For **every** published HTML file (the Pulse log AND the report snapshot), inject this in
`<head>` BEFORE the page's own styles/scripts:

```html
<meta name="color-scheme" content="dark">
<script>
  var de = document.documentElement;
  de.classList.add('dark');   // Tailwind / :root.dark report widgets + app parity
  de.dataset.theme = 'dark';  // data-theme="dark" widgets + the Pulse-log skeleton
</script>
```

Setting both is exactly what the in-app `HtmlWidgetFrame` does, so the page reuses the existing
dark CSS. (If a widget has no dark styling at all, add a minimal dark palette under
`html.dark, html[data-theme="dark"]`.)

## Every publish rebuilds from source — never redeploy a stale file

A publish (including a **re-publish** and the auto-republish) ALWAYS regenerates the
artifacts fresh, then deploys:
- **Re-snapshot** the dashboard (re-run the queries → current data), **re-inject the theme
  shim**, and **rebuild the `index.html` wrapper**. Do NOT redeploy a previously-baked
  `dashboard.html` — stale data, a missing theme, and the old layout persist otherwise.
- **Honor "publish both."** If the existing `publish.targets` lists only one artifact but
  both should go out (the default), update `targets` to include both before rebuilding — a
  re-publish must not silently stay single-artifact just because the old config did.
- After deploy, open the URL and confirm BOTH panes render and the page respects dark mode.

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
npx staticrypt dashboard.html pulse.html index.html \
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
for name in ("dashboard.html", "pulse.html", "index.html"):
    path = Path(name)
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
so the **auto-republish (Pulse)** step can re-encrypt without the user. For org publish, use a
user/global secret with the same named-secret convention. For workflow publish, record only
`visibility: "private"` and the `secret_name` in `workflow.json.publish` / `publish/status.json`.
For org publish, record those same non-secret fields in `pulse/publish.json` /
`pulse/publish/status.json`. **Never the password itself.**

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
finished static files — `dashboard.html`, `pulse.html`, `index.html` — into a scratch dir
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

For **org-level publish**, come back and persist state under `pulse/` instead:

- update `pulse/publish.json` with `enabled=true`, destinations, targets, visibility, and
  the top-level URL,
- write `pulse/publish/status.json` with `state: "published"`, the URL,
  `last_source_hash`, destination results, and updated time,
- never write org publish state into a workflow's `workflow.json`.

For workflow publish, read the destination's `provider`, `method`, and `site` from
`workflow.json.publish`. For org publish, read the same destination fields from
`pulse/publish.json`. Then deploy:

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
   the right config: `workflow.json.publish` for workflow publish, or `pulse/publish.json`
   for org publish (enabled, mode, targets, destination provider/method/site, visibility,
   and secret name only). For a CLI host, **proactively suggest the one-time CLI install** (exact command from
   the table) and confirm the CLI is installed and **logged in** (`<cli> login`) — the CLI
   handles auth, so you don't store tokens. If critical details are missing, ask in this chat
   and write the right status (`publish/status.json` for workflow, or
   `pulse/publish/status.json` for org) with state `configured_not_verified`. Do not publish yet.

   **Edit workflow.json safely.** For workflow publish, change ONLY the `publish` block and preserve every other
   field. The `targets` value must be a JSON array (of strings like `"report"`/`"pulse"`, or
   objects). After writing, re-read it with a JSON parser
   (`python3 -c "import json; json.load(open('workflow.json'))"`) to confirm it still parses —
   a malformed workflow.json drops the workflow's config and can hide the workflow from the UI.
   For org publish, write valid JSON in `pulse/publish.json` and do not touch workflow
   manifests.
2. **Verify** — on the first real publish, deploy, fetch the returned URL to confirm it loads,
   and only then mark `published`. Record the URL in both the destination and the top-level
   `url`.
3. Auto-republish (the post-run Pulse step) runs against a **verified** destination on **every**
   run — the source artifacts (`db/db.sqlite` + reports) can change every run, so
   publishing must use the current verified source set.

## Always write publish status

For workflow publish, before you finish — even on failure — write `publish/status.json`.
For org publish, write the same shape to `pulse/publish/status.json`:

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

Do not write operational publish status into workflow/org config files. Use
`publish/status.json` for workflow publish and `pulse/publish/status.json` for org publish.
If a destination is missing credentials or setup, mark it `failed` and continue with any
others.

**Get `last_source_hash` right or the dot lies.** The backend computes the source hash itself
(a sha256 over `db/db.sqlite` and `reports/`) and reports it as
`current_source_hash` in the workflow publish status. For org publish, hash
`pulse/goals.html` and `pulse/org-pulse.html` deterministically and record that hash in the
`pulse/publish/status.json` status file. Set `last_source_hash` to the current source hash you just
published. If you write any other string, a successful publish immediately reads as
**`stale`** (amber) and Pulse will keep re-publishing. If you genuinely can't obtain it,
leave `last_source_hash` empty — the dot stays green `published`, only change-detection is
disabled. The two states this controls:
- `published` (green) = config enabled + status `published` + hash matches.
- `stale` (amber) = published but the source changed since (it changes every run) — the next Pulse fire re-publishes and returns it to green.

## Discipline

- Provider-agnostic: deploy what the config names; don't assume a host.
- Static only: snapshot the dashboard, never ship the DB or stand up a server.
- Confirm public scope before the first publish; never expose secrets or raw sensitive rows.
- One destination missing setup never blocks the others.
