import { useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { currentNextPrompt, nextPromptFields } from '../../utils/nextPrompt'

// Existing account-scoped session stream, no poller or persistence. A real
// draft, attachment, picker, busy turn or stopped composer takes precedence.
export function useNextPrompt(session: string | null, available: boolean) {
  const event = useChatStore(state => session ? currentNextPrompt(state.tabEvents[session]) : undefined)
  const [dismissed, setDismissed] = useState<string>()
  const text = available && event?.id !== dismissed ? (event && nextPromptFields(event)?.text) || '' : ''
  return { text, dismiss: () => setDismissed(event?.id) }
}
