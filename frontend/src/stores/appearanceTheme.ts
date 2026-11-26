const BUNDLED_MONO_FONTS = [
  { id: 'jetbrains-mono', label: 'JetBrains Mono', family: '"JetBrains Mono", monospace' },
  { id: 'fira-code', label: 'Fira Code', family: '"Fira Code", monospace' },
  { id: 'ibm-plex-mono', label: 'IBM Plex Mono', family: '"IBM Plex Mono", monospace' },
  { id: 'source-code-pro', label: 'Source Code Pro', family: '"Source Code Pro", monospace' },
] as const
export const INTERFACE_FONTS = [
  { id: 'system', label: 'System (SF Pro)', family: '-apple-system, BlinkMacSystemFont, system-ui, "Segoe UI", sans-serif' },
  { id: 'new-york', label: 'New York', family: '"New York", ui-serif, Georgia, serif' },
  { id: 'helvetica-neue', label: 'Helvetica Neue', family: '"Helvetica Neue", Helvetica, Arial, sans-serif' },
  { id: 'avenir-next', label: 'Avenir Next', family: '"Avenir Next", Avenir, system-ui, sans-serif' },
  { id: 'georgia', label: 'Georgia', family: 'Georgia, "Times New Roman", serif' },
  { id: 'menlo', label: 'Menlo', family: 'Menlo, Monaco, monospace' },
  ...BUNDLED_MONO_FONTS,
] as const
export const APPLE_MONO_FONT = '"AgentWorks SF Mono Terminal", "SF Mono Terminal", "SFMonoTerminal-Regular", "SFMono-Regular", "SF Mono", Menlo, Monaco, Consolas, monospace'
export const CODE_FONTS = [{ id: 'system-mono', label: 'System monospace', family: APPLE_MONO_FONT }, ...BUNDLED_MONO_FONTS]
export const COLOUR_ROLES = ['background', 'foreground', 'card', 'muted', 'muted_foreground', 'border', 'accent', 'sidebar', 'working', 'waiting', 'done', 'error', 'loader', 'icon', 'panel_border', 'card_border', 'composer_border', 'input_border', 'selected_agent_border', 'running_agent_border', 'user_background', 'user_foreground', 'assistant_background', 'assistant_foreground'] as const
export type ColourRole = typeof COLOUR_ROLES[number]
export type AppearanceProfile = 'light' | 'dark'
export type AppearanceBorderSurface = 'panel' | 'card' | 'composer' | 'input' | 'selected_agent' | 'running_agent'
export interface AppearanceBorder { width_px?: number; radius_px?: number; style?: 'solid' | 'dashed' | 'dotted' }
export interface AppearanceChat { user_style?: 'bubble' | 'plain' | 'card'; assistant_style?: 'bubble' | 'plain' | 'card'; spacing_px?: number; reading_width_px?: number }
export interface AppearanceLayout { density?: 'compact' | 'normal' | 'roomy'; sidebar_width_px?: number; right_panel_open?: boolean }
export type AppearanceBackgroundMode = 'dark' | 'light' | 'both'
export interface AppearanceDecoration { id: string; children?: AppearanceDecoration[] }
export interface AppearanceScript { code: string; fps: number; mode?: AppearanceBackgroundMode }
export interface AppearanceShader { code: string; speed: number; intensity: number; mode?: AppearanceBackgroundMode }
export const backgroundActiveInMode = (mode: AppearanceBackgroundMode | undefined, profile: AppearanceProfile) => !mode || mode === 'both' || mode === profile
export interface AppearanceImage {asset_id:string;fit:'cover'|'contain'|'tile';position_x:number;position_y:number;brightness:number}
export interface AppearanceTheme {
  name: string
  mode: AppearanceProfile
  interface_font: string
  code_font: string
  interface_size: number
  code_size: number
  colors: Partial<Record<AppearanceProfile, Partial<Record<ColourRole, string>>>>
  chat?: AppearanceChat | null
  layout?: AppearanceLayout | null
  background_script?: AppearanceScript | null
  background_shader?: AppearanceShader | null
  decorations?: AppearanceDecoration[] | null
  background_image?: AppearanceImage | null
  css: string
  background_video?: { url?: string; asset_id?: string; dim: number; playback_rate: number } | null
  /** How see-through each surface is; decoration lives only in personal CSS. */
  materials?: Record<string, { opacity?: number; blur?: number }>
  loader?: { style?: 'ring' | 'dots' | 'pulse' | 'orbit' | 'bar'; duration_ms?: number } | null
  icons?: { style?: 'round' | 'square'; stroke_width?: number; scale?: number } | null
  borders?: Partial<Record<AppearanceBorderSurface, AppearanceBorder>>
}
/** What the server offers (appearance.go's guide). `hooks` are the theme selectors the diagnostic counts. */
export interface AppearanceGuide { fonts?: string[]; code_fonts?: string[]; colour_roles?: string[]; material_surfaces?: Record<string, string>; css_max_bytes?: number; hooks?: string[] }
export interface AppearanceDocument { current: AppearanceTheme; saved: AppearanceTheme[]; guide?: AppearanceGuide }
/** The HTTP read; `preview` is an agent's temporary look, shown only on the theme preview page. */
export type AppearanceRead = AppearanceDocument & { preview?: AppearanceTheme | null }
export const defaultAppearance = (): AppearanceTheme => ({ name: 'Default', mode: 'dark', interface_font: 'system', code_font: 'system-mono', interface_size: 14, code_size: 12, colors: {}, css: '', materials: {} })
export const interfaceFontFamily = (id: string) => INTERFACE_FONTS.find(font => font.id === id)?.family ?? INTERFACE_FONTS[0].family
export const codeFontFamily = (id: string) => CODE_FONTS.find(font => font.id === id)?.family ?? APPLE_MONO_FONT

/** Only what rendering needs to not crash. The server owns every rule and limit
 * (#3085); unknown fields are kept and ignored, never a reason to fall back. */
export function readTheme(value: unknown): AppearanceTheme | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const theme = { ...defaultAppearance(), ...(value as Partial<AppearanceTheme>) }
  if (typeof theme.name !== 'string') return null
  if (theme.mode !== 'light' && theme.mode !== 'dark') theme.mode = 'dark'
  if (!theme.colors || typeof theme.colors !== 'object') theme.colors = {}
  if (typeof theme.css !== 'string') theme.css = ''
  return theme
}

// Hex inputs become semantic HSL tokens, never arbitrary CSS declarations.
export function hexToHsl(hex: string): string {
  const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
  const max = Math.max(r, g, b), min = Math.min(r, g, b), delta = max - min, l = (max + min) / 2
  let h = 0, s = 0
  if (delta) { s = delta / (1 - Math.abs(2 * l - 1)); h = max === r ? ((g - b) / delta) % 6 : max === g ? (b - r) / delta + 2 : (r - g) / delta + 4 }
  return `${(h * 60 + 360) % 360} ${s * 100}% ${l * 100}%`
}
