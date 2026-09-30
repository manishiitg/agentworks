// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Terminal } from '@xterm/xterm'
import { TerminalOutputQueue } from './terminalOutputQueue'

let terminal: Terminal | undefined
afterEach(() => { terminal?.dispose(); terminal = undefined; document.body.innerHTML = '' })

function slowTerminal() {
  const writes: (string | Uint8Array)[] = []
  const callbacks: (() => void)[] = []
  return {
    writes,
    write(data: string | Uint8Array, callback?: () => void) { writes.push(data); callbacks.push(callback!) },
    finish() { callbacks.shift()!() },
  }
}

describe('terminal output rendering', () => {
  it.each([
    new TextEncoder().encode('\x1b]0;unfinished title'),
    new TextEncoder().encode('\x1bPunfinished DCS'),
    new TextEncoder().encode('\x1b[3'),
    new Uint8Array([0xf0, 0x9f]),
  ])('recovers a seed after a disconnect in a partial control sequence or UTF-8 character (%j)', async partial => {
    terminal = new Terminal({ cols: 40, rows: 5 })
    const mount = document.createElement('div'); document.body.append(mount); terminal.open(mount)
    const queue = new TerminalOutputQueue(terminal)
    await new Promise<void>(resolve => queue.write(partial, resolve))
    await queue.invalidate()
    await new Promise<void>(resolve => queue.reseed('\x1bcreconnected π', resolve))
    await new Promise<void>(resolve => queue.write(new TextEncoder().encode('🙂'), resolve))
    expect(terminal.buffer.active.getLine(0)?.translateToString(true)).toBe('reconnected π🙂')
    queue.dispose()
  })

  it('keeps header and footer fixed after restoring a TUI’s origin and scrolling region', async () => {
    terminal = new Terminal({ cols: 40, rows: 5 })
    const mount = document.createElement('div'); document.body.append(mount); terminal.open(mount)
    const queue = new TerminalOutputQueue(terminal)
    await new Promise<void>(resolve => queue.reseed('\x1bcheader\r\none\r\ntwo\r\nthree\r\nfooter\x1b[2;4r\x1b[?6h\x1b[3;1H', resolve))
    await new Promise<void>(resolve => queue.write(new TextEncoder().encode('\r\nnew'), resolve))
    expect(Array.from({ length: 5 }, (_, index) => terminal!.buffer.active.getLine(index)?.translateToString(true)))
      .toEqual(['header', 'two', 'three', 'new', 'footer'])
    queue.dispose()
  })

  it('bounds a slow parser without dropping a byte and continuing the live stream', () => {
    const term = slowTerminal()
    const queue = new TerminalOutputQueue(term, 10)
    expect(queue.write(new Uint8Array(4))).toBe(true)
    expect(queue.write(new Uint8Array(6))).toBe(true)
    expect(queue.write(new Uint8Array(1))).toBe(false)
    expect(term.writes).toHaveLength(1)
    term.finish()
    expect(term.writes).toHaveLength(2)
    term.finish()
    expect(queue.write(new Uint8Array(10))).toBe(true)
  })

  it('waits for the active parse and discards queued old-width output and callbacks', async () => {
    const term = slowTerminal()
    const queue = new TerminalOutputQueue(term)
    const staleSeed = vi.fn()
    queue.write('old seed', staleSeed)
    queue.write('old width redraw')
    const fitNewGrid = vi.fn()
    const drained = queue.invalidate().then(fitNewGrid)
    await Promise.resolve()
    expect(fitNewGrid).not.toHaveBeenCalled()
    term.finish()
    await drained
    expect(fitNewGrid).toHaveBeenCalledOnce()
    expect(staleSeed).not.toHaveBeenCalled()
    queue.write('\x1bcnew seed')
    expect(term.writes).toEqual(['old seed', '\x1bcnew seed'])
  })

  it('stops rendering and resolves drain waits when the pane unmounts', async () => {
    const term = slowTerminal()
    const queue = new TerminalOutputQueue(term)
    const onParsed = vi.fn()
    queue.write('old', onParsed)
    queue.write('queued')
    const drained = queue.invalidate()
    queue.dispose()
    await drained
    term.finish()
    expect(onParsed).not.toHaveBeenCalled()
    expect(term.writes).toEqual(['old'])
    expect(queue.write('after unmount')).toBe(false)
  })

  it('preserves fragmented UTF-8 and ANSI bytes through the real xterm parser', async () => {
    terminal = new Terminal({ cols: 40, rows: 5 })
    const mount = document.createElement('div'); document.body.append(mount); terminal.open(mount)
    const queue = new TerminalOutputQueue(terminal)
    const bytes = new TextEncoder().encode('\x1b[?1049h\x1b[?1h\x1b[31mπ🙂\x1b[0m\r\nready')
    // Every byte is its own frame, including escape sequences and code points.
    await new Promise<void>(resolve => {
      for (let index = 0; index < bytes.length; index++) {
        expect(queue.write(bytes.slice(index, index + 1), index === bytes.length - 1 ? resolve : undefined)).toBe(true)
      }
    })
    expect(terminal.buffer.active.type).toBe('alternate')
    expect(terminal.modes.applicationCursorKeysMode).toBe(true)
    expect(terminal.buffer.active.getLine(0)?.translateToString(true)).toBe('π🙂')
    expect(terminal.buffer.active.getLine(1)?.translateToString(true)).toBe('ready')
    queue.dispose()
  })
})
