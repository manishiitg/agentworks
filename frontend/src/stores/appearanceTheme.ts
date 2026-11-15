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
  /** The shipped background (dark mode only) and how see-through each surface is; see the server's guide. */
  scene?: string
  materials?: Record<string, { opacity?: number; blur?: number }>
}
/** What the server offers (appearance.go's guide): fonts, code fonts and colour roles are checked against this list, not a copy. */
export interface AppearanceGuide { fonts?: string[]; code_fonts?: string[]; colour_roles?: string[]; scenes?: string[]; material_surfaces?: Record<string, string> }
export interface AppearanceDocument { current: AppearanceTheme; saved: AppearanceTheme[]; guide?: AppearanceGuide }
export const defaultAppearance = (): AppearanceTheme => ({ name: 'Default', mode: 'dark', interface_font: 'system', code_font: 'system-mono', interface_size: 14, code_size: 12, colors: {}, css: '', scene: 'none', materials: {} })
export const interfaceFontFamily = (id: string) => INTERFACE_FONTS.find(font => font.id === id)?.family ?? INTERFACE_FONTS[0].family
export const codeFontFamily = (id: string) => CODE_FONTS.find(font => font.id === id)?.family ?? APPLE_MONO_FONT

export function validateTheme(value: unknown, guide: AppearanceGuide = {}): AppearanceTheme {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Choose a theme JSON file.')
  const v = value as AppearanceTheme
  const keys = ['name', 'mode', 'interface_font', 'code_font', 'interface_size', 'code_size', 'colors', 'css', 'scene', 'materials']
  if (Object.keys(v).some(key => !keys.includes(key)) || typeof v.name !== 'string' || !v.name.trim() || v.name.length > 60 || !['light', 'dark'].includes(v.mode) || typeof v.interface_font !== 'string' || typeof v.code_font !== 'string' || (guide.fonts ?? INTERFACE_FONTS.map(font => font.id)).indexOf(v.interface_font) < 0 || (guide.code_fonts ?? CODE_FONTS.map(font => font.id)).indexOf(v.code_font) < 0 || !Number.isInteger(v.interface_size) || v.interface_size < 12 || v.interface_size > 20 || !Number.isInteger(v.code_size) || v.code_size < 10 || v.code_size > 20 || typeof v.css !== 'string' || new TextEncoder().encode(v.css).length > 20000 || !v.colors || typeof v.colors !== 'object' || Array.isArray(v.colors)) throw new Error('Use the offered fonts, sizes, colours and at most 20 KB of CSS.')
  const roles: readonly string[] = guide.colour_roles ?? COLOUR_ROLES
  if (v.scene !== undefined && (typeof v.scene !== 'string' || (guide.scenes && !guide.scenes.includes(v.scene)))) throw new Error('Choose one of the offered backgrounds.')
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
