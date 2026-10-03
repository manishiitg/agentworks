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

export function shellStreamUrl(projectId: string, cols: number, rows: number, apiBase: string, token: string | null, tab = 1): string {
  const url = new URL(`/api/agent-profiles/code/projects/${encodeURIComponent(projectId)}/shell/stream`, apiBase.replace(/^http/i, 'ws'))
  url.searchParams.set('cols', String(cols))
  url.searchParams.set('rows', String(rows))
  if (tab > 1) url.searchParams.set('tab', String(tab))
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

// Terminal tabs: a person has at most SHELL_MAX_TABS terminals per Code (the server refuses any other tab number).
// Tab numbers are 1..SHELL_MAX_TABS; tab 1 is the shell a single terminal always had.
export const SHELL_MAX_TABS = 3
export const SHELL_TABS_KEY_PREFIX = 'code_terminal_tabs:'

export type ShellTabs = { tabs: number[]; active: number }

const DEFAULT_TABS: ShellTabs = { tabs: [1], active: 1 }

/** The open tabs of one Code, as remembered in this browser; anything malformed falls back to one tab. */
export function readShellTabs(projectId: string, storage: Pick<Storage, 'getItem'> | undefined): ShellTabs {
  try {
    const raw = storage?.getItem(SHELL_TABS_KEY_PREFIX + projectId)
    if (!raw) return DEFAULT_TABS
    const parsed = JSON.parse(raw) as Partial<ShellTabs>
    const tabs = Array.from(new Set((Array.isArray(parsed.tabs) ? parsed.tabs : [])
      .filter((tab): tab is number => Number.isInteger(tab) && tab >= 1 && tab <= SHELL_MAX_TABS))).sort((a, b) => a - b)
    if (!tabs.length) return DEFAULT_TABS
    const active = typeof parsed.active === 'number' && tabs.includes(parsed.active) ? parsed.active : tabs[0]
    return { tabs, active }
  } catch {
    return DEFAULT_TABS
  }
}

export function writeShellTabs(projectId: string, value: ShellTabs, storage: Pick<Storage, 'setItem'> | undefined): void {
  try { storage?.setItem(SHELL_TABS_KEY_PREFIX + projectId, JSON.stringify(value)) } catch { /* private window */ }
}

/** The lowest free tab number, or null when all SHELL_MAX_TABS are open. */
export function nextShellTab(tabs: number[]): number | null {
  for (let tab = 1; tab <= SHELL_MAX_TABS; tab += 1) if (!tabs.includes(tab)) return tab
  return null
}

/** The tabs after closing `tab`, and which one becomes active (its left neighbour, else the first). The last tab stays. */
export function closeShellTab(state: ShellTabs, tab: number): ShellTabs {
  if (state.tabs.length <= 1 || !state.tabs.includes(tab)) return state
  const index = state.tabs.indexOf(tab)
  const tabs = state.tabs.filter(value => value !== tab)
  const active = state.active === tab ? tabs[Math.max(0, index - 1)] : state.active
  return { tabs, active }
}

// Keyboard shortcuts of the terminal. They never take a key the shell needs: on a Mac they use ⌘ (shells do not see it); elsewhere
// Ctrl+C stays the shell's interrupt, so copy and paste are Ctrl+Shift+C/V like other terminals.
export type ShellAction =
  | 'search' | 'copy' | 'paste' | 'clear' | 'larger' | 'smaller' | 'fullscreen'
  | 'newTab' | 'tab1' | 'tab2' | 'tab3'

export type ShellKey = { key: string; code?: string; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; altKey: boolean }

export function shellShortcut(event: ShellKey, isMac: boolean): ShellAction | null {
  const key = event.key.toLowerCase()
  const code = event.code || ''
  // Alt shortcuts read the physical key: on a Mac Option changes the character (⌥1 types ¡).
  if (event.altKey && !event.ctrlKey && !event.metaKey) {
    if (event.shiftKey && (code === 'KeyT' || key === 't')) return 'newTab'
    if (!event.shiftKey) {
      if (code === 'Digit1' || key === '1') return 'tab1'
      if (code === 'Digit2' || key === '2') return 'tab2'
      if (code === 'Digit3' || key === '3') return 'tab3'
    }
    return null
  }
  const mod = isMac ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey
  if (!mod || event.altKey) return null
  if (event.shiftKey && key === 'enter') return 'fullscreen'
  if (isMac) {
    if (event.shiftKey) return null
    if (key === 'f') return 'search'
    if (key === 'c') return 'copy'
    if (key === 'v') return 'paste'
    if (key === 'k') return 'clear'
  } else {
    if (!event.shiftKey && key === 'f') return 'search'
    if (event.shiftKey && key === 'c') return 'copy'
    if (event.shiftKey && key === 'v') return 'paste'
    if (event.shiftKey && key === 'k') return 'clear'
  }
  if (!event.shiftKey && (key === '=' || key === '+')) return 'larger'
  if (event.shiftKey && key === '+') return 'larger'
  if (!event.shiftKey && key === '-') return 'smaller'
  return null
}

/** How a shortcut is written in the menu on this platform. */
export function shellShortcutLabel(action: ShellAction, isMac: boolean): string {
  const mod = isMac ? '⌘' : 'Ctrl+'
  const shiftMod = isMac ? '⌘' : 'Ctrl+Shift+'
  const alt = isMac ? '⌥' : 'Alt+'
  switch (action) {
    case 'search': return `${mod}F`
    case 'copy': return `${shiftMod}C`
    case 'paste': return `${shiftMod}V`
    case 'clear': return `${shiftMod}K`
    case 'larger': return `${mod}=`
    case 'smaller': return `${mod}-`
    case 'fullscreen': return isMac ? '⌘⇧Enter' : 'Ctrl+Shift+Enter'
    case 'newTab': return isMac ? '⌥⇧T' : 'Alt+Shift+T'
    case 'tab1': return `${alt}1`
    case 'tab2': return `${alt}2`
    case 'tab3': return `${alt}3`
  }
}
