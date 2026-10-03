import type { ITheme } from '@xterm/xterm'

// Colour schemes of Code's terminal. "homebrew" is the classic macOS Terminal profile of that name: black, bright green text, a green
// block cursor. Its blues are lightened a little: the original dark blue (#0000b2) is unreadable on black, and `ls` prints directories in it.
export type ShellThemeName = 'homebrew' | 'classic'

export const SHELL_THEME_KEY = 'code_terminal_theme'
export const SHELL_THEME_NAMES: ShellThemeName[] = ['homebrew', 'classic']
export const SHELL_THEME_LABELS: Record<ShellThemeName, string> = { homebrew: 'Homebrew', classic: 'Classic' }

export const HOMEBREW_THEME: ITheme = {
  background: '#000000',
  foreground: '#28fe14',
  cursor: '#23ff18',
  cursorAccent: '#000000',
  selectionBackground: '#0a5c05',
  selectionInactiveBackground: '#083905',
  black: '#000000',
  red: '#cc2222',
  green: '#00a600',
  yellow: '#999900',
  blue: '#4a4aff',
  magenta: '#b200b2',
  cyan: '#00a6b2',
  white: '#bfbfbf',
  brightBlack: '#666666',
  brightRed: '#ff3b3b',
  brightGreen: '#28fe14',
  brightYellow: '#e5e500',
  brightBlue: '#7a7aff',
  brightMagenta: '#e500e5',
  brightCyan: '#00e5e5',
  brightWhite: '#ffffff',
  overviewRulerBorder: '#00000000',
  scrollbarSliderBackground: '#28fe1433',
  scrollbarSliderHoverBackground: '#28fe1466',
  scrollbarSliderActiveBackground: '#28fe148c',
}

/** The colours for a scheme. "classic" is the app's own terminal look (the coding-tool terminals), passed in so this file stays light. */
export function shellTheme(name: ShellThemeName, classic: ITheme): ITheme {
  return name === 'homebrew' ? HOMEBREW_THEME : classic
}

export function readShellTheme(storage: Pick<Storage, 'getItem'> | undefined): ShellThemeName {
  try {
    const raw = storage?.getItem(SHELL_THEME_KEY)
    return SHELL_THEME_NAMES.includes(raw as ShellThemeName) ? (raw as ShellThemeName) : 'homebrew'
  } catch {
    return 'homebrew'
  }
}

export function nextShellTheme(current: ShellThemeName): ShellThemeName {
  return SHELL_THEME_NAMES[(SHELL_THEME_NAMES.indexOf(current) + 1) % SHELL_THEME_NAMES.length]
}
