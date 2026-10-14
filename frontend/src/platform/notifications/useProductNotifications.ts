import { useMemo } from 'react'
import { useProductInteractions } from '../interactions/useProductInteractions'

/**
 * A product update received as a `notify` interaction event. Summary delivery
 * emits this in-app copy independently of external channels. The event ID
 * remains stable across hydration so the surface can retain dismissal state.
 * Historical messages remain visible until the user dismisses them.
 */
export type ProductNotification = {
  /** The interaction event id: stable across hydration, so a dismissal can be remembered. */
  id: string
  title: string
  message: string
}

export const NOTIFY_INTERACTION_KIND = 'notify'
const NOTIFY_KINDS = [NOTIFY_INTERACTION_KIND]

export function useProductNotifications(sessionId: string | undefined): ProductNotification[] {
  const interactions = useProductInteractions(sessionId, NOTIFY_KINDS)
  return useMemo(() => {
    const out: ProductNotification[] = []
    for (const it of interactions) {
      const message = typeof it.payload.message === 'string' ? it.payload.message.trim() : ''
      if (!it.id || !message) continue
      const title = typeof it.payload.title === 'string' ? it.payload.title.trim() : ''
      out.push({ id: it.id, title, message })
    }
    return out
  }, [interactions])
}
