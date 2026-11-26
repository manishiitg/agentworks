# App theme contract

Personal themes use the existing claims-owned appearance document and its validated
CSS. No Crew preferences, provider settings or authored Artifact files are changed.

## One palette, shared controls

`src/themes/app-tokens.css` defines the default Light/Dark roles.
`src/contexts/appThemeTokens.ts` applies the saved colour and typography roles,
including a contrasting action label, and removes its tokens on scope changes.
New UI should use shared controls and semantic classes (`text-foreground`,
`text-muted-foreground`, `border-border`, `bg-muted`, `text-destructive`, etc.),
not a second Light/Dark palette. Do not put opacity on an entire panel.

Background material is separate from colour and text. Shared surfaces use
`bg-surface-page`, `bg-surface-panel`, `bg-surface-popover`,
`bg-surface-sidebar`, or `bg-surface-composer`. Their background-only opacity is
`--aw-<surface>-opacity` (0–1, default 1). Panels, popovers, sidebar and composer
also accept `--aw-<surface>-backdrop` (default `none`). For example, saved theme CSS:

```css
:root.dark {
  --aw-panel-opacity: .45;
  --aw-panel-backdrop: blur(12px);
}
```

The right workspace card has `data-theme-surface="panel"`. Its text and icons stay
opaque even at background opacity 0. Blur is optional and can cost GPU time.
CSS glass reveals personal app artwork, not the Mac desktop: native window vibrancy is a
separate desktop implementation. Authored report/force-light surfaces reset material
variables and retain their own palette. Explicit code palettes are separate.

## Live theme hooks

Hooks are presentation of existing authorized projections, not a new state machine,
telemetry feed or permission grant. They never contain messages, IDs or tool inputs.

- Sidebar agent rows and chat pills: `data-theme-agent-state` = `running`,
  `waiting`, `failed`, `done` or `quiet`; `data-theme-agent-selected` = `true|false`.
  These reuse `useAgentMenuState`, including its existing brief visual hold.
  `quiet` means no menu mark, NOT proof that a session is idle/stopped.
  `failed` and `done` are unread marks, cleared by opening the chat.
- Team header: `data-theme-team-working` and `data-theme-team-waiting` = `true|false`,
  using the same admitted Team summary displayed there. Working counts people,
  not raw tool calls. Unmounted/unavailable Team is not an idle signal.
- Transcript working group: `data-theme-turn-live` = `true|false` from the existing
  live flag. Tool summaries (including collapsed calls) expose
  `data-theme-tools-running` = `true|false`, requiring both that flag and a
  running paired call. No new event parsing or tool policy. These hooks only exist
  for mounted rows, so absence is unknown.
- Web search card: `data-theme-tool-state` = its existing `running|ok|error` status.
  A card is historical transcript content, NOT a global current-tool signal.

Example (only animation when motion is allowed):

```css
@keyframes agent-glow { to { box-shadow: inset 3px 0 hsl(var(--primary)); } }
@media (prefers-reduced-motion: no-preference) {
  [data-theme-agent-state="running"] { animation: agent-glow 1.8s ease-in-out infinite alternate; }
}
[data-theme-agent-state="waiting"] { outline: 1px solid hsl(var(--warning)); }
```

Use these hooks to decorate, never to hide Stop, warnings, text, focus or input.
No new listeners, polling, sound, click interception or background game control is
added by these hooks. Blocked, explicit stopped and global tool activity are not
invented: additional server-backed hooks can be added when their real projection
exists. Themes must not infer them from transcript text or historical cards.

## Keeping future UI consistent

`npm run check:theme-contract` always audits shared controls and audits newly added
app style lines against the PR merge base (`THEME_CONTRACT_BASE`, default origin/dev).
It runs before remote tsc/build. String/template AST scanning ignores comments.
Explicit authored renderers, code palettes and background artwork are exempt.
This is a style audit, not a security gate or a claim all legacy feature pages have
been migrated. Existing feature-specific raw colours remain incremental work.

## Visible agent-authored CSS backgrounds (Light or Dark)

Use explicit translucent materials, e.g.
`materials.panel.opacity: .6` and `materials.composer.opacity: .7`. Any offered
surface with opacity below 1 activates the same transparent app wrappers
also in Light, without a separate page-material trick. Then style the stable `[data-app-main]` hook,
not a deeply nested generated class. Give its gradient enough contrast to see;
1–2% alpha under opaque cards is not a visible background. Keep panels/composer
around .7–.9 opacity so text remains legible; never fade the whole element.
The semantic panel/composer materials own both Light and Dark, with no legacy
Light-only background override. Shared materials set background-color only; they
do not reset a custom gradient/background-image with an important shorthand. Put decorative keyframes inside
`@media (prefers-reduced-motion: no-preference)` and prefer small transform/opacity
motion to a full-window background-position repaint. Do not add blur over moving
artwork. Existing running hooks can gate the animation; historical tool rows
must not. Saved CSS is adopted after apply; material-only edits reproject
the root marker too. An adopted sheet/valid keyframe does not prove visible motion:
check computed animation name/play state, two frames over time, ancestor surfaces,
contrast, reduced-motion and a screenshot before claiming live success.

## Pausing custom theme motion when hidden

The existing appearance visibility subscription projects
`html[data-appearance-visible="true|false"]`. Its value tracks document visibility,
not window focus or agent execution; teardown removes the attribute. Custom CSS
can keep animations paused by default and run them only while this value is
`true`, inside `@media (prefers-reduced-motion: no-preference)`. Working effects
may additionally use the existing admitted activity hooks. Prefer small
transform/opacity accents over moving full-window backgrounds, filters or shadows;
never put backdrop blur over moving artwork. Themes belong in the person's saved
appearance document, not application source or a built-in product catalog.

## Agentic appearance entry

Settings and the account menu reuse one picker: Default Light/Default Dark replace
the current customization with canonical defaults; named personal themes apply
or delete through the existing appearance endpoint. Deleting a saved name does
not reset the current look. Two small typography controls offer Text size (12–20px)
and Font; a typography-only apply patch also updates the active saved theme.
System font families use local fallbacks, never downloads. Code fonts remain separate.
No broader manual editor or product preset cards.

Ask an agent chooses an existing own writable Crew tab, preferring the active one,
and prepends a starter request to its existing draft via the current draft helper.
It never sends, auto-runs, creates a chat or writes appearance settings itself.
Watched, teammate, setup and scheduled/bot/execution tabs are not candidates;
backend appearance and Send admission remain authoritative. If there is no own
chat, explain the next step rather than opening a foreign chat.

Only Default Light and Default Dark ship with the app. Personal colours, materials,
CSS, decorations and an optional image, video, shader or script background supply
every other look; there is no preset factory, scene enum or built-in renderer
catalog. Future tool fields are listed only when implemented, not from a design mock.

## Shared decorative loading indicator and status colours

`LoadingIndicator` is the shared glyph for core agent marks, working-turn lines,
workspace/modal loading, composer uploads/queue and common controls. Its caller
still owns real status, accessible text and lifetime. No new status parsing,
subscriptions, polling or per-glyph timers. Other feature spinners are incremental
migration work; do not claim a completed app-wide conversion.

Personal `loader.style` selects ring/dots/pulse/orbit/bar; `duration_ms` is 400–4000
(default 1000, lower is faster). Null clears customization. Root
`data-appearance-loader` is projected/removed with the other personal tokens.
`[data-theme-loader]` is stable; `data-loader-part` is internal artwork. CSS only
transforms/fades the selected glyph; the existing `data-appearance-visible` gate
and reduced-motion pause it. No compositor/frame-time claim without measurement.

Colour roles working/waiting/done/error fan out to existing info/warning/success/
destructive semantic controls, with contrasting labels; working also styles the
turn text. The optional loader role overrides the inner SVG colour only; without it a glyph
inherits its caller (including contrasting action-button labels), while agent marks
keep their existing working tone. Authored scopes
reset loader tokens. Team activity rings/text reuse the same existing counts,
not a new Team state or browser permission. Presence/avatar/typing colours are
not automatically reclassified as work statuses. Explicit code palettes remain
separate (owned by the code/terminal theme work).

## Expressive personal CSS and the stable selector inventory

The server publishes `guide.hooks` plus `guide.hook_guide` from one inventory in
`appearance_contract.go`; `css_max_bytes` is 131072 (128 KiB). The appearance-only
HTTP envelope is bounded separately at 1 MiB so JSON-escaped valid CSS fits.
Browser validation uses the advertised byte cap, not JS character length. There
is no new fetch allowlist, JS, polling or per-hook runtime registry.

Major shared components expose `data-theme-surface`, `data-theme-row`,
`data-theme-control`, message-role/body and composer-action hooks. Native
role=dialog/menu selectors remain public for legacy surfaces. Fields are static
presentation or existing rendered status, never names/IDs/text or a new state
machine. Tool status is historical unless inside an existing live turn/tools
projection. Some inactive agent rows expose state on their existing glyph: CSS
`:has()` on the row can consume it without a second status subscription.

New controls should use the shared primitives and an existing semantic surface
value, not invent a unique selector per screen. Keep native aria-* states and
roles. Do not add wrappers/observers just to populate attributes. Authored report
islands and terminal/syntax palettes retain their separate rendering paths.

CSSOM accepts CSS3D, masks, gradients, blend modes, decorative filters, keyframes,
pseudo-elements and embedded data SVG/images. HTTPS images (`url()`, `image-set()`)
and HTTPS fonts (`@font-face src`, `@import` of font stylesheets, which supply font
faces only) are allowed; relative, protocol-relative, `javascript:` and escaped
resources are dropped. Because remote images are allowed, the server refuses CSS
whose selectors use an input's `value` attribute (`[value…]`, any case or spacing),
and the browser parser drops the same selectors. Shader, script, decoration, image
and video backgrounds are separate typed fields. The appearance skill contains
small original-look techniques and screenshot iteration.
Static markup/parser tests do not prove visual layout, compositor cost or GPU
performance; those remain an actual-browser/laptop check. No universal migration
claim for every ad-hoc legacy feature control.

## Shared typed icon and border projection

Optional `icons` changes the existing Lucide outline family (`svg.lucide`):
round/square caps and joins, stroke width0.75–3, scale0.75–1.5 and the per-mode
`icon` colour role. It is not a new glyph library/filled set; logos and the shared
loader remain distinct. Independent CSS scale preserves existing transforms and
layout slots/hit targets. A custom icon colour may affect action icons, so the
agent must check contrast or leave that role unset.

`borders` configures core panel/card/composer/input/selected_agent/running_agent
surfaces: width0–4px, radius0–9999px, solid/dashed/dotted and per-mode
`<surface>_border` roles. Panel covers shared dialog/menu/popover frames too;
Card falls back to Panel settings. Running wins over selected on the same agent
row, using existing projected attributes only. No fabricated work/Team state,
new polling, focus changes, hidden controls or wrapper elements.

`appearanceControlCSS` generates declarations only for specified fields from the
validated document, so absent values do not reset ordinary utility styles. It
is one owned constructed sheet, installed/removed with the existing personal
token effect before custom CSS. The fixed selectors exclude authored report,
code and terminal islands. Custom CSS may override using these same selectors;
no `!important` is generated. No per-frame work/listeners. Default emits no
control rules; cleanup removes only its own sheet on account/theme changes.
Client selector/parser tests are not live cascade, screenshot or GPU proof.


## Optional Worker-script backgrounds (#2844)

`background_script:{code,fps}` is personal source (<=64KiB,1–30fps), mutually
exclusive with other background media. It defines `render(frame)`; frame has an
OffscreenCanvas, capped dimensions, time/delta, hex palette, working boolean and
reducedMotion. Never evaluate this string in React, the app window or an iframe
DOM. `scriptSandbox.ts` uses a product-only opaque iframe with allow-scripts
ONLY and default-none CSP, then a blob Worker inheriting that CSP. Source travels
over a transferred private MessagePort, never interpolated in srcdoc. Neither
worker nor frame has same-origin access; connect/import/frame/form resources are
blocked. Only bounded frame-complete/error signals return, not tool commands.
No external libraries are bundled or fetched. Inherited blob-worker CSP:
https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy#csp_in_workers

Hidden/theme/account changes terminate the Worker; reduced motion terminates
after one rendered frame. The host schedules one outstanding frame, throttles
sustained >33ms work to15fps, stops continued overload and kills stalled source
or render after1500ms. Browser allocation/GPU limits still apply. No arbitrary JS
runtime can promise a hard memory/GPU budget. Disable in Appearance clears only
background_script. Shared surface blur is suppressed over successful script
rendering; fallback CSS returns on failure. Never add app identifiers, tokens,
text, file paths or account endpoints to this message protocol.

## Preview and diagnostics (#3085)

`preview` stores a validated theme per person in server memory for 30 minutes; it
never changes current or saved themes and publishes no appearance live-feed
change. HTTP `read` returns it as `preview` (full theme) with
`preview_expires_at`; `end_preview` clears it. The `/appearance/preview` route
renders it on fixed sample content in light or dark (`?mode=`) without changing
the person's mode.

The app sends `{"operation":"report","report":{theme,source:"app"|"preview",mode,
at,dropped:[{text,reason}],hooks:{selector:count},background:{kind,state,fps,error}}}`
(at most 32 KiB, 50 dropped rules, 200 hooks). The server keeps the last report
per source in memory and returns it as `last_report.app` / `last_report.preview`
on read; tools cannot send reports. `read` also returns `saved_count` and
`saved_limit` (20).
