import { defaultAppearance, type AppearanceTheme } from '../../stores/appearanceTheme'

// Static hull artwork. Motion is confined to two narrow light strips, never the
// full-window background or the glass beneath text. No scene/script is added.
export function starshipTheme(): AppearanceTheme {
  return {
    ...defaultAppearance(), name: 'Starship', mode: 'light',
    colors: { light: {
      background: '#e9f1f3', foreground: '#20383f', card: '#f9fdfe',
      muted: '#e0edf0', muted_foreground: '#526d75', border: '#b6d1d8',
      accent: '#087e8b', sidebar: '#eff7f8',
    } },
    materials: {
      page: { opacity: 0 }, panel: { opacity: .76, blur: 6 },
      sidebar: { opacity: .82, blur: 0 }, composer: { opacity: .88, blur: 0 },
      popover: { opacity: .96, blur: 0 },
    },
    css: STARSHIP_CSS,
  }
}

export const STARSHIP_CSS = `
:root.light [data-app-main] {
  background-color: #e9f1f3;
  background-image:
    linear-gradient(128deg, transparent 53%, #a4bdc538 53.1%, #f8fcfde0 53.4%, transparent 54%),
    linear-gradient(52deg, transparent 78%, #98b6c13b 78.1%, #fff9 78.5%, transparent 79%),
    radial-gradient(ellipse at 88% 24%, #9fdce357, transparent 48%),
    repeating-linear-gradient(0deg, transparent 0 95px, #8aabb514 96px, transparent 97px),
    linear-gradient(115deg, #fbfefe, #e8f1f4 60%, #d5e6ec);
}
/* The panoramic bridge window: fixed art, faint enough to read over. */
:root.light [data-app-main]::before {
  content: ''; pointer-events: none; position: absolute; z-index: -1;
  top: 8%; right: 2%; width: min(58vw, 960px); height: 42%;
  border: 10px solid #f9fdfee6; border-radius: 100px 20px 100px 20px;
  box-shadow: 0 0 0 1px #90b9c34a, inset 0 0 0 2px #6aabb34d, 0 12px 40px #61869217;
  background:
    radial-gradient(circle at 76% 43%, #faffff 0 2px, transparent 3px),
    radial-gradient(circle at 64% 27%, #fff 0 1px, transparent 2px),
    radial-gradient(circle at 88% 19%, #fff 0 1px, transparent 2px),
    radial-gradient(circle at 37% 64%, #fff 0 1px, transparent 2px),
    radial-gradient(circle at 52% 34%, #fff 0 1px, transparent 2px),
    radial-gradient(ellipse at 87% 60%, #8cb7d183, transparent 45%),
    radial-gradient(ellipse at 30% 10%, #b1e5db99, transparent 65%),
    linear-gradient(155deg, #d2e6ee, #c5dbe9 55%, #eaf7f9);
}
:root.light [data-theme-surface="panel"],
:root.light [data-chat-composer-card] {
  border-color: #a6cbd2;
  box-shadow: inset 0 1px #ffffffd9, 0 12px 32px #294f6010;
}
:root.light [data-chat-composer-card] { border-radius: 18px; position: relative; }
:root.light [data-theme-agent-selected="true"] { background-color: #d5edf1b3; }
:root.light [data-theme-agent-state="running"] {
  border-inline-start: 2px solid #118d9b;
  background-color: #c7e9ed99;
}
:root.light [data-theme-agent-state="running"] svg { color: #087e8b; }
/* Above opaque page wrappers, below popovers; the hook never intercepts input.
   These small strips have no backdrop blur or animated shadow/filter. */
:root.light [data-app-ambient]::before,
:root.light [data-chat-composer-card]::before {
  content: ''; position: absolute; pointer-events: none; height: 2px;
  background: linear-gradient(90deg, transparent, #20b5c6, #b9f8ff, transparent);
  transform-origin: center; animation-play-state: paused;
}
:root.light [data-app-ambient]::before { top: 78px; right: 8%; width: 200px; opacity: .65; }
:root.light [data-chat-composer-card]::before { top: -1px; left: 10%; width: 80%; opacity: .8; }
@keyframes aw-starship-transit { from { transform: translateX(-28px); opacity: .35; } to { transform: translateX(28px); opacity: .85; } }
@keyframes aw-starship-ready { from { transform: scaleX(.35); opacity: .4; } to { transform: scaleX(.85); opacity: .9; } }
@media (prefers-reduced-motion: no-preference) {
  :root.light [data-app-ambient]::before { animation: aw-starship-transit 14s ease-in-out infinite alternate; animation-play-state: paused; }
  :root.light [data-chat-composer-card]::before { animation: aw-starship-ready 3s ease-in-out infinite alternate; animation-play-state: paused; }
  :root.light[data-appearance-visible="true"] [data-app-ambient]::before { animation-play-state: running; }
  :root.light[data-appearance-visible="true"]:has([data-theme-agent-state="running"], [data-theme-turn-live="true"], [data-theme-team-working="true"]) [data-chat-composer-card]::before { animation-play-state: running; }
}
@media (max-width: 640px) {
  :root.light [data-app-main]::before { width: 85%; right: -12%; opacity: .55; }
  :root.light [data-app-ambient]::before { width: 100px; }
}
`
