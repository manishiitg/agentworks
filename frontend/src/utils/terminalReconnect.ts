import type { TerminalSnapshot } from '../services/api-types'

const INITIAL_RECONNECT_DELAY_MS = 500
const MAX_RECONNECT_DELAY_MS = 5000

export type TerminalGrid = { cols: number; rows: number }
export type TerminalGridChange = 'none' | 'rows-only' | 'columns'

export function terminalReconnectDelayMs(failedAttempts: number): number {
  const attempt = Math.max(0, Math.floor(failedAttempts))
  return Math.min(MAX_RECONNECT_DELAY_MS, INITIAL_RECONNECT_DELAY_MS * (2 ** attempt))
}

export function terminalSnapshotCanReconnect(snapshot: TerminalSnapshot): boolean {
  const state = (snapshot.state || '').trim().toLowerCase()
  if (state === 'completed' || state === 'failed' || state === 'closing' || state === 'stale') return false
  if (!snapshot.tmux_session) return false
  return Boolean(snapshot.active) || state === 'running' || state === 'idle' || state === ''
}

// A column change is a connection lifecycle event: xterm must not be resized
// while bytes wrapped for the old width can still arrive, or they land on the
// new grid and scramble. A row-only change does not alter wrapping and can use
// the existing socket's resize control frame, preserving browser scrollback.
// These classifiers/planners keep that distinction independently testable.

export function terminalGridChange(
  current: TerminalGrid,
  proposed: TerminalGrid | undefined,
  minimum: TerminalGrid,
): TerminalGridChange {
  if (!proposed || proposed.cols < minimum.cols || proposed.rows < minimum.rows) return 'none'
  if (proposed.cols !== current.cols) return 'columns'
  if (proposed.rows !== current.rows) return 'rows-only'
  return 'none'
}

// Steps a geometry change performs, IN ORDER. suspend-output must come first
// (it stops the old socket's bytes from reaching xterm) and fit must come after
// close-socket, so no old-width byte can be interpreted on the new grid.
export type GeometryChangeStep = 'suspend-output' | 'close-socket' | 'fit' | 'open-socket'

// The ordered steps performed once the closing socket reports back. The backend
// re-seeds on every connect, so open-socket implies the reseed.
export const GEOMETRY_RECONNECT_AFTER_CLOSE: readonly GeometryChangeStep[] = ['fit', 'open-socket']

export function planGeometryChange(input: {
  hasSocket: boolean
  alreadyPending: boolean
  needsReconnect: boolean
  superseded: boolean
}): GeometryChangeStep[] {
  if (!input.needsReconnect || input.alreadyPending) return []
  // A superseded pane is intentionally detached; reconnecting would steal the
  // terminal back from the window that owns it.
  if (input.superseded) return []
  // No socket means a reconnect is already pending. Fit now so that reconnect
  // opens at the latest geometry, but do not start a second one.
  if (!input.hasSocket) return ['fit']
  return ['suspend-output', 'close-socket']
}

// Server close codes that are FINAL: reconnecting cannot succeed, so the pane
// shows a final state instead of looping (mirrors terminal_live_attach.go).
export const LIVE_ATTACH_ACCESS_REVOKED_CLOSE_CODE = 4003
export const LIVE_ATTACH_CLI_EXITED_CLOSE_CODE = 4004

export type LiveAttachCloseAction =
  | 'ignore'
  | 'superseded'
  | 'access-revoked'
  | 'cli-exited'
  | 'geometry-reconnect'
  | 'recover'

// Classifies why a live-attach socket closed. Order matters: the superseded
// and other final codes are checked before every reconnect path, because
// reconnecting after an eviction would evict whoever replaced this viewer and
// start a ping-pong that never converges, and reconnecting after a revoked
// access or an exited CLI can never produce a live stream.
export function planLiveAttachClose(input: {
  code: number
  paneClosed: boolean
  wasCurrentSocket: boolean
  resizeReconnectPending: boolean
  reconnectOnClose: boolean
  supersededCloseCode: number
}): LiveAttachCloseAction {
  if (input.paneClosed || !input.wasCurrentSocket) return 'ignore'
  if (input.code === input.supersededCloseCode) return 'superseded'
  if (input.code === LIVE_ATTACH_ACCESS_REVOKED_CLOSE_CODE) return 'access-revoked'
  if (input.code === LIVE_ATTACH_CLI_EXITED_CLOSE_CODE) return 'cli-exited'
  if (input.resizeReconnectPending) return 'geometry-reconnect'
  if (!input.reconnectOnClose) return 'ignore'
  return 'recover'
}

export function terminalGridNeedsReconnect(
  current: TerminalGrid,
  proposed: TerminalGrid | undefined,
  minimum: TerminalGrid,
): boolean {
  return terminalGridChange(current, proposed, minimum) === 'columns'
}

// In-band reseed (same socket). A column change no longer reconnects: the
// client sends {type:'resize', cols, rows, reseed:true, epoch}, drops every
// binary frame until the server's {type:'reseed', epoch} text marker, then
// resets xterm and writes the seed that follows. Until the marker the old
// screen stays visible. A newer request supersedes an older one (its marker is
// ignored); no marker within the timeout falls back to the reconnect path, and
// a server that has never answered one is treated as not supporting reseed.
export const LIVE_ATTACH_INBAND_RESEED_TIMEOUT_MS = 8000
// Trailing debounce for a column change: a panel drag fires many widths, and
// each reseed is a full capture.
export const LIVE_ATTACH_INBAND_RESEED_DEBOUNCE_MS = 300

export type ReseedRequest = { type: 'resize'; cols: number; rows: number; reseed: true; epoch: number }

export type ReseedMarkerResult =
  // The pending request's marker: reset xterm, resize it to grid, and treat
  // the next binary frame as a fresh seed.
  | { action: 'apply'; grid: TerminalGrid }
  | { action: 'ignore' }

export class LiveAttachReseedTracker {
  private epoch = 0
  private pending: { epoch: number; grid: TerminalGrid } | null = null
  private markerSeen = false
  private unsupported = false

  // Whether an in-band reseed should be attempted (vs the reconnect path).
  get supported(): boolean {
    return !this.unsupported
  }

  get pendingGrid(): TerminalGrid | null {
    return this.pending ? { ...this.pending.grid } : null
  }

  get pendingEpoch(): number | null {
    return this.pending ? this.pending.epoch : null
  }

  // Starts a request and returns the frame to send. Any older pending request
  // is superseded.
  begin(grid: TerminalGrid): ReseedRequest {
    this.epoch += 1
    this.pending = { epoch: this.epoch, grid: { ...grid } }
    return { type: 'resize', cols: grid.cols, rows: grid.rows, reseed: true, epoch: this.epoch }
  }

  // Binary frames are dropped while a request is pending: they may be wrapped
  // for the old width, and the seed after the marker covers them.
  shouldDropBinary(): boolean {
    return this.pending !== null
  }

  onMarker(marker: { epoch?: unknown; cols?: unknown; rows?: unknown }): ReseedMarkerResult {
    if (!this.pending || marker.epoch !== this.pending.epoch) return { action: 'ignore' }
    this.markerSeen = true
    const cols = typeof marker.cols === 'number' && marker.cols > 0 ? marker.cols : this.pending.grid.cols
    const rows = typeof marker.rows === 'number' && marker.rows > 0 ? marker.rows : this.pending.grid.rows
    this.pending = null
    return { action: 'apply', grid: { cols, rows } }
  }

  // Called when the request's timer fires. Returns true when that request is
  // still pending, i.e. the caller must fall back to reconnecting.
  timeout(epoch: number): boolean {
    if (!this.pending || this.pending.epoch !== epoch) return false
    this.pending = null
    if (!this.markerSeen) this.unsupported = true
    return true
  }

  // The socket closed: its replacement starts with a full seed of its own.
  cancel(): void {
    this.pending = null
  }
}

// Parses a text frame as a reseed marker; null for anything else.
export function parseReseedMarker(data: string): { epoch?: unknown; cols?: unknown; rows?: unknown } | null {
  if (!data.startsWith('{')) return null
  try {
    const parsed = JSON.parse(data) as { type?: unknown }
    if (parsed && typeof parsed === 'object' && parsed.type === 'reseed') return parsed as { epoch?: unknown }
  } catch {
    // not a control frame
  }
  return null
}

export type LiveAttachFrameRoute =
  // Write to xterm as usual (seed or live output).
  | { kind: 'output' }
  // Discard: old-width bytes while a reseed is pending.
  | { kind: 'drop' }
  // A marker for a superseded/unknown request: discard it.
  | { kind: 'ignore-marker' }
  // The pending request's marker. The ONLY route on which the pane resets
  // xterm during a reseed; the next binary frame is the new seed.
  | { kind: 'reseed'; grid: TerminalGrid }

// Routes one incoming socket frame through the reseed state. Output frames
// are binary; text frames are control markers (older servers may send none).
export function routeLiveAttachFrame(tracker: LiveAttachReseedTracker, data: unknown): LiveAttachFrameRoute {
  if (typeof data === 'string') {
    const marker = parseReseedMarker(data)
    if (marker) {
      const result = tracker.onMarker(marker)
      return result.action === 'apply' ? { kind: 'reseed', grid: result.grid } : { kind: 'ignore-marker' }
    }
    return tracker.shouldDropBinary() ? { kind: 'drop' } : { kind: 'output' }
  }
  return tracker.shouldDropBinary() ? { kind: 'drop' } : { kind: 'output' }
}
