type TerminalWriter = { write(data: string | Uint8Array, callback?: () => void): void }
type PendingWrite = { data?: string | Uint8Array; action?: () => void; size: number; generation: number; callback?: () => void }

// Feed one batch at a time into xterm's asynchronous parser. Keeping the rest
// here lets reconnect/resize discard obsolete frames before xterm parses them.
// Never drop a live frame and continue: callers must reconnect and reseed when
// the bound is exceeded, since a dropped byte can split an escape sequence.
export class TerminalOutputQueue {
  private queue: PendingWrite[] = []
  private pendingSize = 0
  private writing = false
  private disposed = false
  private generation = 0
  private idleWaiters: (() => void)[] = []
  private terminal: TerminalWriter
  private maxPendingSize: number

  constructor(terminal: TerminalWriter, maxPendingSize = 16 * 1024 * 1024) {
    this.terminal = terminal
    this.maxPendingSize = maxPendingSize
  }

  write(data: string | Uint8Array, callback?: () => void): boolean {
    if (this.disposed) return false
    const size = typeof data === 'string' ? data.length * 2 : data.byteLength
    if (this.pendingSize + size > this.maxPendingSize || this.queue.length >= 4096) return false
    this.pendingSize += size
    this.queue.push({ data, size, generation: this.generation, callback })
    this.pump()
    return true
  }

  // Run geometry changes between parses, before any subsequently queued seed.
  barrier(action: () => void): boolean {
    if (this.disposed || this.queue.length >= 4096) return false
    this.queue.push({ action, size: 0, generation: this.generation })
    this.pump()
    return true
  }

  reseed(data: string, callback?: () => void): boolean {
    // RIS alone cannot escape an unfinished OSC/DCS from a dropped socket.
    // CAN cancels that parser state. Sending it as UTF-8 bytes also discards
    // any incomplete code point in xterm's byte decoder before the new seed.
    return this.write(new TextEncoder().encode('\x18' + data), callback)
  }

  // The one batch already handed to xterm cannot be cancelled. Wait for it
  // before changing the grid; everything else from the old grid is discarded.
  async invalidate(): Promise<void> {
    this.generation += 1
    for (const entry of this.queue) this.pendingSize -= entry.size
    this.queue = []
    await this.whenIdle()
  }

  whenIdle(): Promise<void> {
    if (this.disposed || (!this.writing && this.queue.length === 0)) return Promise.resolve()
    return new Promise<void>(resolve => this.idleWaiters.push(resolve))
  }

  dispose(): void {
    this.disposed = true
    this.generation += 1
    this.queue = []
    this.pendingSize = 0
    this.finishIdle()
  }

  private pump(): void {
    if (this.disposed || this.writing) return
    const entry = this.queue.shift()
    if (!entry) { this.finishIdle(); return }
    if (entry.action) {
      if (entry.generation === this.generation) entry.action()
      this.pump()
      return
    }
    if (entry.data === undefined) { this.pump(); return }
    this.writing = true
    this.terminal.write(entry.data, () => {
      if (this.disposed) return
      this.pendingSize -= entry.size
      this.writing = false
      if (entry.generation === this.generation) entry.callback?.()
      this.pump()
    })
  }

  private finishIdle(): void {
    const waiters = this.idleWaiters
    this.idleWaiters = []
    for (const resolve of waiters) resolve()
  }
}
