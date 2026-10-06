import { useEffect, useState } from 'react'

// How long the composer keeps showing Stop after the in-flight signal drops.
export const TURN_IN_FLIGHT_HOLD_MS = 2000

// During a running turn the foreground signal follows the server's busy,
// can_steer and terminal heuristics, which dip for a moment several times a
// second; the composer then swapped Stop and Send back and forth. Hold the
// in-flight state briefly after it drops, and release it at once when the turn
// really completes or the tab changes.
export function useHeldTurnInFlight(inFlight: boolean, completed: boolean, resetKey: string | null): boolean {
  const [held, setHeld] = useState(false)
  useEffect(() => {
    setHeld(false)
  }, [resetKey])
  useEffect(() => {
    if (inFlight) {
      setHeld(true)
      return
    }
    if (!held) return
    if (completed) {
      setHeld(false)
      return
    }
    const timer = window.setTimeout(() => setHeld(false), TURN_IN_FLIGHT_HOLD_MS)
    return () => window.clearTimeout(timer)
  }, [inFlight, completed, held])
  return inFlight || (held && !completed)
}
