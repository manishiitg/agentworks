// @vitest-environment happy-dom
import React, { act, createRef } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TerminalSnapshot } from '../services/api-types'

vi.hoisted(() => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: () => null, setItem() {}, removeItem() {},
  } })
})

const runtime = vi.hoisted(() => ({
  terms: [] as any[], sockets: [] as any[], dimensions: { cols: 80, rows: 24 },
  fit: vi.fn(), observe: null as null | (() => void),
}))

vi.mock('../services/api', () => ({ agentApi: {}, getApiBaseUrl: () => 'http://localhost', getWsBaseUrl: () => 'ws://localhost' }))

vi.mock('@xterm/xterm', () => ({ Terminal: class {
  options: any; cols = 80; rows = 24
  writes: (string | Uint8Array)[] = []; pending: (() => void)[] = []
  buffer = { active: { baseY: 0, viewportY: 0 } }
  focus = vi.fn(); reset = vi.fn(); dispose = vi.fn(); scrollToBottom = vi.fn()
  resize = vi.fn((cols: number, rows: number) => { this.cols = cols; this.rows = rows })
  constructor(options: any) { this.options = options; runtime.terms.push(this) }
  loadAddon(addon: any) { addon.term = this }
  open() {}
  write(data: string | Uint8Array, callback?: () => void) { this.writes.push(data); this.pending.push(callback || (() => {})) }
  finish() { this.pending.shift()?.() }
  onData() { return { dispose() {} } }
  onBinary() { return { dispose() {} } }
  onScroll() { return { dispose() {} } }
  attachCustomKeyEventHandler() {}
  hasSelection() { return false }
} }))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class {
  term: any
  proposeDimensions() { return runtime.dimensions }
  fit() { runtime.fit(); Object.assign(this.term, runtime.dimensions) }
} }))
vi.mock('../utils/displayOnlyXterm', () => ({ installDisplayOnlyXtermGuards: () => ({ dispose() {} }), xtermCopyText: () => '' }))

import { LiveAttachXtermPane, StaticXtermPane } from './TerminalCenter'

class FakeSocket {
  static OPEN = 1
  readyState = 1
  binaryType = ''
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null
  onclose: ((event: { code: number }) => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn()
  send = vi.fn()
  url: string
  constructor(url: string) { this.url = url; runtime.sockets.push(this) }
  open() { this.onopen?.() }
  message(data: string) { this.onmessage?.({ data: new TextEncoder().encode(data).buffer }) }
  closed(code = 1006) { this.onclose?.({ code }) }
}

let root: Root
let host: HTMLDivElement
let contentRef: ReturnType<typeof createRef<HTMLDivElement>>
let loadSnapshot: ReturnType<typeof vi.fn<() => Promise<TerminalSnapshot>>>
const render = (extra: Record<string, unknown> = {}) => root.render(<LiveAttachXtermPane
  terminalId="terminal" sessionId="session" tmuxSession="tmux" contentRef={contentRef}
  xtermTheme={{ background: '#000' }} reconnectOnClose interactive
  streamUrl={() => 'ws://localhost/terminal'} loadSnapshot={loadSnapshot} {...extra}
/>)
const renderedText = (term: any) => term.writes.map((data: string | Uint8Array) => typeof data === 'string' ? data : new TextDecoder().decode(data)).join('')

beforeEach(async () => {
  vi.useFakeTimers()
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.stubGlobal('ResizeObserver', class { constructor(callback: () => void) { runtime.observe = callback } observe() {} disconnect() {} })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: runtime.dimensions.cols * 10, height: 400 }) as DOMRect)
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{ width: 800, height: 400 }] as unknown as DOMRectList)
  runtime.terms = []; runtime.sockets = []; runtime.dimensions = { cols: 80, rows: 24 }; runtime.fit.mockClear()
  loadSnapshot = vi.fn<() => Promise<TerminalSnapshot>>(async () => ({ terminal_id: 'terminal', tmux_session: 'tmux', active: true, content: 'snapshot', rows: [] }) as unknown as TerminalSnapshot)
  host = document.createElement('div'); document.body.append(host); root = createRoot(host); contentRef = createRef()
  await act(async () => render())
  await act(async () => vi.advanceTimersByTime(120))
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals()
})

describe('live terminal renderer recovery', () => {
  it('replaces saved snapshots after parsing finishes and skips superseded refreshes', async () => {
    const renderStatic = (content: string) => root.render(<StaticXtermPane content={content} contentRef={contentRef} xtermTheme={{ background: '#000' }} />)
    await act(async () => renderStatic('old snapshot'))
    const term = runtime.terms[1]
    expect(term.writes).toHaveLength(1)
    term.reset.mockClear()
    await act(async () => renderStatic('intermediate snapshot'))
    await act(async () => renderStatic('latest snapshot'))
    expect(term.reset).not.toHaveBeenCalled()
    expect(term.writes).toHaveLength(1)
    await act(async () => term.finish())
    expect(term.reset).toHaveBeenCalledOnce()
    expect(renderedText(term)).toContain('latest snapshot')
    expect(renderedText(term)).not.toContain('intermediate snapshot')
    expect(term.options.disableStdin).toBe(true)
  })

  it('enables input only after the seed is parsed and keeps input errors disabled', async () => {
    const socket = runtime.sockets[0]; const term = runtime.terms[0]
    await act(async () => { socket.open(); socket.message('\x1bc\x1b[?1hready') })
    expect(term.options.disableStdin).toBe(true)
    await act(async () => term.finish())
    expect(term.options.disableStdin).toBe(false)
    expect(term.focus).toHaveBeenCalledOnce()
    await act(async () => socket.onmessage({ data: JSON.stringify({ type: 'input_error', message: 'delivery failed' }) }))
    expect(term.options.disableStdin).toBe(true)
    expect(host.textContent).toContain('delivery failed')
  })

  it('does not re-enable input when an error arrives while the seed is parsing', async () => {
    const socket = runtime.sockets[0]; const term = runtime.terms[0]
    await act(async () => { socket.message('\x1bcready'); socket.onmessage({ data: JSON.stringify({ type: 'input_error', message: 'failed' }) }); term.finish() })
    expect(term.options.disableStdin).toBe(true)
  })

  it('ignores stale snapshots and old socket events after an explicit reconnect', async () => {
    const oldSocket = runtime.sockets[0]; const term = runtime.terms[0]
    let resolveSnapshot!: (snapshot: any) => void
    loadSnapshot.mockImplementation(() => new Promise(resolve => { resolveSnapshot = resolve }))
    await act(async () => { oldSocket.open(); oldSocket.message('\x1bcold'); term.finish() })
    await act(async () => oldSocket.onmessage({ data: JSON.stringify({ type: 'input_error', message: 'failed' }) }))
    await act(async () => oldSocket.closed())
    expect(loadSnapshot).toHaveBeenCalledOnce()
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(runtime.sockets).toHaveLength(2)
    const next = runtime.sockets[1]
    await act(async () => { next.open(); oldSocket.open(); oldSocket.closed(); resolveSnapshot({ tmux_session: 'tmux', active: true, content: 'STALE SNAPSHOT' }) })
    expect(renderedText(term)).not.toContain('STALE SNAPSHOT')
    // A stale close/open cannot cancel the replacement socket's seed deadline.
    await act(async () => vi.advanceTimersByTime(8000))
    expect(next.close).toHaveBeenCalledOnce()
    expect(runtime.sockets).toHaveLength(2)
  })

  it('drains old-width output before applying the same-socket reseed and enabling input', async () => {
    const socket = runtime.sockets[0]; const term = runtime.terms[0]
    await act(async () => { socket.message('\x1bcseed'); term.finish(); socket.message('in flight'); socket.message('queued old width') })
    runtime.fit.mockClear()
    runtime.dimensions = { cols: 100, rows: 24 }
    await act(async () => { runtime.observe?.(); vi.advanceTimersByTime(300) })
    const request = JSON.parse(socket.send.mock.calls[0][0])
    expect(request).toMatchObject({ type: 'resize', cols: 100, rows: 24, reseed: true })
    await act(async () => {
      socket.onmessage({ data: JSON.stringify({ type: 'reseed', epoch: request.epoch, cols: 100, rows: 24 }) })
      socket.message('\x1bcnew width')
      socket.message(' new live output')
    })
    expect(term.resize).not.toHaveBeenCalled()
    expect(term.reset).not.toHaveBeenCalled()
    expect(term.options.disableStdin).toBe(true)
    await act(async () => term.finish())
    expect(term.reset).toHaveBeenCalledOnce()
    expect(term.resize).toHaveBeenCalledWith(100, 24)
    expect(renderedText(term)).not.toContain('queued old width')
    expect(term.options.disableStdin).toBe(true)
    await act(async () => { term.finish(); term.finish() })
    expect(term.options.disableStdin).toBe(false)
    expect(renderedText(term)).toContain('new width')
    expect(renderedText(term)).toContain('new live output')
    expect(runtime.sockets).toHaveLength(1)
    expect(socket.close).not.toHaveBeenCalled()
  })

  it('does not replace an empty live screen’s modes with a metadata snapshot', async () => {
    const socket = runtime.sockets[0]; const term = runtime.terms[0]
    await act(async () => { socket.message('\x1bc\x1b[?1h'); term.finish(); render({ authoritativeContent: 'stale metadata' }) })
    expect(renderedText(term)).not.toContain('stale metadata')
  })

  it('drains a disconnected parser before a layout change and ignores its pending snapshot', async () => {
    const socket = runtime.sockets[0]; const term = runtime.terms[0]
    let resolveSnapshot!: (snapshot: any) => void
    loadSnapshot.mockImplementation(() => new Promise(resolve => { resolveSnapshot = resolve }))
    await act(async () => { socket.message('\x1bcseed'); term.finish(); socket.message('in flight'); socket.closed() })
    runtime.fit.mockClear(); runtime.dimensions = { cols: 100, rows: 24 }
    await act(async () => { runtime.observe?.(); vi.advanceTimersByTime(120) })
    expect(runtime.fit).not.toHaveBeenCalled()
    await act(async () => term.finish())
    expect(runtime.fit).toHaveBeenCalled()
    expect(term.cols).toBe(100)
    expect(runtime.sockets).toHaveLength(2)
    await act(async () => resolveSnapshot({ tmux_session: 'tmux', active: true, content: 'STALE' }))
    expect(renderedText(term)).not.toContain('STALE')
  })

  it('closes and reseeds when output outruns the renderer instead of keeping a broken stream', async () => {
    const socket = runtime.sockets[0]; const term = runtime.terms[0]
    await act(async () => { socket.message('\x1bcseed'); term.finish(); socket.message('in flight') })
    await act(async () => socket.onmessage({ data: new Uint8Array(16 * 1024 * 1024).buffer }))
    expect(socket.close).toHaveBeenCalledWith(4002, 'terminal render buffer full')
    expect(term.options.disableStdin).toBe(true)
    await act(async () => socket.message('after overflow'))
    expect(renderedText(term)).not.toContain('after overflow')
  })

  it('ignores late seed callbacks after unmount', async () => {
    const socket = runtime.sockets[0]; const term = runtime.terms[0]
    await act(async () => socket.message('\x1bcseed'))
    await act(async () => root.unmount())
    await act(async () => term.finish())
    expect(term.focus).not.toHaveBeenCalled()
    expect(term.options.disableStdin).toBe(true)
  })
})
