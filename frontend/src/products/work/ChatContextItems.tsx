import { useEffect, useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Check, Layers, RotateCcw } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { useChatPreferences } from '../../hooks/useChatPreferences'
import { formatTokens } from '../../utils/formatTokens'
import { ContextMeter } from '../../components/chat/ContextMeter'

export const menuRowClass = 'flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
export const menuLabelClass = 'px-2.5 pb-1 pt-1.5 text-[11px] font-medium text-muted-foreground'

function resetDate(iso: string | undefined, compact = false): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, compact
    ? { month: 'short', day: 'numeric' }
    : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

/** Each provider window keeps its own server-reported reset time. */
export function chatLimitsText(limits: ChatContextView['limits']): string {
  return (limits ?? []).map(limit => {
    const when = resetDate(limit.resets_at)
    return `${limit.label} ${Math.round(limit.used_percent)}%${when ? ` (resets ${when})` : ''}`
  }).join(' · ')
}

/** Keep the weekly reset date visible beside the 7d plan use. */
export function chatLimitsShort(limits: ChatContextView['limits']): string {
  return (limits ?? []).map(limit => {
    const when = limit.label === '7d' ? resetDate(limit.resets_at, true) : ''
    return `${limit.label} ${Math.round(limit.used_percent)}%${when ? ` (resets ${when})` : ''}`
  }).join(' · ')
}

/** Sent when the menu changes a chat's context, so the chip in the chat box shows the server's new view. */
export const chatContextChangedEvent = 'agentworks:chat-context-changed'
/** Sent by the chip in the chat box to open that chat's model menu. */
export const openChatContextMenuEvent = 'agentworks:open-chat-context-menu'

/**
 * The "Context" group of a chat's model menu (PLAT-872-71): how full this chat is, its account's plan limits,
 * and the switches and actions the server offers for it. Everything comes from the server; a choice is sent
 * back and the server's answer is shown. Mounted only while the menu is open, so it reads fresh numbers.
 */
export default function ChatContextItems({ sessionId, modelLabel }: { sessionId: string; modelLabel?: string }) {
  const [view, setView] = useState<ChatContextView | null>(null)
  const [busy, setBusy] = useState(false)
  const preferences = useChatPreferences()

  useEffect(() => {
    let cancelled = false
    if (sessionId) agentApi.getChatContext(sessionId).then(next => { if (!cancelled) setView(next) }).catch(() => undefined)
    return () => { cancelled = true }
  }, [sessionId])

  if (!view) return null
  const used = view.used_tokens ?? 0
  const windowTokens = view.window_tokens ?? 0
  const limits = chatLimitsText(view.limits)
  const offersSomething = Boolean(view.idle_compaction || view.can_compact || view.can_fresh)
  if (!(used > 0) && !limits && !offersSomething) return null

  const send = async (body: { idle_compaction?: boolean; action?: 'compact' | 'fresh' }) => {
    if (busy) return
    setBusy(true)
    try {
      const result = await agentApi.setChatContext({ session_id: sessionId, ...body })
      setView(result.context)
      window.dispatchEvent(new CustomEvent(chatContextChangedEvent, { detail: result.context }))
      if (result.message) useChatStore.getState().addToast(result.message, 'success')
    } catch (cause) {
      const reason = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
      useChatStore.getState().addToast(reason || 'That did not work. Try again in a moment.', 'error')
    } finally { setBusy(false) }
  }
  // Keep the menu open after a switch, so the person sees it change.
  const keepOpen = (event: Event) => event.preventDefault()

  return <>
    <Menu.Separator className="my-1 h-px bg-border first:hidden" />
    <Menu.Label className={menuLabelClass}>Context</Menu.Label>
    {limits && <div className="px-2.5 pb-1 text-[11px] text-muted-foreground" title="Your account's plan use, as the provider reports it">{modelLabel ? `${modelLabel} · ` : ''}{limits}</div>}
    {used > 0 && <div className="px-2.5 pb-1.5 pt-0.5">
      <div className="text-xs text-muted-foreground">{formatTokens(used)}{windowTokens > 0 ? ` of ${formatTokens(windowTokens)}` : ''} used</div>
      {windowTokens > 0 && <ContextMeter className="mt-1" used={used} windowTokens={windowTokens} />}
    </div>}
    {view.idle_compaction && <Menu.CheckboxItem className={`${menuRowClass} items-start`} checked={view.idle_compaction.on} disabled={busy} onSelect={keepOpen}
      onCheckedChange={checked => void send({ idle_compaction: checked === true })}>
      <span className="min-w-0 flex-1">
        <span className="block">Auto-compact when idle</span>
        {view.idle_compaction_hint && <span className="block text-[11px] leading-snug text-muted-foreground">{view.idle_compaction_hint}</span>}
      </span>
      <Menu.ItemIndicator className="pt-0.5"><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
    </Menu.CheckboxItem>}
    {view.can_compact && <Menu.Item className={menuRowClass} disabled={busy} onSelect={() => void send({ action: 'compact' })}>
      <Layers className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Compact now
    </Menu.Item>}
    {view.can_fresh && <Menu.Item className={menuRowClass} disabled={busy} onSelect={() => void send({ action: 'fresh' })}>
      <RotateCcw className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Fresh chat
    </Menu.Item>}
    {/* The person's own display choice, saved on the server for all their chats and devices; off until turned on. */}
    <Menu.CheckboxItem className={menuRowClass} checked={preferences.showContextIndicator} disabled={!preferences.ready || preferences.save.isPending} onSelect={keepOpen}
      onCheckedChange={checked => preferences.save.mutate(checked === true, { onError: () => useChatStore.getState().addToast('Could not save that. Try again in a moment.', 'error') })}>
      <span className="min-w-0 flex-1">Show in chat box</span>
      <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
    </Menu.CheckboxItem>
  </>
}
