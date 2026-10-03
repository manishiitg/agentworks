import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import {
  SHELL_FONT_SIZE_KEY,
  clampShellFontSize,
  isOpenableTerminalLink,
  readShellFontSize,
  shellReconnectDelayMs,
  shellStreamUrl,
  SHELL_MAX_TABS,
  closeShellTab,
  nextShellTab,
  readShellTabs,
  writeShellTabs,
  shellShortcut,
  shellShortcutLabel,
} from './codeShellPanelHelpers'

describe('Code terminal helpers', () => {
  it('keeps the text size within a readable range and survives junk', () => {
    expect(clampShellFontSize(13, 13)).toBe(13)
    expect(clampShellFontSize(3, 13)).toBe(10)
    expect(clampShellFontSize(99, 13)).toBe(22)
    expect(clampShellFontSize(Number.NaN, 13)).toBe(13)
    expect(clampShellFontSize(14.6, 13)).toBe(15)
  })

  it('reads the saved text size, ignoring a missing, broken or blocked store', () => {
    expect(readShellFontSize(13, { getItem: () => '16' })).toBe(16)
    expect(readShellFontSize(13, { getItem: () => null })).toBe(13)
    expect(readShellFontSize(13, { getItem: () => 'abc' })).toBe(13)
    expect(readShellFontSize(13, { getItem: () => { throw new Error('blocked') } })).toBe(13)
    expect(readShellFontSize(13, undefined)).toBe(13)
    expect(SHELL_FONT_SIZE_KEY).toBe('code_terminal_font_size')
  })

  it('backs off a dropped connection: 1 s, 2 s, 4 s, then 8 s at most', () => {
    expect([0, 1, 2, 3, 4, 9].map(shellReconnectDelayMs)).toEqual([1000, 2000, 4000, 8000, 8000, 8000])
    expect(shellReconnectDelayMs(-1)).toBe(1000)
  })

  it('builds the stream address on the right scheme, with the size and the sign-in', () => {
    const url = new URL(shellStreamUrl('a b/c', 120, 30, 'https://host.example', 'tok'))
    expect(url.protocol).toBe('wss:')
    expect(url.pathname).toBe('/api/agent-profiles/code/projects/a%20b%2Fc/shell/stream')
    expect(url.searchParams.get('cols')).toBe('120')
    expect(url.searchParams.get('rows')).toBe('30')
    expect(url.searchParams.get('token')).toBe('tok')
    expect(new URL(shellStreamUrl('p', 80, 24, 'http://127.0.0.1:18743', null)).protocol).toBe('ws:')
    expect(new URL(shellStreamUrl('p', 80, 24, 'http://127.0.0.1:18743', null)).searchParams.has('token')).toBe(false)
  })

  it('opens only web links from terminal output', () => {
    expect(isOpenableTerminalLink('https://example.com/a?b=1')).toBe(true)
    expect(isOpenableTerminalLink('http://localhost:3000')).toBe(true)
    expect(isOpenableTerminalLink('javascript:alert(1)')).toBe(false)
    expect(isOpenableTerminalLink('file:///etc/passwd')).toBe(false)
    expect(isOpenableTerminalLink('not a url')).toBe(false)
  })
})

describe('Code terminal panel wiring', () => {
  const source = readFileSync('src/products/work/CodeShellPanel.tsx', 'utf8')

  it('uses the chosen colour scheme (Homebrew by default), recolors in place, and keeps the coding-tool font', () => {
    expect(source).toContain('shellTheme(schemeRef.current, RAW_XTERM_THEMES[themeRef.current])')
    expect(source).toContain('termRef.current.options.theme = shellTheme(colourScheme, RAW_XTERM_THEMES[theme])')
    expect(source).toContain('fontFamily: RAW_XTERM_FONT_FAMILY')
    expect(source).toContain('Colours: {SHELL_THEME_LABELS[colourScheme]}')
  })

  it('loads the xterm add-ons and opens links only through the safe check', () => {
    for (const addon of ['FitAddon', 'SearchAddon', 'Unicode11Addon', 'WebLinksAddon', 'WebglAddon']) {
      expect(source).toContain(`new ${addon}(`)
    }
    expect(source).toContain('isOpenableTerminalLink(uri)')
    expect(source).toContain("'noopener,noreferrer'")
  })

  it('keeps search in the toolbar and the rest in one menu with shortcuts', () => {
    expect(source).toContain('aria-label="Search the terminal"')
    expect(source).toContain('aria-label="Terminal menu"')
    for (const item of ["'Copy selection', 'copy'", "'Paste', 'paste'", "'Clear screen', 'clear'", "'Larger text', 'larger'", "'Smaller text', 'smaller'", "'New terminal', 'newTab'"]) {
      expect(source).toContain(`menuItem(${item}`)
    }
    expect(source).toContain('shellShortcut(event, IS_MAC)')
    expect(source).toContain("expanded ? 'Exit full screen' : 'Full screen'")
    expect(source).toContain('SHELL_RECONNECT_ATTEMPTS')
  })
})

describe('Code terminal tabs', () => {
  const memory = () => {
    const values = new Map<string, string>()
    return { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) } }
  }

  it('asks the server for the tab, keeping tab 1 on the old url', () => {
    expect(shellStreamUrl('p', 80, 24, 'https://x.test', null)).not.toContain('tab=')
    expect(shellStreamUrl('p', 80, 24, 'https://x.test', null, 1)).not.toContain('tab=')
    expect(shellStreamUrl('p', 80, 24, 'https://x.test', null, 3)).toContain('tab=3')
  })

  it('allows at most three tabs and reuses the lowest free number', () => {
    expect(SHELL_MAX_TABS).toBe(3)
    expect(nextShellTab([1])).toBe(2)
    expect(nextShellTab([1, 3])).toBe(2)
    expect(nextShellTab([2, 3])).toBe(1)
    expect(nextShellTab([1, 2, 3])).toBeNull()
  })

  it('closes a tab, activating its left neighbour, and never the last one', () => {
    expect(closeShellTab({ tabs: [1, 2, 3], active: 2 }, 2)).toEqual({ tabs: [1, 3], active: 1 })
    expect(closeShellTab({ tabs: [1, 2, 3], active: 3 }, 1)).toEqual({ tabs: [2, 3], active: 3 })
    expect(closeShellTab({ tabs: [2, 3], active: 2 }, 2)).toEqual({ tabs: [3], active: 3 })
    expect(closeShellTab({ tabs: [1], active: 1 }, 1)).toEqual({ tabs: [1], active: 1 })
  })

  it('remembers the open tabs per Code and ignores anything malformed', () => {
    const store = memory()
    expect(readShellTabs('a', store)).toEqual({ tabs: [1], active: 1 })
    writeShellTabs('a', { tabs: [1, 3], active: 3 }, store)
    expect(readShellTabs('a', store)).toEqual({ tabs: [1, 3], active: 3 })
    expect(readShellTabs('b', store)).toEqual({ tabs: [1], active: 1 })
    store.setItem('code_terminal_tabs:c', JSON.stringify({ tabs: [0, 2, 2, 9, 'x'], active: 9 }))
    expect(readShellTabs('c', store)).toEqual({ tabs: [2], active: 2 })
    store.setItem('code_terminal_tabs:d', '{not json')
    expect(readShellTabs('d', store)).toEqual({ tabs: [1], active: 1 })
  })
})

describe('Code terminal shortcuts', () => {
  const key = (k: string, mods: Partial<{ ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; altKey: boolean; code: string }> = {}) =>
    ({ key: k, ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, ...mods })

  it('never takes the shell\'s own keys', () => {
    for (const isMac of [true, false]) {
      expect(shellShortcut(key('c', { ctrlKey: true }), isMac)).toBeNull() // Ctrl+C interrupts
      expect(shellShortcut(key('d', { ctrlKey: true }), isMac)).toBeNull()
      expect(shellShortcut(key('r', { ctrlKey: true }), isMac)).toBeNull()
      expect(shellShortcut(key('a'), isMac)).toBeNull()
      expect(shellShortcut(key('b', { altKey: true }), isMac)).toBeNull() // readline word jumps
    }
    expect(shellShortcut(key('k', { ctrlKey: true }), false)).toBeNull() // readline kill-line on Linux
  })

  it('maps the Mac shortcuts', () => {
    expect(shellShortcut(key('c', { metaKey: true }), true)).toBe('copy')
    expect(shellShortcut(key('v', { metaKey: true }), true)).toBe('paste')
    expect(shellShortcut(key('f', { metaKey: true }), true)).toBe('search')
    expect(shellShortcut(key('k', { metaKey: true }), true)).toBe('clear')
    expect(shellShortcut(key('=', { metaKey: true }), true)).toBe('larger')
    expect(shellShortcut(key('-', { metaKey: true }), true)).toBe('smaller')
    expect(shellShortcut(key('Enter', { metaKey: true, shiftKey: true }), true)).toBe('fullscreen')
    expect(shellShortcut(key('¡', { altKey: true, code: 'Digit1' }), true)).toBe('tab1')
    expect(shellShortcut(key('ˇ', { altKey: true, shiftKey: true, code: 'KeyT' }), true)).toBe('newTab')
  })

  it('maps the Windows/Linux shortcuts', () => {
    expect(shellShortcut(key('C', { ctrlKey: true, shiftKey: true }), false)).toBe('copy')
    expect(shellShortcut(key('V', { ctrlKey: true, shiftKey: true }), false)).toBe('paste')
    expect(shellShortcut(key('f', { ctrlKey: true }), false)).toBe('search')
    expect(shellShortcut(key('K', { ctrlKey: true, shiftKey: true }), false)).toBe('clear')
    expect(shellShortcut(key('3', { altKey: true, code: 'Digit3' }), false)).toBe('tab3')
    expect(shellShortcutLabel('copy', false)).toBe('Ctrl+Shift+C')
    expect(shellShortcutLabel('copy', true)).toBe('⌘C')
  })
})
