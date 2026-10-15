// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { EventDispatcher } from './EventDispatcher'
import type { PollingEvent } from '../../services/api-types'

// The server's turn_phase event feeds the Working line only. The detailed view printed it as "Unknown Event Type:
// turn_phase" with the raw JSON after a real Pi chat (2026-10-15).
const turnPhase = {
  type: 'turn_phase',
  timestamp: '2026-10-15T17:47:37.761443194Z',
  data: { data: { phase: 'running', component: 'turn_phase' } },
} as unknown as PollingEvent

let container: HTMLDivElement | null = null
afterEach(() => { container?.remove(); container = null })

describe('turn_phase in the detailed view', () => {
  it('renders nothing, not the raw event', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<EventDispatcher event={turnPhase} />) })
    expect(container.textContent || '').toBe('')
    await act(async () => { root.unmount() })
  })
})
