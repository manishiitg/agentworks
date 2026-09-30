import type { IDisposable, Terminal } from '@xterm/xterm'
import { copyToClipboard } from './textUtils'

// Preserve native terminal modes, including the alternate screen, application
// cursor keys and bracketed paste. Only copying a selection is browser-owned.
export function installInteractiveXtermKeys(term: Terminal): IDisposable {
  term.attachCustomKeyEventHandler(event => {
    if (event.type !== 'keydown') return true
    const copy = (event.metaKey || event.ctrlKey) && !event.altKey
      && event.key.toLowerCase() === 'c' && term.hasSelection()
    if (!copy) return true // Ctrl+C without a selection interrupts the CLI.
    void copyToClipboard(term.getSelection())
    event.preventDefault()
    return false
  })
  return { dispose: () => term.attachCustomKeyEventHandler(() => true) }
}

export function terminalInputBytes(data: string, binary = false): Uint8Array {
  return binary
    ? Uint8Array.from(data, char => char.charCodeAt(0) & 0xff)
    : new TextEncoder().encode(data)
}

// Do not queue or replay keyboard bytes across a reconnect: an uncertain Enter
// could submit a prompt twice. Input is enabled only after the server seed.
export function sendTerminalInput(socket: WebSocket | null, ready: boolean, data: string, binary = false): boolean {
  if (!ready || !socket || socket.readyState !== WebSocket.OPEN) return false
  try {
    socket.send(terminalInputBytes(data, binary))
    return true
  } catch {
    return false
  }
}

// tmux knows whether the native application requested bracketed paste, even
// when its initial mode sequence predates the browser attach.
export function sendTerminalPaste(socket: WebSocket | null, ready: boolean, text: string): boolean {
  if (!ready || !socket || socket.readyState !== WebSocket.OPEN) return false
  try { socket.send(JSON.stringify({ type: 'paste', text })); return true } catch { return false }
}
