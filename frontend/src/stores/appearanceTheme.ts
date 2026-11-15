export const INTERFACE_FONTS = [
  { id: 'system', label: 'System default', family: '-apple-system, BlinkMacSystemFont, system-ui, "Segoe UI", sans-serif' },
  { id: 'jetbrains-mono', label: 'JetBrains Mono', family: '"JetBrains Mono", monospace' },
  { id: 'fira-code', label: 'Fira Code', family: '"Fira Code", monospace' },
  { id: 'ibm-plex-mono', label: 'IBM Plex Mono', family: '"IBM Plex Mono", monospace' },
  { id: 'source-code-pro', label: 'Source Code Pro', family: '"Source Code Pro", monospace' },
] as const
export const APPLE_MONO_FONT = '"AgentWorks SF Mono Terminal", "SF Mono Terminal", "SFMonoTerminal-Regular", "SFMono-Regular", "SF Mono", Menlo, Monaco, Consolas, monospace'
export const CODE_FONTS = [{ id: 'system-mono', label: 'System monospace', family: APPLE_MONO_FONT }, ...INTERFACE_FONTS.filter(font => font.id !== 'system')]
export const COLOUR_ROLES = ['background', 'foreground', 'card', 'muted', 'muted_foreground', 'border', 'accent', 'sidebar'] as const
export type ColourRole = typeof COLOUR_ROLES[number]
export type AppearanceProfile = 'light' | 'dark'
export interface AppearanceTheme {
  name: string
  mode: AppearanceProfile
  interface_font: string
  code_font: string
  interface_size: number
  code_size: number
  colors: Partial<Record<AppearanceProfile, Partial<Record<ColourRole, string>>>>
  css: string
}
export interface AppearanceDocument { current: AppearanceTheme; saved: AppearanceTheme[] }
export const defaultAppearance = (): AppearanceTheme => ({ name: 'Default', mode: 'dark', interface_font: 'system', code_font: 'system-mono', interface_size: 14, code_size: 12, colors: {}, css: '' })
export const interfaceFontFamily = (id: string) => INTERFACE_FONTS.find(font => font.id === id)?.family ?? INTERFACE_FONTS[0].family
export const codeFontFamily = (id: string) => CODE_FONTS.find(font => font.id === id)?.family ?? APPLE_MONO_FONT
export const APPEARANCE_HOOKS = ['[data-app-main]', 'nav[aria-label="Workspace navigation"]', '[data-chat-find-scope]', '.conversation-markdown', '[data-testid="transcript-user-bubble"]', '[data-chat-composer-card]', '[data-product-settings]', '[role="menu"]', '[role="dialog"]']

export function validateTheme(value: unknown): AppearanceTheme {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Choose a theme JSON file.')
  const v = value as AppearanceTheme
  const keys = ['name', 'mode', 'interface_font', 'code_font', 'interface_size', 'code_size', 'colors', 'css']
  if (Object.keys(v).some(key => !keys.includes(key)) || typeof v.name !== 'string' || !v.name.trim() || v.name.length > 60 || !['light', 'dark'].includes(v.mode) || !INTERFACE_FONTS.some(font => font.id === v.interface_font) || !CODE_FONTS.some(font => font.id === v.code_font) || !Number.isInteger(v.interface_size) || v.interface_size < 12 || v.interface_size > 20 || !Number.isInteger(v.code_size) || v.code_size < 10 || v.code_size > 20 || typeof v.css !== 'string' || new TextEncoder().encode(v.css).length > 20000 || !v.colors || typeof v.colors !== 'object' || Array.isArray(v.colors)) throw new Error('Use the offered fonts, sizes, colours and at most 20 KB of CSS.')
  for (const [mode, colors] of Object.entries(v.colors)) {
    if (!['light', 'dark'].includes(mode) || !colors || typeof colors !== 'object' || Array.isArray(colors)) throw new Error('Colours belong to Light or Dark.')
    for (const [role, color] of Object.entries(colors)) if (!COLOUR_ROLES.includes(role as ColourRole) || typeof color !== 'string' || !/^#[\da-f]{6}$/i.test(color)) throw new Error('Use an offered colour role and six-digit hex colour.')
  }
  return structuredClone(v)
}

// Hex inputs become semantic HSL tokens, never arbitrary CSS declarations.
export function hexToHsl(hex: string): string {
  const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
  const max = Math.max(r, g, b), min = Math.min(r, g, b), delta = max - min, l = (max + min) / 2
  let h = 0, s = 0
  if (delta) { s = delta / (1 - Math.abs(2 * l - 1)); h = max === r ? ((g - b) / delta) % 6 : max === g ? (b - r) / delta + 2 : (r - g) / delta + 4 }
  return `${(h * 60 + 360) % 360} ${s * 100}% ${l * 100}%`
}
