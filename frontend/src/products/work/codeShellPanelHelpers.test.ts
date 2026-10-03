import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import {
  SHELL_FONT_SIZE_KEY,
  clampShellFontSize,
  isOpenableTerminalLink,
  readShellFontSize,
  shellReconnectDelayMs,
  shellStreamUrl,
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
    expect(source).toContain('aria-label={`Colour scheme: ${SHELL_THEME_LABELS[colourScheme]}`}')
  })

  it('loads the xterm add-ons and opens links only through the safe check', () => {
    for (const addon of ['FitAddon', 'SearchAddon', 'Unicode11Addon', 'WebLinksAddon', 'WebglAddon']) {
      expect(source).toContain(`new ${addon}(`)
    }
    expect(source).toContain('isOpenableTerminalLink(uri)')
    expect(source).toContain("'noopener,noreferrer'")
  })

  it('has the toolbar the design promised', () => {
    for (const label of ['Search the terminal', 'Copy the selection', 'Paste', 'Clear the screen', 'Smaller text', 'Larger text']) {
      expect(source).toContain(`aria-label="${label}"`)
    }
    expect(source).toContain("expanded ? 'Exit full screen' : 'Full screen'")
    expect(source).toContain('SHELL_RECONNECT_ATTEMPTS')
  })
})
