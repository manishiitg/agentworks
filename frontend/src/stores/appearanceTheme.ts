export const INTERFACE_FONTS = [
  { id: 'system', label: 'System default', family: '-apple-system, BlinkMacSystemFont, system-ui, "Segoe UI", sans-serif' },
  { id: 'jetbrains-mono', label: 'JetBrains Mono', family: '"JetBrains Mono", monospace' },
  { id: 'fira-code', label: 'Fira Code', family: '"Fira Code", monospace' },
  { id: 'ibm-plex-mono', label: 'IBM Plex Mono', family: '"IBM Plex Mono", monospace' },
  { id: 'source-code-pro', label: 'Source Code Pro', family: '"Source Code Pro", monospace' },
] as const
export const APPLE_MONO_FONT = '"AgentWorks SF Mono Terminal", "SF Mono Terminal", "SFMonoTerminal-Regular", "SFMono-Regular", "SF Mono", Menlo, Monaco, Consolas, monospace'
export const CODE_FONTS = [{ id: 'system-mono', label: 'System monospace', family: APPLE_MONO_FONT }, ...INTERFACE_FONTS.filter(font => font.id !== 'system')]
export const COLOUR_ROLES = ['background', 'foreground', 'card', 'muted', 'muted_foreground', 'border', 'accent', 'sidebar', 'working', 'waiting', 'done', 'error', 'loader'] as const
export type ColourRole = typeof COLOUR_ROLES[number]
export const CODE_ROLES = ['background', 'foreground', 'keyword', 'string', 'number', 'comment', 'function', 'operator', 'terminal_background', 'terminal_foreground', 'terminal_cursor', 'ansi_black', 'ansi_red', 'ansi_green', 'ansi_yellow', 'ansi_blue', 'ansi_magenta', 'ansi_cyan', 'ansi_white', 'ansi_bright_black', 'ansi_bright_red', 'ansi_bright_green', 'ansi_bright_yellow', 'ansi_bright_blue', 'ansi_bright_magenta', 'ansi_bright_cyan', 'ansi_bright_white'] as const
export type CodeRole = typeof CODE_ROLES[number]
export type AppearanceProfile = 'light' | 'dark'
export type AppearanceMode = 'light' | 'dark' | 'system' | 'schedule'
export const APPEARANCE_MODES: readonly AppearanceMode[] = ['light', 'dark', 'system', 'schedule']
export interface AppearanceTheme {
  name: string
  mode: AppearanceMode
  light_at: string
  dark_at: string
  interface_font: string
  code_font: string
  interface_size: number
  code_size: number
  colors: Partial<Record<AppearanceProfile, Partial<Record<ColourRole, string>>>>
  code: Partial<Record<AppearanceProfile, Partial<Record<CodeRole, string>>>>
  css: string
  /** How see-through each surface is; decoration lives only in personal CSS. */
  materials?: Record<string, { opacity?: number; blur?: number }>
  loader?: { style?: 'ring' | 'dots' | 'pulse' | 'orbit' | 'bar'; duration_ms?: number } | null
}
/** What the server offers (appearance.go's guide): fonts, code fonts and colour roles are checked against this list, not a copy. */
export interface AppearanceGuide { fonts?: string[]; code_fonts?: string[]; colour_roles?: string[]; code_roles?: string[]; modes?: string[]; material_surfaces?: Record<string, string> }
export interface AppearanceDocument { current: AppearanceTheme; saved: AppearanceTheme[]; guide?: AppearanceGuide }
export const defaultAppearance = (): AppearanceTheme => ({ name: 'Default', mode: 'dark', light_at: '07:00', dark_at: '19:00', interface_font: 'system', code_font: 'system-mono', interface_size: 14, code_size: 12, colors: {}, code: {}, css: '', materials: {} })

const TIME_RE = /^([01][0-9]|2[0-3]):[0-5][0-9]$/
export function minutesOfDay(value: string): number | null {
  if (!TIME_RE.test(value)) return null
  const [h, m] = value.split(':').map(Number)
  return h * 60 + m
}
/** Short label for the Default entry: explicit Light/Dark, or the auto policy in brackets. */
export function defaultThemeLabel(theme: AppearanceTheme): string {
  if (theme.name !== 'Default') return theme.name
  if (theme.mode === 'light') return 'Default Light'
  if (theme.mode === 'dark') return 'Default Dark'
  if (theme.mode === 'system') return 'Default (System)'
  return `Default (Schedule ${theme.light_at}-${theme.dark_at})`
}

/** Effective light/dark for a theme on this device. System uses the OS setting; schedule uses local light_at/dark_at. */
export function resolveAppearanceProfile(theme: AppearanceTheme, systemDark: boolean, now = new Date()): AppearanceProfile {
  if (theme.mode === 'light') return 'light'
  if (theme.mode === 'dark') return 'dark'
  if (theme.mode === 'system') return systemDark ? 'dark' : 'light'
  const light = minutesOfDay(theme.light_at)
  const dark = minutesOfDay(theme.dark_at)
  if (light === null || dark === null || light === dark) return systemDark ? 'dark' : 'light'
  const at = now.getHours() * 60 + now.getMinutes()
  const lightSpan = (dark - light + 1440) % 1440
  const sinceLight = (at - light + 1440) % 1440
  return sinceLight < lightSpan ? 'light' : 'dark'
}
export const interfaceFontFamily = (id: string) => INTERFACE_FONTS.find(font => font.id === id)?.family ?? INTERFACE_FONTS[0].family
export const codeFontFamily = (id: string) => CODE_FONTS.find(font => font.id === id)?.family ?? APPLE_MONO_FONT

export function validateTheme(value: unknown, guide: AppearanceGuide = {}): AppearanceTheme {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Choose a theme JSON file.')
  const v = value as AppearanceTheme
  const keys = ['name', 'mode', 'light_at', 'dark_at', 'interface_font', 'code_font', 'interface_size', 'code_size', 'colors', 'code', 'css', 'materials', 'loader']
  if (Object.keys(v).some(key => !keys.includes(key)) || typeof v.name !== 'string' || !v.name.trim() || v.name.length > 60 || !(guide.modes ?? APPEARANCE_MODES).includes(v.mode) || typeof v.light_at !== 'string' || typeof v.dark_at !== 'string' || minutesOfDay(v.light_at) === null || minutesOfDay(v.dark_at) === null || (v.mode === 'schedule' && v.light_at === v.dark_at) || typeof v.interface_font !== 'string' || typeof v.code_font !== 'string' || (guide.fonts ?? INTERFACE_FONTS.map(font => font.id)).indexOf(v.interface_font) < 0 || (guide.code_fonts ?? CODE_FONTS.map(font => font.id)).indexOf(v.code_font) < 0 || !Number.isInteger(v.interface_size) || v.interface_size < 12 || v.interface_size > 20 || !Number.isInteger(v.code_size) || v.code_size < 10 || v.code_size > 20 || typeof v.css !== 'string' || new TextEncoder().encode(v.css).length > 20000 || !v.colors || typeof v.colors !== 'object' || Array.isArray(v.colors) || !v.code || typeof v.code !== 'object' || Array.isArray(v.code)) throw new Error('Use the offered fonts, sizes, colours and at most 20 KB of CSS.')
  if (v.loader != null) {
    const loader = v.loader
    if (typeof loader !== 'object' || Array.isArray(loader) || Object.keys(loader).some(key => !['style', 'duration_ms'].includes(key)) ||
      (loader.style !== undefined && !['ring', 'dots', 'pulse', 'orbit', 'bar'].includes(loader.style)) ||
      (loader.duration_ms !== undefined && (!Number.isInteger(loader.duration_ms) || loader.duration_ms < 400 || loader.duration_ms > 4000))) throw new Error('Choose an offered loading indicator and duration from 400 to 4000 ms.')
  }
  const roles: readonly string[] = guide.colour_roles ?? COLOUR_ROLES
  const codeRoles: readonly string[] = guide.code_roles ?? CODE_ROLES
  if (v.materials !== undefined) {
    if (!v.materials || typeof v.materials !== 'object' || Array.isArray(v.materials)) throw new Error('Use a materials object.')
    for (const [surface, material] of Object.entries(v.materials)) {
      const level = (value: unknown, max: number) => value === undefined || (typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= max)
      if ((guide.material_surfaces && !(surface in guide.material_surfaces)) || !material || typeof material !== 'object' || !level(material.opacity, 1) || !level(material.blur, 24)) throw new Error('Use an offered surface with opacity 0 to 1 and blur 0 to 24.')
    }
  }
  for (const [mode, colors] of Object.entries(v.colors)) {
    if (!['light', 'dark'].includes(mode) || !colors || typeof colors !== 'object' || Array.isArray(colors)) throw new Error('Colours belong to Light or Dark.')
    for (const [role, color] of Object.entries(colors)) if (!roles.includes(role) || typeof color !== 'string' || !/^#[\da-f]{6}$/i.test(color)) throw new Error('Use an offered colour role and six-digit hex colour.')
  }
  for (const [mode, colors] of Object.entries(v.code)) {
    if (!['light', 'dark'].includes(mode) || !colors || typeof colors !== 'object' || Array.isArray(colors)) throw new Error('Code colours belong to Light or Dark.')
    for (const [role, color] of Object.entries(colors)) if (!codeRoles.includes(role) || typeof color !== 'string' || !/^#[\da-f]{6}$/i.test(color)) throw new Error('Use an offered code colour role and six-digit hex colour.')
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
