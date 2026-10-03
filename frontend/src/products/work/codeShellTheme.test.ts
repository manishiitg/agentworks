import { describe, expect, it } from 'vitest'
import { HOMEBREW_THEME, SHELL_THEME_KEY, nextShellTheme, readShellTheme, shellTheme } from './codeShellTheme'

describe('Code terminal colour schemes', () => {
  it('Homebrew is black with bright green text and a green cursor, with a full 16-colour palette', () => {
    expect(HOMEBREW_THEME.background).toBe('#000000')
    expect(HOMEBREW_THEME.foreground).toBe('#28fe14')
    expect(HOMEBREW_THEME.cursor).toBe('#23ff18')
    for (const colour of ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white', 'brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightMagenta', 'brightCyan', 'brightWhite']) {
      expect(HOMEBREW_THEME[colour as keyof typeof HOMEBREW_THEME], colour).toMatch(/^#[0-9a-f]{6}$/)
    }
  })

  it('keeps blue readable on black: directories in `ls` are blue, and the original Homebrew blue (#0000b2) disappears', () => {
    const luminance = (hex: string) => {
      const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255).map(c => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4))
      return 0.2126 * r + 0.7152 * g + 0.0722 * b
    }
    expect(luminance(HOMEBREW_THEME.blue as string)).toBeGreaterThan(luminance('#0000b2'))
    expect(luminance(HOMEBREW_THEME.brightBlue as string)).toBeGreaterThan(luminance(HOMEBREW_THEME.blue as string))
  })

  it('Classic follows the app\'s terminal look', () => {
    const appLook = { background: '#0b0e14' }
    expect(shellTheme('classic', appLook)).toBe(appLook)
    expect(shellTheme('homebrew', appLook)).toBe(HOMEBREW_THEME)
  })

  it('defaults to Homebrew and ignores a broken or blocked store', () => {
    expect(readShellTheme(undefined)).toBe('homebrew')
    expect(readShellTheme({ getItem: () => null })).toBe('homebrew')
    expect(readShellTheme({ getItem: () => 'neon' })).toBe('homebrew')
    expect(readShellTheme({ getItem: () => 'classic' })).toBe('classic')
    expect(readShellTheme({ getItem: () => { throw new Error('blocked') } })).toBe('homebrew')
    expect(SHELL_THEME_KEY).toBe('code_terminal_theme')
  })

  it('switches between the two', () => {
    expect(nextShellTheme('homebrew')).toBe('classic')
    expect(nextShellTheme('classic')).toBe('homebrew')
  })
})
