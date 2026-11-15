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
CSS glass reveals the app scene, not the Mac desktop: native window vibrancy is a
separate desktop implementation. Authored report/force-light surfaces reset material
variables and retain their own palette. Code palettes and game artwork are separate.

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

## Deep Space (no game)

`:root.dark { --aw-background: deep-space; }` selects the shipped passive starfield,
nebula and five-planet system. It is lazy-mounted only for the authenticated app,
with no game controls, input handlers, canvas/RAF loop, timers or network requests.
Its bounded CSS animations are paused by default and resume only while a mounted
`agent-state=running`, `turn-live=true`, `tools-running=true` or
`team-working=true` hook exists. CSS `:has()` consumes those existing hooks; no
observer or status copy is added. Hidden documents pause, reduced-motion disables
all scene animation, and unmount removes the visibility listener. Missing hooks
mean no animation, not a claim that every agent is idle. Historical tool cards do
not activate it. Idle phases freeze rather than restart on each tool call.
Light/Default clear the scene; the prior Neon Orbit preset remains separate.
There is no measured CPU/GPU/frame-time claim from choosing CSS over canvas.

## Visible agent-authored CSS backgrounds (Light or Dark)

Use `scene: "none"` and explicit translucent materials, e.g.
`materials.panel.opacity: .6` and `materials.composer.opacity: .7`. Any offered
surface with opacity below 1 activates the same transparent app wrappers
as shipped scenes, also in Light, without a separate page-material trick. Then style the stable `[data-app-main]` hook,
not a deeply nested generated class. Give its gradient enough contrast to see;
1–2% alpha under opaque cards is not a visible background. Keep panels/composer
around .7–.9 opacity so text remains legible; never fade the whole element.
The semantic panel/composer materials own both Light and Dark, with no legacy
Light-only background override. Shared materials set background-color only; they
do not reset a custom gradient/background-image with an important shorthand. Put decorative keyframes inside
`@media (prefers-reduced-motion: no-preference)` and prefer small transform/opacity
motion to a full-window background-position repaint. Do not add blur over moving
artwork. Existing running hooks can gate the animation; historical tool rows
must not. Saved CSS is adopted after apply; material/scene-only edits reproject
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
