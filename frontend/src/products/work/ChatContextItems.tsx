import { openSubscriptionLimits } from '../../utils/subscriptionLimitsNavigation'
import { useChatContextView } from './useChatContextView'
import { useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Check, Info, Layers, RotateCcw } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { formatTokens } from '../../utils/formatTokens'
import { ContextMeter } from '../../components/chat/ContextMeter'

export const menuRowClass = 'flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
const pairButtonClass = 'flex flex-1 cursor-pointer items-center justify-center gap-1.5 rounded-md border border-border px-2 py-1.5 text-xs outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
export const menuLabelClass = 'px-2.5 pb-1 pt-1.5 text-[11px] font-medium text-muted-foreground'

export function resetDate(iso: string | undefined, compact = false): string {
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

/** The chat box keeps only the fullest plan window ("17%"); every window and its reset time are in the hover text. */
export function chatLimitsPeak(limits: ChatContextView['limits']): string {
  const peak = Math.max(-1, ...(limits ?? []).map(limit => limit.used_percent))
  return peak < 0 ? '' : `${Math.round(peak)}%`
}

/** Plan use without reset times ("5h 42% · 7d 56%"); the times are in its hover text. */
export function chatLimitsPercent(limits: ChatContextView['limits']): string {
  return (limits ?? []).map(limit => `${limit.label} ${Math.round(limit.used_percent)}%`).join(' · ')
}

/** One plan window: "5h  12%  ▓░░░  resets 3:00 PM". */
export function PlanWindowRow({ label, percent, resets }: { label: string; percent: number; resets: string }) {
  const value = Math.round(percent)
  return <div className="flex flex-wrap items-center gap-2 px-2.5 pb-1 text-[11px] tabular-nums text-muted-foreground" title={`Your account's plan use, as the provider reports it: ${label} ${value}%${resets ? `, resets ${resets}` : ''}`}>
    <span className="w-10 shrink-0 text-foreground">{label}</span>
    <span className="shrink-0 text-right">{value}% used · {Math.max(0, 100 - value)}% left</span>
    <span className="h-1 w-14 shrink-0 overflow-hidden rounded-full bg-muted" aria-hidden="true"><span className={`block h-full rounded-full ${value >= 90 ? 'bg-diff-del' : 'bg-primary/70'}`} style={{ width: `${Math.min(100, Math.max(0, value))}%` }} /></span>
    <span className="min-w-0">{resets ? `Resets ${resets}` : 'Reset time not reported'}</span>
  </div>
}

/** One-line setting: its explanation is the hover text of the row and its ⓘ. */
function SettingLabel({ label, hint }: { label: string; hint?: string }) {
  return <span className="flex min-w-0 flex-1 items-center gap-1.5" title={hint}>
    <span className="truncate">{label}</span>
    {hint && <Info className="h-3 w-3 shrink-0 text-muted-foreground" aria-label={hint} />}
  </span>
}

/** Sent when the menu changes a chat's context, so the chip in the chat box shows the server's new view. */
export const chatContextChangedEvent = 'agentworks:chat-context-changed'
/** Asks the chat box of one chat to send a message as if the person typed it ({ sessionId, text }). */
export const chatSendMessageEvent = 'agentworks:chat-send-message'
/** Sent by the chip in the chat box to open that chat's model menu. */
export const openChatContextMenuEvent = 'agentworks:open-chat-context-menu'

export type ChatContextChange = { idle_compaction?: boolean; compact_early?: boolean; claude_terminal?: boolean; action?: 'compact' | 'fresh' }

/**
 * Sends one change to the server and shows its answer everywhere: the menu, the chip and the terminal icon
 * all follow chatContextChangedEvent. Returns the server's new view, or null when it refused (a toast says why).
 */
export async function changeChatContext(sessionId: string, body: ChatContextChange): Promise<ChatContextView | null> {
  try {
    const result = await agentApi.setChatContext({ session_id: sessionId, ...body })
    window.dispatchEvent(new CustomEvent(chatContextChangedEvent, { detail: result.context }))
    if (result.message) useChatStore.getState().addToast(result.message, 'success')
    // The terminal had nothing to start from: send the server's short starter message in this chat for the
    // person, so Claude opens in the terminal now instead of waiting for them to type something.
    if (result.starter_message) {
      window.dispatchEvent(new CustomEvent(chatSendMessageEvent, { detail: { sessionId, text: result.starter_message } }))
    }
    return result.context
  } catch (cause) {
    const reason = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
    useChatStore.getState().addToast(reason || 'That did not work. Try again in a moment.', 'error')
    return null
  }
}

/**
 * The "Context" group of a Builder chat's model menu (PLAT-872-71); a Crew chat shows the same in the right pane: how full this chat is, its account's plan limits,
 * and the switches and actions the server offers for it. Everything comes from the server; a choice is sent
 * back and the server's answer is shown. Mounted only while the menu is open, so it reads fresh numbers.
 */
export default function ChatContextItems({ sessionId, modelLabel }: { sessionId: string; modelLabel?: string }) {
  const view = useChatContextView(sessionId)
  const [busy, setBusy] = useState(false)

  if (!view) return null
  const used = view.used_tokens ?? 0
  const windowTokens = view.window_tokens ?? 0
  const limits = chatLimitsText(view.limits)
  const offersSomething = Boolean(view.idle_compaction || view.compact_early || view.can_compact || view.can_fresh)
  if (!(used > 0) && !limits && !offersSomething) return null

  const send = async (body: ChatContextChange) => {
    if (busy) return
    setBusy(true)
    try {
      await changeChatContext(sessionId, body)
      // The shared hook receives the server response through chatContextChangedEvent.
    } finally { setBusy(false) }
  }
  // Keep the menu open after a switch, so the person sees it change.
  const keepOpen = (event: Event) => event.preventDefault()

  const accountLimits = <>
    {(view.limits ?? []).map(limit => <PlanWindowRow key={limit.label} label={limit.label} percent={limit.used_percent} resets={resetDate(limit.resets_at)} />)}
    <Menu.Item className={menuRowClass} onSelect={openSubscriptionLimits}>All accounts and usage</Menu.Item>
  </>
  const context = <>
    {used > 0 && <div className="px-2.5 pb-1.5 pt-0.5">
      <div className="text-xs text-muted-foreground">{formatTokens(used)}{windowTokens > 0 ? ` of ${formatTokens(windowTokens)}` : ''} used</div>
      {windowTokens > 0 && <ContextMeter className="mt-1" used={used} windowTokens={windowTokens} />}
    </div>}
    {view.idle_compaction && <Menu.CheckboxItem className={menuRowClass} checked={view.idle_compaction.on} disabled={busy} onSelect={keepOpen}
      onCheckedChange={checked => void send({ idle_compaction: checked === true })}>
      <SettingLabel label="Auto-compact when idle" hint={view.idle_compaction_hint} />
      <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
    </Menu.CheckboxItem>}
    {view.compact_early && <Menu.CheckboxItem className={menuRowClass} checked={view.compact_early.on} disabled={busy} onSelect={keepOpen}
      onCheckedChange={checked => void send({ compact_early: checked === true })}>
      <SettingLabel label="Compact at 300K" hint={view.compact_early_hint} />
      <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
    </Menu.CheckboxItem>}
    {(view.can_compact || view.can_fresh) && <div className="flex gap-1.5 px-1 py-1">
      {view.can_compact && <Menu.Item className={pairButtonClass} disabled={busy} onSelect={() => void send({ action: 'compact' })}
        title="Summarise this chat now, so each next step reads less.">
        <Layers className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Compact now
      </Menu.Item>}
      {view.can_fresh && <Menu.Item className={pairButtonClass} disabled={busy} onSelect={() => void send({ action: 'fresh' })}
        title="Your next message starts a fresh conversation with a short summary of this one. This chat's history stays here.">
        <RotateCcw className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Fresh chat
      </Menu.Item>}
    </div>}
  </>
  return <>
    <Menu.Separator className="my-1 h-px bg-border first:hidden" />
    <Menu.Label className={menuLabelClass}>Context</Menu.Label>
    {modelLabel && <div className="truncate px-2.5 pb-1 text-[11px] text-muted-foreground">{modelLabel}</div>}
    {accountLimits}
    {context}
  </>
}
