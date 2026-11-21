import { expect, it } from 'vitest'
import { trackTerminalContext } from './terminalContextSelection'

it('requires actual completed command boundaries and refuses repainted, spanning or disposed selection', () => {
  let osc!: (data: string) => boolean, line = 10
  let selection = { start: { x: 0, y: 10 }, end: { x: 4, y: 11 } }
  const term = { parser: { registerOscHandler: (_: number, callback: typeof osc) => { osc = callback; return { dispose() {} } } }, buffer: { active: { type: 'normal', cursorX: 0 } }, getSelection: () => 'actual\ntext', getSelectionPosition: () => selection, registerMarker: () => ({ line, isDisposed: false, dispose() { this.isDisposed = true } }) }
  const context = trackTerminalContext(term)
  expect(context.take()).toBeUndefined() // Output text alone has no authority.
  osc('C;command-1;'+btoa('printf test'))
  expect(context.take()).toBeUndefined() // Still running.
  line = 11; term.buffer.active.cursorX = 4; osc('D;command-1;0')
  expect(context.take()?.command).toBe('printf test')
  selection = { start: { x: 0, y: 9 }, end: { x: 4, y: 11 } }
  expect(context.take()).toBeUndefined() // Spans another command/prompt.
  selection = { start: { x: 0, y: 10 }, end: { x: 4, y: 11 } }
  term.buffer.active.type = 'alternate'; expect(context.take()).toBeUndefined()
  term.buffer.active.type = 'normal'; context.clear(); expect(context.take()).toBeUndefined()
  osc('C;command-2;'+btoa('echo next')); expect(context.take()).toBeUndefined()
  osc('D;old-command;0'); expect(context.take()).toBeUndefined()
  context.dispose()
})
