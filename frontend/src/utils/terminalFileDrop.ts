/** Files dropped on a chat's live terminal go to that chat's upload, which pastes their paths into the terminal. */
export const TERMINAL_FILE_DROP_EVENT = 'agentworks:terminal-file-drop'
export type TerminalFileDrop = { sessionId: string; files: File[] }

export function dropFilesOnTerminal(sessionId: string | undefined, files: File[]) {
  if (sessionId && files.length) window.dispatchEvent(new CustomEvent<TerminalFileDrop>(TERMINAL_FILE_DROP_EVENT, { detail: { sessionId, files } }))
}
