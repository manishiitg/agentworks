// Shared links identify assets, never credentials or access grants.
export function sharedLink(base: string, kind: 'file' | 'folder' | 'report', path: string, uid?: string): string {
  const bytes = new TextEncoder().encode(path)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  const params = new URLSearchParams({ path: btoa(binary) })
  if (uid) params.set('uid', uid)
  return `${base.replace(/\/$/, '')}/${kind}?${params.toString()}`
}

export function sharedReportLink(base: string, workspacePath: string, documentPath: string, uid?: string): string {
  const url = new URL(sharedLink(base, 'report', workspacePath, uid))
  url.searchParams.set('document', documentPath)
  return url.toString()
}

export function isShareableAppOrigin(base: string): boolean {
  try {
    const url = new URL(base)
    const hostname = url.hostname.toLowerCase()
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return false
    if (hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '::1') return false
    if (/^127(?:\.\d{1,3}){3}$/.test(hostname)) return false
    return hostname.length > 0
  } catch {
    return false
  }
}
export const SHARE_RETURN_KEY = 'agentworks:share-return'
const SHARE_RETURN_OAUTH_PREFIX = `${SHARE_RETURN_KEY}:oauth:`

export function sharedReturnPath(value: string | null): string | null {
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return null
  const url = new URL(value, 'https://agentworks.invalid')
  if (url.origin !== 'https://agentworks.invalid') return null
  const kinds = ['file', 'folder', 'report']
  const queryRoute = kinds.some(kind => url.pathname === `/${kind}`) && url.searchParams.has('path')
  const legacyRoute = kinds.some(kind => url.pathname.startsWith(`/${kind}/`) && url.pathname.length > kind.length + 2)
  if (!queryRoute && !legacyRoute) return null
  return url.pathname + url.search
}

/**
 * Preserve a share destination across OAuth. sessionStorage is the normal
 * same-tab path; the state-bound localStorage copy covers providers and mail
 * clients that complete sign-in in a replacement browsing context.
 */
export function rememberSharedReturnPath(value: string | null, oauthState: string): void {
  const target = sharedReturnPath(value)
  try {
    if (target) sessionStorage.setItem(SHARE_RETURN_KEY, target)
    else sessionStorage.removeItem(SHARE_RETURN_KEY)
  } catch {
    // Storage can be unavailable in hardened/private browser contexts.
  }
  try {
    const key = SHARE_RETURN_OAUTH_PREFIX + oauthState
    if (target) localStorage.setItem(key, target)
    else localStorage.removeItem(key)
  } catch {
    // The same-tab session copy remains the primary path.
  }
}

/** Read and clear the validated OAuth return destination. */
export function consumeSharedReturnPath(oauthState: string): string | null {
  let sessionValue: string | null = null
  let fallbackValue: string | null = null
  try {
    sessionValue = sessionStorage.getItem(SHARE_RETURN_KEY)
    sessionStorage.removeItem(SHARE_RETURN_KEY)
  } catch {
    // Fall through to the state-bound durable copy.
  }
  try {
    const key = SHARE_RETURN_OAUTH_PREFIX + oauthState
    fallbackValue = localStorage.getItem(key)
    localStorage.removeItem(key)
  } catch {
    // Returning to the app root remains the safe final fallback.
  }
  return sharedReturnPath(sessionValue) || sharedReturnPath(fallbackValue)
}

// The app's own share pages (sharedLink). A link an agent wrote for one of them
// on another local port (PUBLIC_URL=http://localhost:5173 left in a local .env
// produced http://localhost:5173/report?path=..., 2026-10-07) opens on the app
// actually in use instead of a dead port.
const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '[::1]'])
const SHARED_PAGES = new Set(['/report', '/file', '/folder'])

export function repairLocalAppLink(href: string, currentOrigin: string): string {
  try {
    const url = new URL(href)
    const current = new URL(currentOrigin)
    if (!LOCAL_HOSTS.has(url.hostname) || !LOCAL_HOSTS.has(current.hostname)) return href
    if (url.origin === current.origin || !SHARED_PAGES.has(url.pathname) || !url.searchParams.has('path')) return href
    return `${current.origin}${url.pathname}${url.search}${url.hash}`
  } catch {
    return href
  }
}
