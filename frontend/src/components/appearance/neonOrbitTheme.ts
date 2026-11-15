import { defaultAppearance, type AppearanceTheme } from '../../stores/appearanceTheme'

/** A saved personal theme, not a new project setting or executable theme script. */
export function neonOrbitTheme(): AppearanceTheme {
  return { ...defaultAppearance(), name: 'Neon Orbit', mode: 'dark', colors: { dark: {
    background: '#06111c', foreground: '#e0eee9', card: '#112330', muted: '#162b36',
    muted_foreground: '#9bb4bd', border: '#29434d', accent: '#7deac0', sidebar: '#0b1c29',
  } }, scene: 'neon-orbit', css: '[data-chat-composer-card] { border-radius: 18px; }' }
}
