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
export const APPEARANCE_CSS_MAX_BYTES = 128 * 1024
export const COLOUR_ROLES = ['background', 'foreground', 'card', 'muted', 'muted_foreground', 'border', 'accent', 'sidebar', 'working', 'waiting', 'done', 'error', 'loader', 'icon', 'panel_border', 'card_border', 'composer_border', 'input_border', 'selected_agent_border', 'running_agent_border', 'user_background', 'user_foreground', 'assistant_background', 'assistant_foreground'] as const
export type ColourRole = typeof COLOUR_ROLES[number]
export type AppearanceProfile = 'light' | 'dark'
export type AppearanceBorderSurface = 'panel' | 'card' | 'composer' | 'input' | 'selected_agent' | 'running_agent'
export interface AppearanceBorder { width_px?: number; radius_px?: number; style?: 'solid' | 'dashed' | 'dotted' }
export interface AppearanceChat { user_style?: 'bubble' | 'plain' | 'card'; assistant_style?: 'bubble' | 'plain' | 'card'; spacing_px?: number; reading_width_px?: number }
export interface AppearanceLayout { density?: 'compact' | 'normal' | 'roomy'; sidebar_width_px?: number; right_panel_open?: boolean }
export interface AppearanceDecoration { id: string; children?: AppearanceDecoration[] }
export interface AppearanceScript { code: string; fps: number }
export interface AppearanceShader { code: string; speed: number; intensity: number }
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
/** What the server offers (appearance.go's guide): fonts, code fonts and colour roles are checked against this list, not a copy. */
export interface AppearanceGuide { fonts?: string[]; code_fonts?: string[]; colour_roles?: string[]; material_surfaces?: Record<string, string>; css_max_bytes?: number }
export interface AppearanceDocument { current: AppearanceTheme; saved: AppearanceTheme[]; guide?: AppearanceGuide }
export const defaultAppearance = (): AppearanceTheme => ({ name: 'Default', mode: 'dark', interface_font: 'system', code_font: 'system-mono', interface_size: 14, code_size: 12, colors: {}, css: '', materials: {} })
export const interfaceFontFamily = (id: string) => INTERFACE_FONTS.find(font => font.id === id)?.family ?? INTERFACE_FONTS[0].family
export const codeFontFamily = (id: string) => CODE_FONTS.find(font => font.id === id)?.family ?? APPLE_MONO_FONT

export function validateTheme(value: unknown, guide: AppearanceGuide = {}): AppearanceTheme {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Choose a theme JSON file.')
  const v = value as AppearanceTheme
  const keys = ['name', 'mode', 'interface_font', 'code_font', 'interface_size', 'code_size', 'colors', 'css', 'materials', 'loader', 'icons', 'borders', 'background_video', 'chat', 'layout', 'background_shader', 'decorations', 'background_image', 'background_script']
  if (Object.keys(v).some(key => !keys.includes(key)) || typeof v.name !== 'string' || !v.name.trim() || v.name.length > 60 || !['light', 'dark'].includes(v.mode) || typeof v.interface_font !== 'string' || typeof v.code_font !== 'string' || (guide.fonts ?? INTERFACE_FONTS.map(font => font.id)).indexOf(v.interface_font) < 0 || (guide.code_fonts ?? CODE_FONTS.map(font => font.id)).indexOf(v.code_font) < 0 || !Number.isInteger(v.interface_size) || v.interface_size < 12 || v.interface_size > 20 || !Number.isInteger(v.code_size) || v.code_size < 10 || v.code_size > 20 || typeof v.css !== 'string' || new TextEncoder().encode(v.css).length > (guide.css_max_bytes ?? APPEARANCE_CSS_MAX_BYTES) || !v.colors || typeof v.colors !== 'object' || Array.isArray(v.colors)) throw new Error('Use the offered fonts, sizes, colours and CSS within the limit returned by the server (normally 128 KiB).')
  const bounded = (n: unknown, min: number, max: number) => n === undefined || (Number.isInteger(n) && Number(n) >= min && Number(n) <= max)
  if (v.chat != null) {
    const c = v.chat
    if (typeof c !== 'object' || Array.isArray(c) || Object.keys(c).some(k => !['user_style','assistant_style','spacing_px','reading_width_px'].includes(k)) ||
      [c.user_style,c.assistant_style].some(style => style !== undefined && !['bubble','plain','card'].includes(style)) || !bounded(c.spacing_px,0,32) || !bounded(c.reading_width_px,480,1600)) throw new Error('Choose bubble/plain/card messages, spacing 0–32 px and reading width 480–1600 px.')
  }
  if (v.layout != null) {
    const l = v.layout
    if (typeof l !== 'object' || Array.isArray(l) || Object.keys(l).some(k => !['density','sidebar_width_px','right_panel_open'].includes(k)) ||
      (l.density !== undefined && !['compact','normal','roomy'].includes(l.density)) || !bounded(l.sidebar_width_px,220,360) || (l.right_panel_open !== undefined && typeof l.right_panel_open !== 'boolean')) throw new Error('Choose compact/normal/roomy density, sidebar 220–360 px and a true/false right-panel default.')
  }
  if ([v.background_script,v.background_shader,v.background_image,v.background_video].filter(Boolean).length>1) throw new Error('Choose one background; clear the others with null.')
  if (v.background_script != null) {
    const s=v.background_script
    if (typeof s!=='object' || Array.isArray(s) || Object.keys(s).some(k=>!['code','fps'].includes(k)) || typeof s.code!=='string' || !s.code || new TextEncoder().encode(s.code).length>65536 || !Number.isInteger(s.fps) || s.fps<1 || s.fps>30) throw new Error('Use script code up to 64 KiB and fps 1–30.')
  }
  if (v.background_shader != null) {
    const s = v.background_shader
    if (v.background_video || v.background_image || typeof s !== 'object' || Array.isArray(s) || Object.keys(s).some(k => !['code','speed','intensity'].includes(k)) || typeof s.code !== 'string' || !s.code || new TextEncoder().encode(s.code).length > 16384 || !Number.isFinite(s.speed) || s.speed < 0 || s.speed > 2 || !Number.isFinite(s.intensity) || s.intensity < 0 || s.intensity > 1) throw new Error('Use a GLSL shader up to 16 KiB, speed 0–2 and intensity 0–1; clear the video first.')
  }
  if (v.decorations != null) {
    let count = 0; const ids = new Set<string>()
    const visit = (nodes: unknown, depth: number) => {
      if (!Array.isArray(nodes) || (nodes.length > 0 && depth > 6)) throw new Error('Use a decoration tree up to 6 levels.')
      for (const n of nodes) {
        if (++count > 64 || !n || typeof n !== 'object' || Array.isArray(n) || Object.keys(n).some(k => !['id','children'].includes(k)) || typeof n.id !== 'string' || !/^[a-z][a-z0-9_-]{0,31}$/.test(n.id) || ids.has(n.id)) throw new Error('Use up to 64 decorations with unique lowercase IDs, not HTML or React.')
        ids.add(n.id)
        if (n.children !== undefined) visit(n.children,depth+1)
      }
    }
    visit(v.decorations,1)
  }
  if (v.background_image != null) {
    const b=v.background_image
    if (v.background_video || v.background_shader || typeof b!=='object' || Array.isArray(b) || Object.keys(b).some(k=>!['asset_id','fit','position_x','position_y','brightness'].includes(k)) || !/^[a-f0-9]{64}$/.test(b.asset_id) || !['cover','contain','tile'].includes(b.fit) || !Number.isInteger(b.position_x) || b.position_x<0 || b.position_x>100 || !Number.isInteger(b.position_y) || b.position_y<0 || b.position_y>100 || !Number.isFinite(b.brightness) || b.brightness<.25 || b.brightness>1.5) throw new Error('Use an own image, cover/contain/tile, positions0–100 and brightness0.25–1.5; clear other backgrounds first.')
  }
  if (v.background_video != null) {
    const video = v.background_video
    const asset = typeof video.asset_id === 'string' && /^[a-f0-9]{64}$/.test(video.asset_id)
    let safeURL = false
    try { const url = new URL(video.url ?? ''); safeURL = url.protocol === 'https:' && !!url.hostname && !url.username && !url.password && (video.url?.length ?? 0) <= 2048 && !/[\r\n]/.test(video.url ?? '') } catch { /* An own asset needs no remote URL. */ }
    if (typeof video !== 'object' || Array.isArray(video) || Object.keys(video).some(key => !['url', 'asset_id', 'dim', 'playback_rate'].includes(key)) || (asset ? !!video.url : (!safeURL || video.asset_id !== undefined)) || !Number.isFinite(video.dim) || video.dim < .35 || video.dim > .95 || !Number.isFinite(video.playback_rate) || video.playback_rate < .25 || video.playback_rate > 1) throw new Error('Use an HTTPS video with dim 0.35–0.95 and playback_rate 0.25–1.')
  }
  if (v.loader != null) {
    const loader = v.loader
    if (typeof loader !== 'object' || Array.isArray(loader) || Object.keys(loader).some(key => !['style', 'duration_ms'].includes(key)) ||
      (loader.style !== undefined && !['ring', 'dots', 'pulse', 'orbit', 'bar'].includes(loader.style)) ||
      (loader.duration_ms !== undefined && (!Number.isInteger(loader.duration_ms) || loader.duration_ms < 400 || loader.duration_ms > 4000))) throw new Error('Choose an offered loading indicator and duration from 400 to 4000 ms.')
  }
  if (v.icons != null) {
    const icons = v.icons
    const bounded = (n: unknown, min: number, max: number) => n === undefined || (typeof n === 'number' && Number.isFinite(n) && n >= min && n <= max)
    if (typeof icons !== 'object' || Array.isArray(icons) || Object.keys(icons).some(key => !['style', 'stroke_width', 'scale'].includes(key)) ||
      (icons.style !== undefined && !['round', 'square'].includes(icons.style)) || !bounded(icons.stroke_width, 0.75, 3) || !bounded(icons.scale, 0.75, 1.5)) throw new Error('Use round/square outline icons, stroke width 0.75–3 and scale 0.75–1.5.')
  }
  if (v.borders !== undefined) {
    if (!v.borders || typeof v.borders !== 'object' || Array.isArray(v.borders)) throw new Error('Use a borders object.')
    for (const [surface, border] of Object.entries(v.borders)) {
      const bounded = (n: unknown, max: number) => n === undefined || (Number.isInteger(n) && Number(n) >= 0 && Number(n) <= max)
      if (!['panel', 'card', 'composer', 'input', 'selected_agent', 'running_agent'].includes(surface) || !border || typeof border !== 'object' || Array.isArray(border) || Object.keys(border).some(key => !['width_px', 'radius_px', 'style'].includes(key)) ||
        !bounded(border.width_px, 4) || !bounded(border.radius_px, 9999) || (border.style !== undefined && !['solid', 'dashed', 'dotted'].includes(border.style))) throw new Error('Use an offered border surface, width 0–4 px, radius 0–9999 px and solid/dashed/dotted style.')
    }
  }
  const roles: readonly string[] = guide.colour_roles ?? COLOUR_ROLES
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
