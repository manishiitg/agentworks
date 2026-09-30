// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Terminal } from '@xterm/xterm'
import { installInteractiveXtermKeys, sendTerminalInput, sendTerminalPaste } from './interactiveXterm'

const { copyToClipboard } = vi.hoisted(() => ({ copyToClipboard: vi.fn(async () => true) }))
vi.mock('./textUtils', () => ({ copyToClipboard }))
let term: Terminal | undefined
afterEach(() => { term?.dispose(); document.body.innerHTML = ''; vi.clearAllMocks() })

describe('native terminal interaction', () => {
  it('preserves alternate screen, application arrows, mouse and native Ctrl+C', async () => {
    term = new Terminal({ cols: 40, rows: 5 })
    const mount = document.createElement('div'); document.body.append(mount); term.open(mount)
    installInteractiveXtermKeys(term)
    const write = (data: string) => new Promise<void>(resolve => term!.write(data, resolve))
    await write('\x1b[?1049h\x1b[?1h\x1b[?1003h\x1b[?2004h')
    expect(term.buffer.active.type).toBe('alternate')
    expect(term.modes.applicationCursorKeysMode).toBe(true)
    expect(term.modes.bracketedPasteMode).toBe(true)
    expect(term.modes.mouseTrackingMode).not.toBe('none')
    const data: string[] = []; term.onData(value => data.push(value))
    const key = (key: string, extra: KeyboardEventInit = {}) => term!.textarea!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...extra }))
    key('ArrowUp', { keyCode: 38 }); key('ArrowDown', { keyCode: 40 }); key('c', { keyCode: 67, ctrlKey: true })
    expect(data).toEqual(['\x1bOA', '\x1bOB', '\x03'])
    await write('hello'); term.select(0, 0, 5); key('c', { keyCode: 67, ctrlKey: true })
    expect(copyToClipboard).toHaveBeenCalledWith('hello')
    expect(data).toHaveLength(3)
  })

  it('forwards UTF-8, slash commands and binary bytes exactly, with no reconnect replay', () => {
    const send = vi.fn(); const socket = { readyState: WebSocket.OPEN, send } as unknown as WebSocket
    expect(sendTerminalInput(socket, false, 'early\r')).toBe(false)
    expect(sendTerminalInput(socket, true, '/model π\r')).toBe(true)
    expect(send.mock.calls[0][0]).toEqual(new TextEncoder().encode('/model π\r'))
    expect(sendTerminalInput(socket, true, '\x80\xff', true)).toBe(true)
    expect(send.mock.calls[1][0]).toEqual(new Uint8Array([128, 255]))
    const closed = { readyState: WebSocket.CLOSED, send } as unknown as WebSocket
    expect(sendTerminalInput(closed, true, '\r')).toBe(false)
    expect(sendTerminalInput(socket, true, 'next')).toBe(true)
    expect(send).toHaveBeenCalledTimes(3)
  })

  it('lets tmux handle paste mode and surfaces send failures', () => {
    const send = vi.fn(); const socket = { readyState: WebSocket.OPEN, send } as unknown as WebSocket
    expect(sendTerminalPaste(socket, true, 'first\nsecond')).toBe(true)
    expect(JSON.parse(send.mock.calls[0][0])).toEqual({ type: 'paste', text: 'first\nsecond' })
    send.mockImplementation(() => { throw new Error('gone') })
    expect(sendTerminalInput(socket, true, '\r')).toBe(false)
    expect(sendTerminalPaste(socket, true, 'paste')).toBe(false)
  })
})
