import { useCallback, useEffect, useRef, type PointerEvent as ReactPointerEvent } from 'react'

type DragHandlers = { onMove: (clientX: number) => void; onEnd: () => void }

/** Own the native listeners for one primary-pointer drag, including interrupted drags. */
export function usePointerDrag() {
  const cleanup = useRef<((notify: boolean) => void) | null>(null)
  const stop = useCallback(() => cleanup.current?.(false), [])
  useEffect(() => stop, [stop])

  const start = useCallback((event: ReactPointerEvent<HTMLElement>, handlers: DragHandlers, axis: 'x' | 'y' = 'x') => {
    if (event.button !== 0 || !event.isPrimary) return false
    cleanup.current?.(true)
    // React clears currentTarget after dispatch. Never retain the synthetic event.
    const target = event.currentTarget
    const pointerId = event.pointerId
    const doc = target.ownerDocument
    const view = doc.defaultView!
    event.preventDefault()
    target.setPointerCapture(pointerId)
    let ended = false
    const finish = (notify: boolean) => {
      if (ended) return
      ended = true
      target.removeEventListener('pointermove', move)
      target.removeEventListener('pointerup', end)
      target.removeEventListener('pointercancel', end)
      target.removeEventListener('lostpointercapture', end)
      view.removeEventListener('blur', blur)
      doc.removeEventListener('visibilitychange', visibility)
      cleanup.current = null
      if (target.hasPointerCapture(pointerId)) target.releasePointerCapture(pointerId)
      if (notify) handlers.onEnd()
    }
    const move = (event: PointerEvent) => {
      if (event.pointerId !== pointerId) return
      // Recover even if the release happened outside the browser window.
      if ((event.buttons & 1) === 0) { finish(true); return }
      handlers.onMove(axis === 'y' ? event.clientY : event.clientX)
    }
    const end = (event: PointerEvent) => { if (event.pointerId === pointerId) finish(true) }
    const blur = () => finish(true)
    const visibility = () => { if (doc.hidden) finish(true) }
    target.addEventListener('pointermove', move)
    target.addEventListener('pointerup', end)
    target.addEventListener('pointercancel', end)
    target.addEventListener('lostpointercapture', end)
    view.addEventListener('blur', blur)
    doc.addEventListener('visibilitychange', visibility)
    cleanup.current = finish
    return true
  }, [])

  return { start, stop }
}
