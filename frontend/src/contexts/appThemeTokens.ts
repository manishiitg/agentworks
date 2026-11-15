import { codeFontFamily, hexToHsl, interfaceFontFamily, type AppearanceProfile, type AppearanceTheme, type ColourRole } from '../stores/appearanceTheme'

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
  for (const role of Object.keys(colors) as ColourRole[]) {
    const hex = colors[role]!
    for (const token of ROLE_TOKENS[role]) tokens[token] = hexToHsl(hex)
  }
  if (colors.accent) tokens['--primary-foreground'] = hexToHsl(actionForeground(colors.accent))
  return tokens
}

/** The appearance provider owns these inline tokens, including their cleanup. */
export function applyAppearanceTokens(root: HTMLElement, theme: AppearanceTheme, profile: AppearanceProfile): () => void {
  const tokens = appearanceTokens(theme, profile)
  for (const [name, value] of Object.entries(tokens)) root.style.setProperty(name, value)
  return () => { for (const name of Object.keys(tokens)) root.style.removeProperty(name) }
}
