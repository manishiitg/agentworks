# Appearance

Change how the app looks for the signed-in person with the `appearance` tool (`settings action=appearance` in chat and over MCP). It changes only their interface, never a project, an agent run or another person's screen.

**Agentic first.** Appearance shows only Default Light, Default Dark and saved personal themes with apply/delete. “Ask an agent to change the look” opens an existing own agent chat with a starter draft; it never sends automatically. Do not build an editor, add product presets or edit application source for a personal look. Read the live offered guide first; do not invent fields or promise an unsupported customization.

**How to work.** Read first: you get `current`, the saved themes and the `guide` (fonts, code fonts, colour roles, scenes, surfaces, CSS hooks). Then:
- `apply` previews a change and keeps it as the current look. It merges into the current theme; send `replace: true` with a full theme to start from Default, so colours you leave out are cleared.
- `save` stores it under a name (up to 60 characters, 20 themes) and applies it. `apply` with `name` and no `theme` selects one of your saved themes. `delete` removes a saved name, leaving the current look in place. `reset` returns to Default and keeps the saved themes.
- `export` with `name` returns that saved theme object; omit `name` for current. Save the returned `theme` object as JSON. `import` receives that object in `theme`, validates it from Default (not merged into current) and saves it without applying; optional `name` gives it a different name. Images are not supported in this format yet.
- `duplicate` takes the saved `name` (or current when omitted) and a fresh `new_name`; it saves a copy without applying. `rename` takes saved `name` and `new_name`, refusing a different existing name; an applied name follows its rename. No account or host path is accepted.
- Mutation replies are short: summary, current name/mode and changed field names. They do not repeat saved CSS or prove visual success. Use `read` for current/saved content and the live guide.
- Say what you changed in plain words, and offer `reset` if they do not like it. Before claiming visual success, inspect a screenshot of the actual app on the correct computer/account using an authorized route. If that is unavailable, say the preference was saved but the look is unverified, and ask the person to look.

**Current offered fields.** Light or dark mode; interface and code fonts and sizes; the offered colour roles as `#RRGGBB` (the `accent` colour is the action colour); `materials` per surface (`page`, `panel`, `popover`, `sidebar`, `composer`) with `opacity` 0 to 1 and, for everything but the page, `blur` 0 to 24 px; and short custom CSS. Prefer these fields to CSS. Existing saved scene looks still run while they are converted to personal CSS; for a new custom background use `scene: "none"`, not another shipped scene. Do not claim that icons, chat/layout, code/ANSI, auto-switch or image-asset operations exist until the live guide offers them.

**Loading and status colours.** Use `loader.style` (`ring`, `dots`, `pulse`, `orbit`, `bar`) and `loader.duration_ms` (400–4000; lower is faster); `loader: null` restores the default. Set `colors.light` / `colors.dark` roles `working`, `waiting`, `done`, `error` and optionally `loader`. The shared loader uses the loader colour, falling back to the caller's text or working colour so action-button glyphs stay readable. It covers core agent marks, turn working lines, workspace/modal loading and common controls; some legacy feature spinners have not moved yet. It is decorative: changing it never starts, stops or infers work. It pauses when the app is hidden or reduced motion is enabled; the status text stays. Use `[data-theme-loader]` for additional small CSS decoration, not timers or scripts.

**Make it readable.**
- Text must stand out from its background: keep a strong difference in lightness between `foreground` and `background`, and between `muted_foreground` and `card`. Avoid mid-grey on mid-grey and saturated colours on each other.
- The label on action buttons is chosen for the accent colour automatically; still pick an accent that is clearly lighter or darker than the background, not close to it.
- Lowering a surface's opacity only fades its background; text stays solid. Do not lower it so far that text sits on a busy image.

**Keep it fast.**
- Prefer a still custom background. Keep motion cheap and separate from agent execution.
- Do not blur over a moving scene: the server refuses a blur with a scene. Raise `opacity` for a frosted look instead.
- Keep custom CSS short (the limit is 20,000 bytes; a few rules is plenty) and never hide, cover or shrink controls, warnings or the message box.
- No external resources: `@import`, external `url()` and `image-set()` are removed. Animate only inside `@media (prefers-reduced-motion: no-preference)`.

**CSS hooks.** Use these stable hooks, never generated class names: `[data-app-ambient]` (an empty layer over the whole window that never takes clicks), `[data-app-main]`, `nav[aria-label="Workspace navigation"]`, `[data-chat-find-scope]`, `.conversation-markdown`, `[data-testid="transcript-user-bubble"]`, `[data-chat-composer-card]`, `[data-product-settings]`, `[role="menu"]`, `[role="dialog"]`, and the state marks the guide lists (`[data-theme-agent-state]`, `[data-theme-team-working]`, `[data-theme-turn-live]`, and so on). Quiet is not proof an agent is idle.

Example, a rounder message box and a quiet glow on the working agent:

```css
[data-chat-composer-card] { border-radius: 18px; }
@media (prefers-reduced-motion: no-preference) {
  [data-theme-agent-state="running"] { box-shadow: inset 2px 0 hsl(var(--primary)); }
}
```

**A background animation people can see.** Opaque nested surfaces can hide a background on `[data-app-main]`. Explicit translucent materials activate transparent wrappers in Light and Dark; keep enough tint for readable text. Shared materials set background-color only, so a custom gradient is not erased. Put the animation on `[data-app-ambient]`: it covers the window, is always visible and never blocks clicks. Keep it faint (colours at 6-15% opacity, never a solid fill) so text stays readable, and animate `transform` or `opacity` of a pseudo-element rather than `background-position` (that repaints the whole window every frame):

```css
@media (prefers-reduced-motion: no-preference) {
  [data-app-ambient]::before {
    content: ""; position: absolute; inset: -20%;
    background: radial-gradient(600px 400px at 20% 20%, hsl(var(--primary) / .12), transparent 70%),
                radial-gradient(700px 450px at 80% 80%, hsl(var(--accent) / .12), transparent 70%);
    animation: drift 24s ease-in-out infinite alternate;
    animation-play-state: paused;
  }
  html[data-appearance-visible="true"] [data-app-ambient]::before { animation-play-state: running; }
  @keyframes drift { to { transform: translate3d(6%, 4%, 0) scale(1.08); } }
}
```

Other things that decide whether a change shows:
- Use shared semantic fields and stable hooks. The old Light-only opaque composer fill has been removed; do not add blanket important rules. Prefer small transform/opacity motion, not full-window background-position, shadows or filters.
- Make glows strong enough to see: shadow and glow colours at 25% opacity or more, not 5-10%.
- After saving, look at a screenshot of the real window. If the animation is not there, it is almost always hidden under a solid surface or too faint.
