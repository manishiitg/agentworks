// Every build renames the hashed chunks under /assets. A tab opened before a
// deploy still asks for the previous names on its next lazy import, the new
// release no longer serves them, and React surfaces it as "Failed to fetch
// dynamically imported module" -- which looked to users like a random crash
// ("Something went wrong") after each deploy (server A, 2026-09-03). The only
// correct recovery is a reload, so do it once, automatically.

const RELOAD_FLAG = 'agentworks:stale-chunk-reload'

const STALE_CHUNK_PATTERNS = [
  /failed to fetch dynamically imported module/i,
  /importing a module script failed/i,
  /error loading dynamically imported module/i,
  /unable to preload css/i,
]

export function isStaleChunkError(error: unknown): boolean {
  const message =
    typeof error === 'string' ? error : error && typeof error === 'object' ? String((error as { message?: unknown }).message ?? '') : ''
  return STALE_CHUNK_PATTERNS.some(pattern => pattern.test(message))
}

/**
 * Reloads the page for a stale-chunk error, at most once per tab session so a
 * genuinely broken build cannot loop. Returns true when a reload was issued.
 */
export function reloadOnceForStaleChunk(error: unknown): boolean {
  if (!isStaleChunkError(error)) return false
  let alreadyReloaded = false
  try {
    alreadyReloaded = window.sessionStorage.getItem(RELOAD_FLAG) === '1'
    if (!alreadyReloaded) window.sessionStorage.setItem(RELOAD_FLAG, '1')
  } catch {
    // Storage unavailable (private mode, disabled): still reload once per
    // page lifetime -- the flag simply cannot persist across the reload.
  }
  if (alreadyReloaded) return false
  window.location.reload()
  return true
}

/** Clears the once-per-session guard after a page that loaded fine. */
export function clearStaleChunkReloadFlag(): void {
  try {
    window.sessionStorage.removeItem(RELOAD_FLAG)
  } catch {
    // ignore
  }
}
