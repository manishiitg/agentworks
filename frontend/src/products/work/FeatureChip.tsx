import { useEffect, useRef } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { setUpAgentFeature } from '../../utils/agentPurposeDraft'
import { featuresChanged, openCrewWorkEvent, useCrewFeatures } from './featuresApi'

/**
 * ◈ on an agent's top bar (#3210). Not in a feature: a click asks the agent, in this chat, to join the feature that fits its
 * work or start one; the agent knows the work better than a name box (owner, 19 Nov). In a feature: a click opens the
 * right panel's Work view on it. Leaving or switching is asked in chat too.
 */
export function FeatureChip({ projectId, tabId, main }: { projectId: string; tabId?: string; main: boolean }) {
  const { target, forTab, chatKeyFor } = useCrewFeatures(projectId)
  const activeTabId = useChatStore(state => state.activeTabId)
  const tab = tabId || activeTabId || undefined
  // Read the features again when this agent finishes a turn, so the chip shows the one it just joined.
  const streaming = useChatStore(state => (tab ? !!state.chatTabs[tab]?.isStreaming : false))
  const was = useRef(streaming)
  useEffect(() => {
    if (was.current && !streaming) featuresChanged()
    was.current = streaming
  }, [streaming])
  if (!target || !tab || !chatKeyFor(tab, main)) return null
  const mine = forTab(tab, main)
  const label = mine.length ? mine[0].name + (mine.length > 1 ? ` +${mine.length - 1}` : '') : 'Feature'
  const click = () => mine.length
    ? window.dispatchEvent(new CustomEvent(openCrewWorkEvent, { detail: { projectId } }))
    : setUpAgentFeature(tab)
  return <button type="button" onClick={click}
    title={mine.length ? `In ${mine.map(f => f.name).join(', ')}. Open its work.` : 'Ask this agent to put its work in a feature'}
    aria-label={mine.length ? `Features: ${mine.map(f => f.name).join(', ')}` : 'Ask this agent to put its work in a feature'}
    className={`flex h-6 max-w-40 shrink-0 items-center gap-1 rounded-full border px-2 text-[11px] ${mine.length ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:text-foreground'}`}>
    <span aria-hidden="true">◈</span><span className="min-w-0 truncate">{label}</span>
  </button>
}
