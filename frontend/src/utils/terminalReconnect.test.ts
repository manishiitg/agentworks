import { describe, expect, it } from 'vitest'
import type { TerminalSnapshot } from '../services/api-types'
import {
  GEOMETRY_RECONNECT_AFTER_CLOSE,
  LIVE_ATTACH_ACCESS_REVOKED_CLOSE_CODE,
  LIVE_ATTACH_CLI_EXITED_CLOSE_CODE,
  LiveAttachReseedTracker,
  planGeometryChange,
  planLiveAttachClose,
  routeLiveAttachFrame,
  terminalGridChange,
  terminalGridNeedsReconnect,
  terminalReconnectDelayMs,
  terminalSnapshotCanReconnect,
} from './terminalReconnect'

function snapshot(patch: Partial<TerminalSnapshot>): TerminalSnapshot {
  return {
    terminal_id: 'terminal-1',
    session_id: 'session-1',
    active: true,
    state: 'running',
    tmux_session: 'tmux-1',
    content: '',
    rows: [],
    chunk_index: 0,
    status: {},
    created_at: '',
    updated_at: '',
    ...patch,
  }
}

describe('terminal reconnect recovery', () => {
  it('backs off quickly and caps retries at five seconds', () => {
    expect([0, 1, 2, 3, 4, 20].map(terminalReconnectDelayMs))
      .toEqual([500, 1000, 2000, 4000, 5000, 5000])
  })

  it('continues for live and idle tmux panes', () => {
    expect(terminalSnapshotCanReconnect(snapshot({ state: 'running' }))).toBe(true)
    expect(terminalSnapshotCanReconnect(snapshot({ active: false, state: 'idle' }))).toBe(true)
  })

  it('stops for settled or detached panes', () => {
    expect(terminalSnapshotCanReconnect(snapshot({ active: false, state: 'completed' }))).toBe(false)
    expect(terminalSnapshotCanReconnect(snapshot({ state: 'stale' }))).toBe(false)
    expect(terminalSnapshotCanReconnect(snapshot({ tmux_session: '' }))).toBe(false)
  })

  it('reconnects only when a usable grid changes width', () => {
    const current = { cols: 120, rows: 40 }
    const minimum = { cols: 40, rows: 10 }

    expect(terminalGridNeedsReconnect(current, { cols: 100, rows: 40 }, minimum)).toBe(true)
    expect(terminalGridNeedsReconnect(current, { cols: 120, rows: 42 }, minimum)).toBe(false)
    expect(terminalGridNeedsReconnect(current, { cols: 120, rows: 40 }, minimum)).toBe(false)
    expect(terminalGridNeedsReconnect(current, { cols: 20, rows: 8 }, minimum)).toBe(false)
    expect(terminalGridNeedsReconnect(current, undefined, minimum)).toBe(false)
  })

  it('distinguishes row-only resize from width-changing reconnect', () => {
    const current = { cols: 117, rows: 43 }
    const minimum = { cols: 40, rows: 10 }

    expect(terminalGridChange(current, { cols: 117, rows: 38 }, minimum)).toBe('rows-only')
    expect(terminalGridChange(current, { cols: 118, rows: 38 }, minimum)).toBe('columns')
    expect(terminalGridChange(current, current, minimum)).toBe('none')
    expect(terminalGridChange(current, { cols: 20, rows: 8 }, minimum)).toBe('none')
  })
})

describe('geometry change ordering', () => {
  const base = { hasSocket: true, alreadyPending: false, needsReconnect: true, superseded: false }

  it('suspends output before closing the socket', () => {
    // The whole point of the geometry reconnect: xterm must stop accepting the
    // old-width socket's bytes BEFORE anything closes or resizes, or bytes
    // wrapped for the old width land on the new grid and scramble.
    expect(planGeometryChange(base)).toEqual(['suspend-output', 'close-socket'])
  })

  it('fits only after the socket has closed, then reopens', () => {
    // The second half runs from onclose. fit must NOT appear in the first half:
    // resizing xterm while the socket is still open is the original bug.
    expect(planGeometryChange(base)).not.toContain('fit')
    expect(GEOMETRY_RECONNECT_AFTER_CLOSE).toEqual(['fit', 'open-socket'])
  })

  it('does nothing when the grid is unchanged', () => {
    expect(planGeometryChange({ ...base, needsReconnect: false })).toEqual([])
  })

  it('does not start a second reconnect while one is pending', () => {
    expect(planGeometryChange({ ...base, alreadyPending: true })).toEqual([])
  })

  it('only re-fits when a reconnect is already pending with no socket', () => {
    // Fit so the pending reconnect opens at the latest geometry, but do not
    // start a competing one.
    expect(planGeometryChange({ ...base, hasSocket: false })).toEqual(['fit'])
  })

  it('never reconnects a superseded pane', () => {
    // Reconnecting here would steal the terminal back from the window that
    // owns it, and that window would steal it back — a ping-pong with no
    // convergence, since a successful seed resets the client backoff.
    expect(planGeometryChange({ ...base, superseded: true })).toEqual([])
    expect(planGeometryChange({ ...base, superseded: true, hasSocket: false })).toEqual([])
  })
})

describe('live-attach close classification', () => {
  const SUPERSEDED = 4001
  const base = {
    code: 1006,
    paneClosed: false,
    wasCurrentSocket: true,
    resizeReconnectPending: false,
    reconnectOnClose: true,
    supersededCloseCode: SUPERSEDED,
  }

  it('recovers from an ordinary disconnect', () => {
    expect(planLiveAttachClose(base)).toBe('recover')
  })

  it('continues the geometry reconnect when one is pending', () => {
    expect(planLiveAttachClose({ ...base, resizeReconnectPending: true })).toBe('geometry-reconnect')
  })

  it('treats the superseded code as terminal, even mid-geometry-reconnect', () => {
    // Eviction must win over every reconnect path, otherwise two open tabs
    // evict each other in a loop.
    expect(planLiveAttachClose({ ...base, code: SUPERSEDED })).toBe('superseded')
    expect(planLiveAttachClose({ ...base, code: SUPERSEDED, resizeReconnectPending: true })).toBe('superseded')
    expect(planLiveAttachClose({ ...base, code: SUPERSEDED, reconnectOnClose: false })).toBe('superseded')
  })

  it('ignores unmounted panes and stale sockets', () => {
    expect(planLiveAttachClose({ ...base, paneClosed: true })).toBe('ignore')
    expect(planLiveAttachClose({ ...base, wasCurrentSocket: false })).toBe('ignore')
    // A stale socket must not be able to trigger a take-over either.
    expect(planLiveAttachClose({ ...base, code: SUPERSEDED, wasCurrentSocket: false })).toBe('ignore')
  })

  it('does not reconnect a settled terminal', () => {
    expect(planLiveAttachClose({ ...base, reconnectOnClose: false })).toBe('ignore')
  })

  it('treats access-revoked and CLI-exited as final states, never reconnects', () => {
    expect(LIVE_ATTACH_ACCESS_REVOKED_CLOSE_CODE).toBe(4003)
    expect(LIVE_ATTACH_CLI_EXITED_CLOSE_CODE).toBe(4004)
    for (const patch of [{}, { resizeReconnectPending: true }, { reconnectOnClose: false }]) {
      expect(planLiveAttachClose({ ...base, ...patch, code: 4003 })).toBe('access-revoked')
      expect(planLiveAttachClose({ ...base, ...patch, code: 4004 })).toBe('cli-exited')
    }
    // Stale sockets stay ignored.
    expect(planLiveAttachClose({ ...base, code: 4004, wasCurrentSocket: false })).toBe('ignore')
  })

  it('never reconnects a pane left in a final state on a later geometry change', () => {
    // The pane passes superseded||finalState into the planner.
    expect(planGeometryChange({ hasSocket: false, alreadyPending: false, needsReconnect: true, superseded: true })).toEqual([])
  })
})

describe('in-band reseed', () => {
  const bin = (text: string) => new TextEncoder().encode(text).buffer
  const marker = (epoch: number, cols = 90, rows = 30) => JSON.stringify({ type: 'reseed', epoch, cols, rows })

  it('sends a reseed resize frame with an increasing epoch', () => {
    const tracker = new LiveAttachReseedTracker()
    expect(tracker.begin({ cols: 90, rows: 30 })).toEqual({ type: 'resize', cols: 90, rows: 30, reseed: true, epoch: 1 })
    expect(tracker.begin({ cols: 80, rows: 30 }).epoch).toBe(2)
  })

  it('passes output through when no reseed is pending', () => {
    const tracker = new LiveAttachReseedTracker()
    expect(routeLiveAttachFrame(tracker, bin('live'))).toEqual({ kind: 'output' })
  })

  it('drops binary frames until the matching marker, and resets only on the marker', () => {
    const tracker = new LiveAttachReseedTracker()
    const { epoch } = tracker.begin({ cols: 90, rows: 30 })
    // Old-width output keeps arriving; none of it is written, and none of it
    // triggers a reset (the old screen stays visible meanwhile).
    const routes = ['a', 'b', 'c'].map(chunk => routeLiveAttachFrame(tracker, bin(chunk)))
    expect(routes.every(route => route.kind === 'drop')).toBe(true)
    expect(routes.some(route => route.kind === 'reseed')).toBe(false)

    expect(routeLiveAttachFrame(tracker, marker(epoch))).toEqual({ kind: 'reseed', grid: { cols: 90, rows: 30 } })
    // The seed and live output after the marker are written normally.
    expect(routeLiveAttachFrame(tracker, bin('\x1bcseed'))).toEqual({ kind: 'output' })
    expect(routeLiveAttachFrame(tracker, bin('live'))).toEqual({ kind: 'output' })
    expect(tracker.pendingEpoch).toBeNull()
  })

  it('ignores stale markers from a superseded request', () => {
    const tracker = new LiveAttachReseedTracker()
    const first = tracker.begin({ cols: 90, rows: 30 })
    const second = tracker.begin({ cols: 70, rows: 30 })
    // The first request's marker and seed arrive: ignored, seed still dropped.
    expect(routeLiveAttachFrame(tracker, marker(first.epoch))).toEqual({ kind: 'ignore-marker' })
    expect(routeLiveAttachFrame(tracker, bin('seed@90'))).toEqual({ kind: 'drop' })
    expect(routeLiveAttachFrame(tracker, marker(second.epoch, 70, 30))).toEqual({ kind: 'reseed', grid: { cols: 70, rows: 30 } })
    // A late duplicate/older marker after completion is ignored too.
    expect(routeLiveAttachFrame(tracker, marker(first.epoch))).toEqual({ kind: 'ignore-marker' })
    expect(routeLiveAttachFrame(tracker, bin('live'))).toEqual({ kind: 'output' })
  })

  it('uses the geometry the server applied (clamped) when the marker carries it', () => {
    const tracker = new LiveAttachReseedTracker()
    const { epoch } = tracker.begin({ cols: 900, rows: 30 })
    expect(routeLiveAttachFrame(tracker, marker(epoch, 500, 30))).toEqual({ kind: 'reseed', grid: { cols: 500, rows: 30 } })
    const next = tracker.begin({ cols: 88, rows: 31 })
    expect(routeLiveAttachFrame(tracker, JSON.stringify({ type: 'reseed', epoch: next.epoch }))).toEqual({ kind: 'reseed', grid: { cols: 88, rows: 31 } })
  })

  it('falls back to reconnecting when no marker arrives in time, and stops trying reseed on old servers', () => {
    const tracker = new LiveAttachReseedTracker()
    const { epoch } = tracker.begin({ cols: 90, rows: 30 })
    expect(tracker.timeout(epoch)).toBe(true)
    // After the fallback, output is no longer dropped (the reconnect takes over).
    expect(routeLiveAttachFrame(tracker, bin('x'))).toEqual({ kind: 'output' })
    // A server that never answered is treated as not supporting reseed.
    expect(tracker.supported).toBe(false)
  })

  it('does not fall back for a timer whose request already completed or was superseded', () => {
    const tracker = new LiveAttachReseedTracker()
    const first = tracker.begin({ cols: 90, rows: 30 })
    const second = tracker.begin({ cols: 80, rows: 30 })
    expect(tracker.timeout(first.epoch)).toBe(false)
    routeLiveAttachFrame(tracker, marker(second.epoch, 80, 30))
    expect(tracker.timeout(second.epoch)).toBe(false)
    expect(tracker.supported).toBe(true)
    // A later timeout on a server that has answered before stays on reseed.
    const third = tracker.begin({ cols: 70, rows: 30 })
    expect(tracker.timeout(third.epoch)).toBe(true)
    expect(tracker.supported).toBe(true)
  })

  it('clears the pending request when the socket closes', () => {
    const tracker = new LiveAttachReseedTracker()
    tracker.begin({ cols: 90, rows: 30 })
    tracker.cancel()
    expect(tracker.pendingGrid).toBeNull()
    expect(routeLiveAttachFrame(tracker, bin('seed'))).toEqual({ kind: 'output' })
  })
})
