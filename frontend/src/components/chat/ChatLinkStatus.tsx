import { useChatStore } from '../../stores/useChatStore'

// Phone-friendly connection and delivery line above the message box
// (PLAT-856-509). Everything shown comes from the server's stream: the
// browser keeps no queue and decides nothing about delivery.
const STATE_TEXT = { sent: 'Sent', received: 'Received by your Mac', working: '', failed: 'Not delivered' } as const

export function ChatLinkStatus({ sessionId, tabId }: { sessionId?: string; tabId?: string }) {
  const link = useChatStore(state => sessionId ? state.sseLink[sessionId] : undefined)
  const message = useChatStore(state => sessionId ? state.messageStates[sessionId] : undefined)
  const pendingId = useChatStore(state => tabId ? state.chatTabs[tabId]?.config?.pendingSubmission?.id : undefined)
  if (!sessionId) return null
  // A send still waiting for the server's receipt shows as sending only while
  // the stream is down, so normal sends do not flash a status.
  const sending = Boolean(pendingId) && link === 'reconnecting' && message?.submission_id !== pendingId
  const delivery = sending ? 'Sending…' : message ? STATE_TEXT[message.state] : ''
  return <>
    {link === 'reconnecting' && <div role="status" aria-live="polite" className="h-6 shrink-0 bg-amber-500/15 px-4 text-[11px] leading-6 text-amber-800 dark:text-amber-200">Reconnecting… your work keeps running.</div>}
    {delivery && <p role="status" aria-live="polite" className="px-4 pt-1 text-right text-[11px] text-muted-foreground">{delivery}{message?.state === 'failed' && message.reason ? `: ${message.reason}` : ''}</p>}
  </>
}
