# Appearance

Change the signed-in person's own app look with the `appearance` tool (`settings action=appearance` in chat and over MCP). It changes only their interface, never a project, an agent run or another person's screen.

**How to work.** Read first: you get `current`, the saved themes and the `guide` (fonts, code fonts, colour roles, scenes, surfaces, CSS hooks). Then:
- `apply` previews a change and keeps it as the current look. It merges into the current theme; send `replace: true` with a full theme to start from Default, so colours you leave out are cleared.
- `save` stores it under a name (up to 60 characters, 20 themes) and applies it. `delete` removes a saved name. `reset` returns to Default and keeps the saved themes.
- Say what you changed in plain words, and offer `reset` if they do not like it. You cannot see their screen: do not claim it looks right, ask them to look.

**What you can set.** Light or dark mode; interface and code fonts and sizes; eight colour roles as `#RRGGBB` (the `accent` colour is the action colour); a background `scene` (`none`, `neon-orbit`, `deep-space`; dark mode only); `materials` per surface (`page`, `panel`, `popover`, `sidebar`, `composer`) with `opacity` 0 to 1 and, for everything but the page, `blur` 0 to 24 px; and short custom CSS. Prefer these fields to CSS.

**Make it readable.**
- Text must stand out from its background: keep a strong difference in lightness between `foreground` and `background`, and between `muted_foreground` and `card`. Avoid mid-grey on mid-grey and saturated colours on each other.
- The label on action buttons is chosen for the accent colour automatically; still pick an accent that is clearly lighter or darker than the background, not close to it.
- Lowering a surface's opacity only fades its background; text stays solid. Do not lower it so far that text sits on a busy image.

**Keep it fast.**
- Prefer a still background (`none`). Scenes move: `neon-orbit` and `deep-space` animate only while an agent is working and stay still otherwise.
- Do not blur over a moving scene: the server refuses a blur with a scene. Raise `opacity` for a frosted look instead.
- Keep custom CSS short (the limit is 20,000 bytes; a few rules is plenty) and never hide, cover or shrink controls, warnings or the message box.
- No external resources: `@import`, external `url()` and `image-set()` are removed. Animate only inside `@media (prefers-reduced-motion: no-preference)`.

**CSS hooks.** Use these stable hooks, never generated class names: `[data-app-main]`, `nav[aria-label="Workspace navigation"]`, `[data-chat-find-scope]`, `.conversation-markdown`, `[data-testid="transcript-user-bubble"]`, `[data-chat-composer-card]`, `[data-product-settings]`, `[role="menu"]`, `[role="dialog"]`, and the state marks the guide lists (`[data-theme-agent-state]`, `[data-theme-team-working]`, `[data-theme-turn-live]`, and so on). Quiet is not proof an agent is idle.

Example, a rounder message box and a quiet glow on the working agent:

```css
[data-chat-composer-card] { border-radius: 18px; }
@media (prefers-reduced-motion: no-preference) {
  [data-theme-agent-state="running"] { box-shadow: inset 2px 0 hsl(var(--primary)); }
}
```
