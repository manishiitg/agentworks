// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { TURN_IN_FLIGHT_HOLD_MS, useHeldTurnInFlight } from './useHeldTurnInFlight'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let shown = false
function Probe({ inFlight, completed, tab }: { inFlight: boolean; completed: boolean; tab: string }) {
  shown = useHeldTurnInFlight(inFlight, completed, tab)
  return null
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

// The Stop/Send flicker: the in-flight flags dip for a moment during a run.
it('keeps Stop through a brief dip and releases it on completion or after the hold', async () => {
  const root = createRoot(document.createElement('div'))
  const render = (inFlight: boolean, completed = false, tab = 'a') =>
    act(async () => root.render(<Probe inFlight={inFlight} completed={completed} tab={tab} />))
  try {
    await render(true)
    expect(shown).toBe(true)
    await render(false) // a dip mid-run
    expect(shown).toBe(true)
    await render(true)
    await render(false)
    await act(async () => { vi.advanceTimersByTime(TURN_IN_FLIGHT_HOLD_MS + 10) })
    expect(shown).toBe(false) // the turn really ended

    await render(true)
    await render(false, true) // completion hides Stop at once
    expect(shown).toBe(false)

    await render(true, false, 'a')
    await render(false, false, 'b') // another tab never inherits the hold
    expect(shown).toBe(false)
  } finally {
    await act(async () => root.unmount())
  }
})
