import { codeFontFamily, hexToHsl, interfaceFontFamily, type AppearanceProfile, type AppearanceTheme, type ColourRole } from '../stores/appearanceTheme'
import { appearanceControlCSS } from './appearanceControls'

/** Every custom role fans out here, not through component-specific CSS patches. */
const ROLE_TOKENS: Record<ColourRole, readonly string[]> = {
  background: ['--background'],
  foreground: ['--foreground', '--card-foreground', '--popover-foreground', '--secondary-foreground', '--accent-foreground'],
  card: ['--card', '--popover'],
  muted: ['--muted', '--secondary', '--accent'],
  muted_foreground: ['--muted-foreground'],
  border: ['--border', '--input'],
  accent: ['--primary', '--ring', '--info', '--chart-1'],
  sidebar: ['--appearance-sidebar'],
  working: ['--info', '--working-accent'],
  waiting: ['--warning'],
  done: ['--success'],
  error: ['--destructive'],
  loader: [], // A complete CSS colour, not an HSL triplet (the glyph inherits the caller when absent).
  user_background: [], user_foreground: [], assistant_background: [], assistant_foreground: [],
  icon: [], panel_border: [], card_border: [], composer_border: [], input_border: [], selected_agent_border: [], running_agent_border: [], // Shared control sheet, not global palette substitutions.
}

// The saved schema has no action-label colour: derive a legible one for both
// very light accents (e.g. mint) and very dark accents rather than keeping white.
export function actionForeground(hex: string): string {
  const linear = [1, 3, 5].map(index => {
    const channel = parseInt(hex.slice(index, index + 2), 16) / 255
    return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  })
  const luminance = linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722
  return (luminance + 0.05) / 0.05 >= 1.05 / (luminance + 0.05) ? '#000000' : '#ffffff'
}

export function appearanceTokens(theme: AppearanceTheme, profile: AppearanceProfile): Record<string, string> {
  const tokens: Record<string, string> = {
    '--app-font': interfaceFontFamily(theme.interface_font),
    '--chat-font': interfaceFontFamily(theme.interface_font),
    '--app-font-size': `${theme.interface_size}px`,
    '--app-code-font': codeFontFamily(theme.code_font),
    '--app-code-size': `${theme.code_size}px`,
  }
  const colors = theme.colors[profile] ?? {}
  for (const role of Object.keys(ROLE_TOKENS) as ColourRole[]) {
    const hex = colors[role]
    if (!hex) continue
    for (const token of ROLE_TOKENS[role]) tokens[token] = hexToHsl(hex)
  }
  for (const [role, token] of [['working', '--info-foreground'], ['waiting', '--warning-foreground'], ['done', '--success-foreground'], ['error', '--destructive-foreground']] as const) {
    if (colors[role]) tokens[token] = hexToHsl(actionForeground(colors[role]!))
  }
  if (colors.loader) tokens['--aw-loader-color'] = colors.loader
  if (theme.loader?.duration_ms) tokens['--aw-loader-duration'] = `${theme.loader.duration_ms}ms`
  if (colors.accent) tokens['--primary-foreground'] = hexToHsl(actionForeground(colors.accent))
  // Material fields and custom CSS use the same background-only tokens.
  for (const [surface, material] of Object.entries(theme.materials ?? {})) {
    if (typeof material.opacity === 'number') tokens[`--aw-${surface}-opacity`] = String(material.opacity)
    if (typeof material.blur === 'number') tokens[`--aw-${surface}-backdrop`] = material.blur > 0 ? `blur(${material.blur}px)` : 'none'
  }
  return tokens
}

/** The provider owns these inline tokens and optional control sheet, including cleanup. */
export function applyAppearanceTokens(root: HTMLElement, theme: AppearanceTheme, profile: AppearanceProfile): () => void {
  const tokens = appearanceTokens(theme, profile)
  root.dataset.appearanceLoader = theme.loader?.style || 'ring'
  for (const [name, value] of Object.entries(tokens)) root.style.setProperty(name, value)
  const css = appearanceControlCSS(theme, profile)
  const sheet = css ? new CSSStyleSheet() : null
  const doc = root.ownerDocument
  if (sheet) {
    sheet.replaceSync(css)
    doc.adoptedStyleSheets = [...doc.adoptedStyleSheets, sheet]
  }
  return () => {
    delete root.dataset.appearanceLoader
    for (const name of Object.keys(tokens)) root.style.removeProperty(name)
    if (sheet) doc.adoptedStyleSheets = doc.adoptedStyleSheets.filter(item => item !== sheet)
  }
}
