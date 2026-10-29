import { useEffect, useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Check, Layers, RotateCcw } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { useChatPreferences } from '../../hooks/useChatPreferences'

export const menuRowClass = 'flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
export const menuLabelClass = 'px-2.5 pb-1 pt-1.5 text-[11px] font-medium text-muted-foreground'

/** 312000 -> "312K", 1000000 -> "1M". */
export function formatTokens(value: number): string {
  if (value >= 1_000_000) return `${Number((value / 1_000_000).toFixed(1))}M`
  if (value >= 1_000) return `${Math.round(value / 1_000)}K`
  return String(value)
}

/** "5h 4% · 7d 85% (resets 11:30 PM)": the reset shown is the fullest window's, with its date when not today. */
export function chatLimitsText(limits: ChatContextView['limits']): string {
  if (!limits?.length) return ''
  const parts = limits.map(limit => `${limit.label} ${Math.round(limit.used_percent)}%`)
  const fullest = [...limits].sort((a, b) => b.used_percent - a.used_percent)[0]
  const resets = fullest?.resets_at ? new Date(fullest.resets_at) : null
  const today = resets && resets.toDateString() === new Date().toDateString()
  const when = resets && !Number.isNaN(resets.getTime())
    ? ` (resets ${resets.toLocaleString(undefined, today ? { hour: 'numeric', minute: '2-digit' } : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })})` : ''
  return parts.join(' · ') + when
}

/** "5h 4% · 7d 85%": the plan use without reset times, for the chip in the chat box. */
export function chatLimitsShort(limits: ChatContextView['limits']): string {
  return (limits ?? []).map(limit => `${limit.label} ${Math.round(limit.used_percent)}%`).join(' · ')
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
  const percent = windowTokens > 0 ? Math.min(100, Math.round((used / windowTokens) * 100)) : 0
  const limits = chatLimitsText(view.limits)
  const offersSomething = Boolean(view.idle_compaction || view.can_compact || view.can_fresh)
  if (!(used > 0 && windowTokens > 0) && !limits && !offersSomething) return null

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
    {used > 0 && windowTokens > 0 && <div className="px-2.5 pb-1.5 pt-0.5" role="meter" aria-label="Context used" aria-valuemin={0} aria-valuemax={windowTokens} aria-valuenow={used}>
      <div className="text-xs text-muted-foreground">{formatTokens(used)} of {formatTokens(windowTokens)} used</div>
      <div className="mt-1 h-1 overflow-hidden rounded-full bg-muted">
        <div className={`h-full rounded-full ${percent > 50 ? 'bg-amber-500' : 'bg-primary'}`} style={{ width: `${Math.max(percent, 2)}%` }} />
      </div>
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
