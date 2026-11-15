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
