import { create } from 'zustand'
import type { PendingQueuedMessage } from '../../shared/session/types'

/** What the queued message's own row in the chat needs to show its controls. The server queue stays the source of truth: DurableChatQueue polls it and owns the actions. */
export interface QueuedMessageControls {
  messages: Record<string, PendingQueuedMessage> // by client_message_id
  busyMessageId: string | null
  canEdit: boolean
  editable: boolean
  act: (message: PendingQueuedMessage, kind: 'edit' | 'steer' | 'retry' | 'remove') => void
}

interface Store {
  bySession: Record<string, QueuedMessageControls | undefined>
  set: (sessionId: string, controls: QueuedMessageControls | undefined) => void
}

export const useQueuedMessageControlsStore = create<Store>(set => ({
  bySession: {},
  set: (sessionId, controls) => set(state => {
    const bySession = { ...state.bySession }
    if (controls) bySession[sessionId] = controls
    else delete bySession[sessionId]
    return { bySession }
  }),
}))
