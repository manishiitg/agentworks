/**
 * A deployment's branding, from window.__APP_RUNTIME_CONFIG__ (each
 * deployment's runtime-config.js; assets in its brand/ folder). Every key is
 * optional: without them the app is AgentWorks.
 */
export type RuntimeBrandingConfig = {
  appName?: unknown
  faviconUrl?: unknown
  /** Square mark for the sign-in card. */
  markUrl?: unknown
  /** Wide logo for the top bar (light theme), and its dark-theme version. */
  logoUrl?: unknown
  logoDarkUrl?: unknown
  /** Brand colour (#rrggbb): the primary colour in every theme. */
  brandColor?: unknown
}

function hasControlCharacters(value: string): boolean {
  return Array.from(value).some((character) => {
    const codePoint = character.codePointAt(0)
    return codePoint !== undefined && (codePoint <= 0x1f || codePoint === 0x7f)
  })
}

function safeRuntimeAssetPath(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const path = value.trim()
  if (!path.startsWith('/') || path.startsWith('//') || hasControlCharacters(path)) {
    return null
  }
  return path
}

export function runtimeBrandingConfig(): RuntimeBrandingConfig | null {
  if (typeof window === 'undefined') return null
  const config = (window as Window & { __APP_RUNTIME_CONFIG__?: RuntimeBrandingConfig }).__APP_RUNTIME_CONFIG__
  return config && typeof config === 'object' ? config : null
}

/** A same-origin asset path from the branding config, or null. */
export function runtimeBrandAsset(key: 'markUrl' | 'logoUrl' | 'logoDarkUrl' | 'faviconUrl', config: RuntimeBrandingConfig | null = runtimeBrandingConfig()): string | null {
  return config ? safeRuntimeAssetPath(config[key]) : null
}

/** "#109aaa" as the "H S% L%" triplet the theme variables use, or null. */
export function hexToHslTriplet(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const match = /^#([0-9a-f]{6})$/i.exec(value.trim())
  if (!match) return null
  const int = parseInt(match[1], 16)
  const r = ((int >> 16) & 255) / 255, g = ((int >> 8) & 255) / 255, b = (int & 255) / 255
  const max = Math.max(r, g, b), min = Math.min(r, g, b)
  const l = (max + min) / 2
  let h = 0, s = 0
  if (max !== min) {
    const d = max - min
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
    h = max === r ? (g - b) / d + (g < b ? 6 : 0) : max === g ? (b - r) / d + 2 : (r - g) / d + 4
    h *= 60
  }
  return `${Math.round(h)} ${Math.round(s * 100)}% ${Math.round(l * 100)}%`
}

export function getRuntimeAppName(config: RuntimeBrandingConfig | null | undefined): string | null {
  if (!config || typeof config !== 'object') return null
  if (typeof config.appName !== 'string') return null
  const appName = config.appName.trim()
  if (!appName || appName.length > 120 || hasControlCharacters(appName)) return null
  return appName
}

export function applyRuntimeBranding(config: RuntimeBrandingConfig | null | undefined, doc: Document = document) {
  if (!config || typeof config !== 'object') return

  const appName = getRuntimeAppName(config)
  if (appName) doc.title = appName

  // Inline on the root, so it wins over every theme's --primary.
  const brand = hexToHslTriplet(config.brandColor)
  if (brand) {
    doc.documentElement.style.setProperty('--primary', brand)
    doc.documentElement.style.setProperty('--ring', brand)
  }

  const faviconUrl = safeRuntimeAssetPath(config.faviconUrl)
  if (!faviconUrl) return

  let favicon = doc.querySelector<HTMLLinkElement>('link[rel~="icon"]')
  if (!favicon) {
    favicon = doc.createElement('link')
    favicon.rel = 'icon'
    doc.head.appendChild(favicon)
  }
  favicon.type = faviconUrl.toLowerCase().endsWith('.svg') ? 'image/svg+xml' : 'image/png'
  favicon.href = faviconUrl
}
