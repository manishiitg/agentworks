import { LoadingIndicator } from './ui/LoadingIndicator'
import { useEffect, useRef, useState } from 'react'
import { Square } from 'lucide-react'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { useTabTurnActive } from '../hooks/useTabTurnActive'
import { Button } from './ui/Button'

const FORCE_STOP_AFTER_MS = 10_000

interface SessionStopButtonProps {
  tabId: string
  footer?: boolean
}

// Shared by the composer and scheduled-run footer. The composer's Stop interrupts the current turn (like Escape);
// the footer's Stop ends the whole scheduled/triggered run and its background work.
export function SessionStopButton({ tabId, footer = false }: SessionStopButtonProps) {
  const tab = useChatStore(state => state.chatTabs[tabId])
  const [stopping, setStopping] = useState(false)
  const inFlight = useRef(false)

  // The same in-flight signal as the transcript's "Working…" row (PLAT-699). The composer counts only the CLI's
  // own turn; background work has its own pill with per-item Stop (PLAT-705). The footer stops the whole run.
  const turnActive = useTabTurnActive(tabId, footer ? 'any' : 'foreground')

  // Force stop (#3188): when the agent is still working 10 s after Stop, offer to close its program for this chat.
  const [stopSentAt, setStopSentAt] = useState(0)
  const [offerForce, setOfferForce] = useState(false)
  useEffect(() => {
    if (!turnActive) { setStopSentAt(0); setOfferForce(false); return }
    if (!stopSentAt || footer) return
    const timer = window.setTimeout(() => setOfferForce(true), Math.max(0, stopSentAt + FORCE_STOP_AFTER_MS - Date.now()))
    return () => window.clearTimeout(timer)
  }, [turnActive, stopSentAt, footer])

  const sessionId = tab?.sessionId
  if (tab?.metadata?.agentProfileTabCanWrite === false || !sessionId || (!turnActive && !stopping)) return null

  const forceStop = async () => {
    if (inFlight.current) return
    inFlight.current = true
    setStopping(true)
    try {
      await agentApi.cancelCurrentTurn(sessionId, true)
      useChatStore.getState().setTabStreaming(tabId, false)
      setOfferForce(false)
    } catch (error) {
      console.error('[SessionStopButton] Force stop failed:', error)
      useChatStore.getState().addToast('Could not force stop. Please try again.', 'error')
    } finally {
      inFlight.current = false
      setStopping(false)
    }
  }

  const stopSession = async () => {
    if (inFlight.current) return
    inFlight.current = true
    setStopping(true)
    try {
      // Two stops (owner, 2026-10-07). In a chat, Stop only interrupts what the coding CLI is doing in tmux
      // (/api/session/cancel-turn, the same as Escape; each adapter sends its own interrupt). Steps, workflows and
      // background agents the chat started keep running. On a scheduled or triggered run the footer stops the run.
      if (!footer) {
        await agentApi.cancelCurrentTurn(sessionId)
        useChatStore.getState().setTabStreaming(tabId, false)
        setStopSentAt(value => value || Date.now())
      } else {
        await agentApi.stopSession(sessionId, true, false)
        const store = useChatStore.getState()
        store.setTabStreaming(tabId, false)
        store.setTabHasRunningBgAgents(tabId, false)
      }
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
      data-theme-action="stop"
      type="button"
      onClick={() => void stopSession()}
      disabled={stopping}
      variant={footer ? 'destructive' : 'ghost'}
      size={footer ? 'sm' : 'icon'}
      className={footer ? 'gap-2' : 'h-7 w-7 rounded-full p-0 bg-diff-del/15 text-diff-del hover:bg-diff-del/25 hover:text-diff-del'}
      data-testid={footer ? 'scheduled-run-stop-button' : 'chat-stop-button'}
      aria-label={stopping ? 'Stopping' : footer ? 'Stop run and background work' : 'Stop the current turn'}
      title={footer ? 'Stop run and background work' : 'Stop the current turn (background work keeps running)'}
    >
      {stopping
        ? <LoadingIndicator className="h-3.5 w-3.5" />
        : <Square className="h-3.5 w-3.5" fill="currentColor" />}
      {footer && (stopping ? 'Stopping…' : 'Stop run')}
    </Button>
  )

  if (offerForce && !footer) return <span className="inline-flex items-center gap-1">
    <Button type="button" variant="ghost" size="sm" onClick={() => void forceStop()} disabled={stopping} data-testid="chat-force-stop-button"
      title="The agent did not stop. This closes its program for this chat; anything it started in the background stops too. Your next message continues the conversation."
      className="h-7 gap-1 rounded-full px-2.5 text-xs bg-diff-del/15 text-diff-del hover:bg-diff-del/25 hover:text-diff-del">
      {stopping ? <LoadingIndicator className="h-3.5 w-3.5" /> : <Square className="h-3 w-3" fill="currentColor" />}Force stop
    </Button>
  </span>

  return footer
    ? <div className="flex shrink-0 justify-end border-t border-border px-4 py-3">{button}</div>
    : button
}
