// OSC 6973 is stamped/filtered by the server from managed shell integration.
// This does not infer commands from prompts, output or the last history line.
type Marker = { line: number; isDisposed: boolean; dispose: () => void }
type Position = { x: number; y: number }
type Terminal = {
  parser: { registerOscHandler: (id: number, handler: (data: string) => boolean) => { dispose: () => void } }
  registerMarker: (offset?: number) => Marker | undefined
  buffer: { active: { type: string; cursorX: number } }
  getSelection: () => string
  getSelectionPosition: () => { start: Position; end: Position } | undefined
}
export type TerminalContextSelection = { command_id: string; command: string; text: string; start_line: number; end_line: number }

export function trackTerminalContext(term: Terminal) {
  let current: { id: string; command: string; start: Marker; startColumn: number; end?: Marker; endColumn?: number } | undefined
  const clear = () => { current?.start.dispose(); current?.end?.dispose(); current = undefined }
  const handler = term.parser.registerOscHandler(6973, data => {
    const [kind, id, payload] = data.split(';')
    if (kind === 'C') {
      clear()
      if (!id || !payload || payload.length > 2048 || term.buffer.active.type !== 'normal') return true
      try {
        const command = new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(atob(payload), c => c.charCodeAt(0)))
        const start = term.registerMarker(0)
        if (start && command) current = { id, command, start, startColumn: term.buffer.active.cursorX }
      } catch { /* Unusable metadata never falls back to a guessed command. */ }
    } else if (kind === 'D' && current?.id === id) {
      current.end?.dispose()
      current.end = term.registerMarker(0)
      current.endColumn = term.buffer.active.cursorX
    } else clear()
    return true
  })
  const take = (): TerminalContextSelection | undefined => {
    const selection = term.getSelectionPosition(), text = term.getSelection(), group = current
    if (!group?.end || group.start.isDisposed || group.end.isDisposed || term.buffer.active.type !== 'normal' || !selection || !text.trim() || new TextEncoder().encode(text).length > 16384) return
    const { start, end } = selection
    if (start.y < group.start.line || (start.y === group.start.line && start.x < group.startColumn) || end.y > group.end.line || (end.y === group.end.line && end.x > (group.endColumn ?? 0))) return
    return { command_id: group.id, command: group.command, text, start_line: start.y, end_line: end.y }
  }
  // Repainted tmux history, resize/reflow, reconnect and new terminal input
  // invalidate provenance. Only the current connected, completed run is usable.
  return { clear, take, dispose: () => { clear(); handler.dispose() } }
}
