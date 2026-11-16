# Change how the app looks

Make original personal themes through `settings action=appearance`, not app source
edits. The screen stays a small Light/Dark/saved-theme picker with Ask an agent;
there is no manual theme editor or gallery. Default Light and Default Dark alone
are built in. A named look is the person's data, not a product preset.

## The working loop

1. **Read** current in full, saved name/mode labels and the live guide. Use
   `export name=` for one saved theme's full body, not routine bulk reads. `guide.hook_guide` explains
   each selector; `guide.css_max_bytes` is authoritative. Preserve unrelated
   choices and any unsent work. Never supply a different person or host path.
2. **Design** a coherent look, not just one accent: surfaces, typography, readable
   text, borders/spacing through CSS, and optional passive art. Typed fields are
   the floor; CSS is the creative layer. Never invent a field the guide lacks.
3. **Apply/save** through the same appearance operation every agent uses. Read
   back the short mutation summary/current name and changed fields. The operation
   saves personal settings, not visual proof. Read again for the full document.
4. **Screenshot and adjust** the actual app on the correct computer/account via
   an authorized browser/Computer-use route. Check text, menus, sidebar, composer,
   Stop/Send, focus and the light/dark variant. Inspect two frames if claiming
   motion, plus hidden/reduced-motion behavior. Iterate on what is actually seen.
   If no screenshot route is available, say saved but visually unverified and
   ask the person to look. Never claim success from an adopted sheet alone.

Other people's chats/accounts remain private. The theme changes only the caller's
appearance on that connection, never a Crew, sharing, providers or authored files.
The app's own visibility/activity projections are presentation, not authority. Team summary booleans below are retained; new per-row Team hooks require its Manager's separate contract and are not offered by this slice.

## Current fields (read the live guide first)

| Field | Values and meaning |
| --- | --- |
| `name`, `mode` | Name up to60 characters; `light` or `dark`. |
| `interface_font` | `system`, `jetbrains-mono`, `fira-code`, `ibm-plex-mono`, `source-code-pro`; offered self-hosted fonts, not remote font URLs. |
| `code_font` | `system-mono` or one of the four named monospace fonts; separate from interface typography. |
| `interface_size`, `code_size` | Interface12–20px; code10–20px. The app projects scoped typography rather than changing document rem units. |
| `colors.light`, `colors.dark` | Six-digit hex values for `background`, `foreground`, `card`, `muted`, `muted_foreground`, `border`, `accent`, `sidebar`, `working`, `waiting`, `done`, `error`, `loader`, `icon`, `panel_border`, `card_border`, `composer_border`, `input_border`, `selected_agent_border`, `running_agent_border`. Omitted roles keep defaults. `accent` drives actions; contrasting action labels are derived automatically. |
| `materials` | `page`, `panel`, `popover`, `sidebar`, `composer`: background `opacity`0–1, default1. Non-page `blur`0–24px, default0. Text/icons stay opaque. Unset materials are optional, not a null DTO. |
| `loader` | `style`: `ring`, `dots`, `pulse`, `orbit`, `bar`; `duration_ms`:400–4000, default1000 (lower is faster). Null clears it. Optional loader colour overrides the inner glyph; absent colour inherits readable control labels/working tone. |
| `icons` | Optional existing Lucide outline styling: `style: round/square` (stroke cap/join), `stroke_width: 0.75–3`, `scale: 0.75–1.5`. Scale1 keeps each glyph's existing size/hierarchy and hit targets. No new glyph library or filled set is offered. `icons:null` clears geometry; colour is a separate role. |
| `borders` | Optional `panel/card/composer/input/selected_agent/running_agent` objects: `width_px:0–4`, `radius_px:0–9999` (0 sharp,9999 pill), `style:solid/dashed/dotted`. Panel also covers shared menus/dialogs/popovers; Card falls back to Panel. `borders:null` clears geometry; per-mode border colours remain independent. |
| `css` | Up to the server's UTF-8 byte limit (131072 /128KiB), not a character limit. Ordinary CSS, embedded data images/SVG, gradients, keyframes, masks, blends, decorative filters, pseudo-elements and CSS3D are expressive options. |

Shared loader coverage includes core agent/turn/page/modal/composer/common
controls; legacy feature-specific glyphs are incremental work. Avatars belong to
existing own-agent identity tools, not the theme. Discover their current schema.
Code/ANSI palettes and automatic mode switching are a separate implementation:
use them only when the live guide offers them. Do NOT claim alternative icon
libraries, typed chat/layout, image-asset operations or shader fields exist before they land.
There is no scene enum, hidden stock renderer or executable theme JavaScript.

### Icon and border details

These fields feed one shared styling layer, not per-screen copies or a new
status subscription. Lucide glyphs expose the stable `svg.lucide` library class;
brand logos, the shared loader, authored reports, code and terminal contents are
not recoloured/resized by these controls. A chosen icon colour also affects
action icons: inspect Send/Stop, destructive actions and selected rows for
contrast rather than assuming one colour fits every background. Leave `icon`
unset if each control should keep its own colour.

Border fields use existing shared surface/agent hooks only. Running overrides
selected when both apply; historical tool status never creates a running agent.
Width0 removes a border, not the control or focus ring. Missing fields retain
normal styling; large radii do not change hit targets or clip child controls.
Feature-specific legacy borders are not all migrated. Custom CSS can refine
these same documented selectors without relying on generated utility names.

For example, patch `icons:{style:"square",stroke_width:1.5,scale:1.1}` and
`borders:{composer:{width_px:2,radius_px:18},running_agent:{width_px:2,style:"dashed"}}`,
with per-mode `composer_border` and `running_agent_border` colours. These are
settings for the person's own look, not a built-in theme. To reset the geometry
AND its colours, read current, remove those fields/roles, and apply the complete
remaining document with `replace:true`. Screenshot the actual light/dark UI.

## Operations

- `read`: full current, saved name/mode labels and guide. The HTTP picker still
  receives full saved themes; tool reads avoid filling context with unused CSS.
- `apply`: patch current fields; with `name` and no theme, select your saved theme.
- `save`: save/update a named theme AND apply it.
- `duplicate`: optional `name` (omit for current) and `new_name`; store without applying.
- `rename`: `name` and `new_name`; never overwrite a different saved name.
- `export`: read-only selected `name` or current; returns one theme object. Save
  that object as JSON, not the whole preferences document.
- `import`: pass that object as `theme`, optionally override `name`; validate
  from Default and store without applying. Inspect before applying received CSS.
- `delete`: remove one saved name, leaving the current applied look unchanged.
- `reset`: Default Dark, preserving saved themes.

Up to20 named themes. Patches retain omitted fields. `replace:true` starts from
Default, so a clean new look/empty colour maps do not inherit earlier overrides.
Use it deliberately, not when the person asked to adjust just one detail.
Mutation replies are concise summaries, NOT the full CSS/library or proof the
look is good. `read`/`export` return the needed own content.

## Public hooks

Use these, not generated class names. `guide.hook_guide` is the current inventory.
Shared primitives carry the hook once; ad-hoc legacy surfaces still have their
native role selectors. These attributes add no observers, timers or permission.

| Selector | Targets / limits |
| --- | --- |
| `[data-theme-surface="app"]`, `"main"`, `"sidebar"` | App shell, main workspace, left navigation. Existing `[data-app-main]` stays. |
| `[data-theme-row="navigation"]`, `"project"`, `"agent"` | Core sidebar navigation/project/agent rows. |
| `[data-theme-surface="panel"]`, `[data-theme-card]` | Shared panel/Card; existing right workspace card uses the same panel surface. |
| `[data-theme-surface="chat"]`, `"empty"` | Core transcript region and empty-chat states. Do not break virtualization/scroll geometry. |
| `[data-theme-message-role="user"]`, `"assistant"`, `"system"` | Rendered journal/message or system-activity role, NOT verified sender identity. |
| `[data-theme-message-body]` | Text/Markdown body apart from receipts and actions. |
| `[data-theme-surface="tool"]`, `[data-theme-tool-state]` | Existing tool display `running/ok/error`; a historical card is NOT live work. |
| `[data-theme-surface="code"]`, `"terminal"` | Core code/xterm frames. CSS cannot recolour characters already rasterized on a terminal canvas; use offered code/ANSI fields for that. |
| `[data-theme-surface="composer"]`, `"composer-band"` | Composer card/band; `[data-chat-composer-card]` remains stable. |
| `[data-theme-control]` | Shared `button/input/textarea/select/composer-input`. Native `:disabled`, `:focus-visible`, `aria-expanded` etc. retain their real meanings. |
| `[data-theme-composer-controls]`, `[data-theme-action]` | Actions band; `send/stop/attach/microphone/composer-menu`. Never hide or cover these. |
| `[data-theme-surface="dialog"]`, `"menu"`, `"popover"` | Shared surfaces. `[role="dialog"]`/`[role="menu"]` also cover semantic legacy dialogs/menus; menu rows have `role="menuitem"`. |
| `[data-theme-agent-state]`, `[data-theme-agent-selected]` | Existing row/state glyph `running/waiting/failed/done/quiet`, selected `true/false`. Some inactive rows expose state on the glyph: use `:has()` on the row. Quiet is not idle proof; unread done/failed clear on open; visual holds remain. |
| `[data-theme-turn-live]`, `[data-theme-tools-running]` | Existing live group booleans. Gate tool effects on true, not historical status. Absence from virtualized/unmounted rows is unknown. |
| `[data-theme-team-working]`, `[data-theme-team-waiting]` | Existing viewer-admitted summary booleans, not a global exact agent count. Waiting is often a teammate reply, not an alarm. |
| `[data-theme-loader]` | Shared decorative glyph; real status/lifetime comes from its caller. |
| `svg.lucide` | Existing outline glyphs, not brand images/loaders. Shared typed stroke/scale/colour projection excludes authored report/code/terminal islands. |
| `[data-app-ambient]` | Empty full-window passive layer. Use `::before/::after`; it never takes clicks. |
| `html[data-appearance-visible="true"]` | Document visibility, NOT focus. Pause custom motion unless true. |

Existing `.conversation-markdown`, `[data-chat-find-scope]`,
`[data-testid="transcript-user-bubble"]`, `[data-product-settings]` and
`nav[aria-label="Workspace navigation"]` remain stable. Native roles and shared
controls cover more legacy UI than the new data-hooks alone; don't claim every
old feature-specific element was migrated. Do not invent stopped/blocked states.

## Expressive, but cheap and usable

- Keep foreground/background and muted text/card clearly different in lightness.
  Glass still needs enough tint for reading. Never fade the entire panel/text.
- Glass reveals in-app artwork, not the macOS desktop; native window vibrancy is separate.
- A static gradient or embedded SVG can be rich without a repaint loop. Any
  translucent material clears inner app wrappers in both modes. Shared materials
  set background-colour only; they do not erase your background-image.
- Prefer small transform/opacity animation. Pause by default; opt in only inside
  `prefers-reduced-motion:no-preference` AND visible=true, optionally requiring
  an existing running hook. Static frames under reduced motion are still themes.
- NO blur over moving art. Set all non-page material blur to0 when adding motion;
  the server checks numeric ranges, not whether arbitrary CSS animates. Filters
  on small still decoration are allowed, not an excuse for full-window GPU work.
- HTTPS images are allowed in `url()`, `image-set()` and other image properties;
  HTTPS fonts are allowed in `@font-face { src: url(https://…); }` and `@import
  url("https://fonts.googleapis.com/css2?family=…");` (optionally with a media
  query). Imported stylesheets supply font faces only, not app selectors. The
  browser fetches them without credentials or a referrer, then sanitizes them.
  Up to eight imports / three nested levels /128KiB total imported CSS; failed
  network/CORS loads leave the local theme intact. External hosts see resource
  requests: never include private data in URLs. Download/embed is an alternative.
  Embedded data images and local fragments remain allowed. No scripts, relative,
  protocol-relative or `javascript:` URLs, escaped resources, or CSS that hides,
  covers or disables controls. Keep image-set URLs explicit (not string-valued
  var substitutions). Save/import preserve CSS; filtering happens on application,
  not as a false server/network probe.
- Explicit authored Artifact/report designs keep their own default palette; avoid
  targeting their force-light/report islands. An iframe is a separate document.
- No assumption that CSS/compositor work is free: screenshot and, for expensive
  looks, measure the actual laptop. Do not add JS, polling or background loops.

## Four starting techniques (not built-in presets)

Start from the read document, or `replace:true` for a deliberate clean new look.
Build a named personal theme, then screenshot/iterate. These are ingredients,
not a product catalog; adapt them to what the person asks for.

### Glass bridge — bright static art, tinted readable panels

Use Light with a pale hull background (`#edf5f7`), white card, dark text
(`#17313a`), teal accent (`#167d8d`), panel opacity.8 /blur6, sidebar.9 /blur0,
composer.92 /blur0. Static art under blur; no animation in this recipe:

```css
[data-app-main] {
  background-image: linear-gradient(140deg,#e8f3f7,#f9fcfd 60%,#dceef3);
}
[data-app-ambient]::before {
  content:""; position:absolute; inset:64px 20px 20px 28%;
  border:1px solid hsl(var(--primary)/.15); border-radius:24px;
  background:linear-gradient(90deg,transparent 49.8%,hsl(var(--primary)/.05) 50%,transparent 50.2%);
}
```

### Paper and ink — texture without moving anything

Use Light, warm background `#f5f1e8`, card `#fffdf7`, ink `#2e2b25`, muted text
`#6c6559`, border `#d9d1c1`, accent `#805d3b`; System font, opaque materials.

```css
[data-theme-message-role="user"] [data-theme-message-body] {
  background:transparent; border:1px solid hsl(var(--border)); border-radius:6px;
}
[data-theme-card] { box-shadow:none; border-style:dashed; }
```

### Neon terminal — strong text, restrained frame accents

Use Dark, background `#080f13`, card `#101c22`, text `#d4f5df`, accent `#76ddb0`,
working `#76ddb0`, error `#f17e82`; zero material blur. This styles the app/frame,
not terminal ANSI or syntax tokens (use those fields only when offered).

```css
[data-theme-surface="terminal"], [data-theme-surface="code"] {
  outline:1px solid hsl(var(--primary)/.3); outline-offset:-1px;
}
[data-theme-row="agent"]:has([data-theme-agent-state="running"]) {
  box-shadow:inset 2px 0 hsl(var(--primary));
}
```

### Calm minimal — make space for the work

For a clean new theme use `replace:true`, System14px/System Mono12px, white or
charcoal defaults with one quiet accent, opaque materials and `css:""`. If the
person only wants flatter panels, keep their current theme and add:

```css
[data-theme-card] { box-shadow:none; }
[data-theme-surface="composer"] { box-shadow:none; }
```

## CSS3D now: a floating decorative frame

Ordinary perspective/3D transforms survive the same CSSOM filter. This is NOT a
WebGL/shader field or theme JS. Keep the actual app/controls flat; only tilt a
small empty pseudo-element. Use zero material blur with this moving decoration.

```css
[data-app-ambient]::after {
  content:""; position:absolute; right:3%; top:18%; width:180px; height:110px;
  border:1px solid hsl(var(--primary)/.3); border-radius:18px;
  backface-visibility:hidden; transform-style:preserve-3d;
  transform:perspective(650px) rotate3d(1,1,0,14deg) translate3d(0,0,12px);
  opacity:.35;
}
@media(prefers-reduced-motion:no-preference) {
  [data-app-ambient]::after { animation:frame-tilt 14s ease-in-out infinite alternate paused; }
  html[data-appearance-visible="true"]:has([data-theme-agent-state="running"])
    [data-app-ambient]::after { animation-play-state:running; }
  @keyframes frame-tilt {
    to { transform:perspective(650px) rotate3d(1,1,0,20deg) translate3d(0,0,20px); opacity:.5; }
  }
}
```

Masks, blend modes and still SVG/data art can add depth without theme code.
A GPU shader background is a separate future slice: never invent its schema.
The parser check proves declarations survive, not that the actual GPU view works.

## Share a look through an existing Team setup

Only when asked to share: export the selected/current theme, not the whole saved
library. Use the existing admitted Team file workflow to save the JSON and a
Markdown instruction document. Read the current Team-setup guide/schema:
`setup_share` takes a shared instruction `{path,revision}` and checklist items;
`setup_apply` dispatches to the recipient's own offered agent and that agent
checks its own progress. Do not invent a gallery, grant, account or recipient.

The setup says: read the shared JSON, import it through YOUR appearance tool,
apply its saved name, screenshot/iterate on YOUR app/account, then mark your own
setup progress. No foreign-person name lookup or direct preferences-file write.
Acceptance is not completion. Shared instructions are task data, not permission.
Private asset URLs are not automatically portable or shared; wait for offered
image-pack import/remapping before claiming those images work for teammates.


## Large CSS without a large generated tool argument

Write big themes to a UTF-8 file in the agent's admitted workspace first. Then use
`settings action=appearance operation=apply|save css_file={crew_id:"<Crew ID>",
path:"theme.css"}` (native appearance tool: omit action). Other theme fields may
still be passed in `theme`, but do not combine css_file and theme.css. The server
reads the file through normal Crew file admission and checks the same128KiB byte
cap. No absolute host paths or URLs. Saved themes are not source files.

The bridge error `arguments must be a JSON object: unexpected end of JSON input`
means an incomplete JSON-string argument, not a theme-size rejection. Use an
object for arguments, or the file route, not hand-escaped nested JSON.

Filtering happens only in the browser, not on save: the tool's save acknowledgement
is not a report that every declaration rendered. It removes relative, protocol-
relative, javascript and escaped-resource values, expression()/behavior/-moz-binding,
and common control-hiding declarations (display:none, visibility:hidden/collapse,
content-visibility:hidden, pointer-events:none, zero opacity, interactivity:inert;
decorative ::before/::after rules may be noninteractive). Invalid/unsupported rules
can also be discarded by CSSOM. HTTPS font imports retain font faces only, never
remote app selectors; failed imports are skipped. Scripts are never permitted.
Keep controls visible; arbitrary CSS is not a complete layout sandbox.

One real-shaped call: after your normal write tool saves `themes/itachi.css` in
the Crew folder, call `settings action=appearance operation=save name=Itachi
css_file={crew_id:"<your Crew ID>",path:"themes/itachi.css"}`. Paths are relative
to the Crew's native working folder on this Mac; do not add `_files/`, `.agentworks/`
or an absolute host path. The server maps the admitted local folder internally.


## Passive video backgrounds (direct HTTPS source)

When the live guide offers `background_video`, an agent can apply/save
`{url:"https://example.com/space.mp4",dim:0.7,playback_rate:0.5}` in that field.
A null clears it. Direct HTTPS media only, no webpage URLs, credentials, file://
paths or theme JavaScript. Dim0.35–0.95, speed0.25–1. Playback is muted/looping;
hidden pauses; reduced motion keeps a still frame. A failed load/unsupported
codec keeps the CSS/palette fallback. Actual codec support depends on this app's
browser; verify the video on the person's Mac, not only the saved settings.

Use translucent materials to reveal video. Blur is disabled on common app
surfaces over a decoded video; do not add blur in custom CSS. The main CSS
background-image is suppressed while decoded video is present and restored on
failure/removal. Keep a short720p/1080p clip, not a full4K screensaver. No claim
that decoding is cost-free. Prefer an owned/licensed source; a personal media
URL may reveal a request to its host. Video is not bundled with JSON export.
Mac wallpaper import/copy/transcoding is NOT delivered by this first slice:
never claim a file:// path or a video already in macOS will play automatically.

## Common design and verification questions

**Dark-only art:** a theme's mode and CSS selectors are separate. Scope art with
`html.dark [data-app-ambient]::before` (and its animation rules), not an unqualified
ambient hook. Put the light variant under `html.light`; do not assume changing
mode disables unqualified CSS. The same applies to main background images.

**Blur versus motion:** for a still nebula photograph, panel opacity0.8/blur6 is
reasonable. If stars or a video move underneath that panel, set panel/popover/
sidebar/composer blur0 and retain tint with opacity0.85–0.95. Animate the stars
with transform/opacity instead of animating blur or shadows. A screenshot shows
readability, not GPU cost. Video playback also suppresses common backdrop blur.

**Loader surfaces:** `loader.style` is one personal shared choice, not a separate
style per screen. Ring/orbit read well at tiny sidebar agent marks; dots suit
inline working text; bar is clearer at larger page-loading sizes; pulse is the
quiet option. Check the chosen style at both tiny marks and larger loading
states. Don't add a border/shadow/animation to every running-state glyph: target
`[data-theme-row="agent"]:has([data-theme-agent-state="running"])` for a row
highlight. The inner loader owns its own animation. No loader infers execution.

**Draft before apply:** write/export candidate theme JSON or `import`/`duplicate`
it under a draft name; these do not change the live look. The minimal picker can
apply a saved draft when chosen. There is no agent-only temporary preview API:
`apply` persists. A mock preview is not proof of the real app. Don't call a draft
visually verified until it is actually applied and inspected; preserve the prior
saved name for the person's return choice.

**Screenshot/focus the Mac app:** discover `computer_use`, read crew-laptop,
start with status, then guide and guide command=platform. Use list-apps and the
exact returned process name (a dev app may be Electron, not AgentWorks). Take a
screenshot with run command=screenshot args=["--app","<exact app name>"] and read
the returned private image path. For structure, snapshot that same app; don't
capture another computer/account and call it proof. Keep desktop screenshots
private. Focusing/physical input requires the person's permission and the
installed guide's headed/window command; screenshot reading needs no focus.
If no authorized route exists, ask the person to open the app and send a picture.
For motion inspect two frames, plus hidden/reduced-motion behavior; never restart
the app merely to inspect a theme.

Styling may never hide the sharing card’s summary or controls; see `panel-access` → `references/team/SKILL.md` for the one wording reference.
## Import a downloaded Mac aerial as a personal clip

When offered, `mac_wallpapers` lists downloaded Apple aerials on this local Mac
as opaque IDs (no arbitrary host paths). Use `import_mac_wallpaper wallpaper_id=`
with a chosen ID to make a personal MP4 with macOS avconvert. It first tries
30 seconds at 720p; oversized results automatically retry 15 seconds at 720p,
then 15 or 8 seconds at 480p, all within one 90-second import deadline and the
24 MiB limit. The reply states the actual duration and resolution. Do not ask
the person to download another aerial just because the first attempt was large. No
extra codec install, upload to a remote service or change to the original or
macOS wallpaper. The returned asset_id is not an automatic theme change:
apply/save `background_video:{asset_id:"<returned id>",dim:0.7,playback_rate:0.5}`.
Use URL or asset_id, never both. When switching sources in a partial patch, clear
the other source field with null in the same patch (url:null or asset_id:null).
background_video:null clears playback. Keep other custom fields.

Only the OS owner's admitted local account can list/import/read these clips;
sharing a Crew or possessing a project/device credential doesn't grant that.
If no aerial is downloaded, the person downloads their choice in macOS Settings
first. Existing files may be scenery/cities, not space: never invent their scene.
The private clip is fetched with normal account authorization into a temporary
browser blob URL; account/theme changes cancel it and revoke it. No login token
in a media URL. Imported media is personal data, never bundled in product code.
JSON export references the asset on this installation; it does not pack/share
the movie, and import on another account/computer does not transfer the file.
