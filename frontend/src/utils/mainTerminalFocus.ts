export const MAIN_TERMINAL_FOCUS_EVENT = 'agentworks:main-terminal-focus'

export function requestMainTerminalFocus(sessionId: string | null | undefined): void {
  if (sessionId) window.dispatchEvent(new CustomEvent(MAIN_TERMINAL_FOCUS_EVENT, { detail: { sessionId } }))
}
