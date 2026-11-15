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
not reset the current look. No manual editor or product preset cards.

Ask an agent chooses an existing own writable Crew tab, preferring the active one,
and prepends a starter request to its existing draft via the current draft helper.
It never sends, auto-runs, creates a chat or writes appearance settings itself.
Watched, teammate, setup and scheduled/bot/execution tabs are not candidates;
backend appearance and Send admission remain authoritative. If there is no own
chat, explain the next step rather than opening a foreign chat.

Only Default Light and Default Dark ship with the app. Personal colours, materials
and CSS supply every other look; no preset factory, scene enum, canvas/game engine
or hidden renderer catalog remains. Former looks are exported/converted through
the caller's appearance tools before source removal, never served as built-in
presets. No executable gameplay is represented as CSS. Future tool fields are
listed only when implemented, not from a design mock.

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
