import { describe, expect, it } from 'vitest'
import { paintedTelemetryTransport } from './chatDeliveryTelemetry'

describe('paintedTelemetryTransport', () => {
  it('marks events emitted before the tab opened as react_restore', () => {
    const openedAt = Date.parse('2026-11-26T12:00:33.000Z')
    expect(paintedTelemetryTransport('2026-11-26T12:00:00.000Z', openedAt)).toBe('react_restore')
    expect(paintedTelemetryTransport('2026-11-26T12:00:32.000Z', openedAt)).toBe('react_restore')
  })

  it('keeps live events emitted after or just before tab open as react', () => {
    const openedAt = Date.parse('2026-11-26T12:00:33.000Z')
    expect(paintedTelemetryTransport('2026-11-26T12:00:32.800Z', openedAt)).toBe('react')
    expect(paintedTelemetryTransport('2026-11-26T12:00:35.000Z', openedAt)).toBe('react')
  })

  it('defaults to react when timestamp or open time is missing', () => {
    expect(paintedTelemetryTransport(undefined, Date.now())).toBe('react')
    expect(paintedTelemetryTransport('not-a-date', Date.now())).toBe('react')
    expect(paintedTelemetryTransport('2026-11-26T12:00:00.000Z', 0)).toBe('react')
  })
})
