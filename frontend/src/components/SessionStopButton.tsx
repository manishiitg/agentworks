import { useRef, useState } from 'react'
import { Loader2, Square } from 'lucide-react'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { useTabTurnActive } from '../hooks/useTabTurnActive'
import { Button } from './ui/Button'

interface SessionStopButtonProps {
  tabId: string
  footer?: boolean
}

// Shared by the composer and scheduled-run footer. This stops the session
// and background work; Escape remains a separate foreground interrupt.
export function SessionStopButton({ tabId, footer = false }: SessionStopButtonProps) {
  const tab = useChatStore(state => state.chatTabs[tabId])
  const [stopping, setStopping] = useState(false)
  const inFlight = useRef(false)

  // The same in-flight signal as the transcript's "Working…" row (PLAT-699).
  const turnActive = useTabTurnActive(tabId)

  const sessionId = tab?.sessionId
  if (!sessionId || (!turnActive && !stopping)) return null

  const stopSession = async () => {
    if (inFlight.current) return
    inFlight.current = true
    setStopping(true)
    try {
      await agentApi.stopSession(sessionId, true, !footer)
      const store = useChatStore.getState()
      store.setTabStreaming(tabId, false)
      store.setTabHasRunningBgAgents(tabId, false)
    } catch (error) {
      console.error('[SessionStopButton] Failed to stop session:', error)
      useChatStore.getState().addToast('Could not stop the session. Please try again.', 'error')
    } finally {
      inFlight.current = false
      setStopping(false)
    }
  }

  const button = (
    <Button
      type="button"
      onClick={() => void stopSession()}
      disabled={stopping}
      variant={footer ? 'destructive' : 'ghost'}
      size={footer ? 'sm' : 'icon'}
      className={footer ? 'gap-2' : 'h-7 w-7 p-0 text-muted-foreground hover:bg-destructive/10 hover:text-destructive'}
      data-testid={footer ? 'scheduled-run-stop-button' : 'chat-stop-button'}
      aria-label={stopping ? 'Stopping session' : 'Stop session and background work'}
      title="Stop session and background work"
    >
      {stopping
        ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
        : <Square className="h-3.5 w-3.5" fill="currentColor" />}
      {footer && (stopping ? 'Stopping…' : 'Stop run')}
    </Button>
  )

  return footer
    ? <div className="flex shrink-0 justify-end border-t border-border px-4 py-3">{button}</div>
    : button
}
