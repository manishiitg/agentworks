// Pure helpers of Code's terminal panel (CodeShellPanel), kept apart so they can be tested without a terminal.

export const SHELL_FONT_SIZE_MIN = 10
export const SHELL_FONT_SIZE_MAX = 22
export const SHELL_FONT_SIZE_KEY = 'code_terminal_font_size'
// A dropped connection (a deploy, a sleeping laptop) is retried a few times, quietly, before the panel asks the person to.
export const SHELL_RECONNECT_ATTEMPTS = 4

export function clampShellFontSize(value: number, fallback: number): number {
  if (!Number.isFinite(value)) return fallback
  return Math.min(SHELL_FONT_SIZE_MAX, Math.max(SHELL_FONT_SIZE_MIN, Math.round(value)))
}

export function readShellFontSize(fallback: number, storage: Pick<Storage, 'getItem'> | undefined): number {
  try {
    const raw = storage?.getItem(SHELL_FONT_SIZE_KEY)
    return raw == null ? fallback : clampShellFontSize(Number(raw), fallback)
  } catch {
    return fallback
  }
}

/** Delay before reconnect attempt number `attempt` (0-based): 1 s, 2 s, 4 s, 8 s. */
export function shellReconnectDelayMs(attempt: number): number {
  return Math.min(8000, 1000 * 2 ** Math.max(0, attempt))
}

export function shellStreamUrl(projectId: string, cols: number, rows: number, apiBase: string, token: string | null): string {
  const url = new URL(`/api/agent-profiles/code/projects/${encodeURIComponent(projectId)}/shell/stream`, apiBase.replace(/^http/i, 'ws'))
  url.searchParams.set('cols', String(cols))
  url.searchParams.set('rows', String(rows))
  if (token) url.searchParams.set('token', token)
  return url.toString()
}

/** A link in terminal output opens in a new tab, never in the app's own tab, and only http(s). */
export function isOpenableTerminalLink(uri: string): boolean {
  try {
    const protocol = new URL(uri).protocol
    return protocol === 'http:' || protocol === 'https:'
  } catch {
    return false
  }
}
